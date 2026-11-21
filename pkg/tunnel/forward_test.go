package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("HELPER_STDOUT") != "" {
		fmt.Fprint(os.Stdout, os.Getenv("HELPER_STDOUT"))
	}
	if os.Getenv("HELPER_STDERR") != "" {
		fmt.Fprint(os.Stderr, os.Getenv("HELPER_STDERR"))
	}
	if os.Getenv("HELPER_EXIT_CODE") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func fakeExecCommand(stdout, stderr string, exitCode int, recorded *[]string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if recorded != nil {
			*recorded = append([]string{name}, args...)
		}
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_STDOUT="+stdout,
			"HELPER_STDERR="+stderr,
			fmt.Sprintf("HELPER_EXIT_CODE=%d", exitCode),
		)
		return cmd
	}
}

func TestParsePortString(t *testing.T) {
	tests := []struct {
		input     string
		expectedL int
		expectedR int
		hasErr    bool
	}{
		{"8080", 8080, 8080, false},
		{" 80 ", 80, 80, false},
		{"1", 1, 1, false},
		{"65535", 65535, 65535, false},
		{"3000:80", 3000, 80, false},
		{"1:65535", 1, 65535, false},
		{"", 0, 0, true},
		{"   ", 0, 0, true},
		{"0", 0, 0, true},
		{"-1", 0, 0, true},
		{"65536", 0, 0, true},
		{"0:80", 0, 0, true},
		{"80:0", 0, 0, true},
		{"-80:80", 0, 0, true},
		{"80:70000", 0, 0, true},
		{"invalid", 0, 0, true},
		{"abc:80", 0, 0, true},
		{"80:abc", 0, 0, true},
		{":80", 0, 0, true},
		{"80:", 0, 0, true},
		{"80:80:80", 0, 0, true},
	}

	for _, tc := range tests {
		l, r, err := ParsePortString(tc.input)
		if tc.hasErr && err == nil {
			t.Fatalf("expected error for %q", tc.input)
		}
		if !tc.hasErr && err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.input, err)
		}
		if !tc.hasErr && (l != tc.expectedL || r != tc.expectedR) {
			t.Fatalf("for %q expected %d:%d, got %d:%d", tc.input, tc.expectedL, tc.expectedR, l, r)
		}
	}
}

func TestPortForwarderForward(t *testing.T) {
	t.Run("default namespace and stdout/stderr wiring", func(t *testing.T) {
		var recorded []string
		pf := NewPortForwarder()
		pf.execCommand = fakeExecCommand("Forwarding from 127.0.0.1:8080 -> 80\n", "", 0, &recorded)

		var outBuf, errBuf bytes.Buffer
		err := pf.Forward(context.Background(), PortForwardOptions{
			WorkloadName: "web-api",
			LocalPort:    8080,
			RemotePort:   80,
		}, &outBuf, &errBuf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		wantArgs := []string{"kubectl", "port-forward", "deployment/web-api", "8080:80", "-n", "default"}
		if strings.Join(recorded, " ") != strings.Join(wantArgs, " ") {
			t.Fatalf("expected command %v, got %v", wantArgs, recorded)
		}
		if !strings.Contains(outBuf.String(), "Establishing port-forward to web-api [Local :8080 -> Remote :80] in namespace 'default'") {
			t.Errorf("unexpected banner output: %s", outBuf.String())
		}
		if !strings.Contains(outBuf.String(), "Forwarding from 127.0.0.1:8080 -> 80") {
			t.Errorf("expected subprocess stdout in outBuf, got: %s", outBuf.String())
		}
	})

	t.Run("custom namespace and nil writers", func(t *testing.T) {
		var recorded []string
		pf := &PortForwarder{
			execCommand: fakeExecCommand("", "", 0, &recorded),
		}

		err := pf.Forward(context.Background(), PortForwardOptions{
			Namespace:    "staging",
			WorkloadName: "payments",
			LocalPort:    9000,
			RemotePort:   9090,
		}, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		wantArgs := []string{"kubectl", "port-forward", "deployment/payments", "9000:9090", "-n", "staging"}
		if strings.Join(recorded, " ") != strings.Join(wantArgs, " ") {
			t.Fatalf("expected command %v, got %v", wantArgs, recorded)
		}
	})

	t.Run("empty workload name returns error", func(t *testing.T) {
		pf := NewPortForwarder()
		err := pf.Forward(context.Background(), PortForwardOptions{
			LocalPort:  8080,
			RemotePort: 80,
		}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "workload name is required") {
			t.Fatalf("expected workload name error, got %v", err)
		}
	})

	t.Run("command failure is wrapped", func(t *testing.T) {
		pf := NewPortForwarder()
		pf.execCommand = fakeExecCommand("", "Error from server (NotFound)", 1, nil)

		var outBuf, errBuf bytes.Buffer
		err := pf.Forward(context.Background(), PortForwardOptions{
			Namespace:    "prod",
			WorkloadName: "missing-svc",
			LocalPort:    8080,
			RemotePort:   8080,
		}, &outBuf, &errBuf)
		if err == nil || !strings.Contains(err.Error(), "port-forward failed or interrupted") {
			t.Fatalf("expected wrapped port-forward error, got %v", err)
		}
		if !strings.Contains(errBuf.String(), "NotFound") {
			t.Errorf("expected stderr output, got: %s", errBuf.String())
		}
	})
}
