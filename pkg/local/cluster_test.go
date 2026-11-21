package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("HELPER_DUMP_STDIN") == "1" {
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, string(data))
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

type fakeStep struct {
	stdout    string
	stderr    string
	exitCode  int
	dumpStdin bool
}

func makeFakeExec(steps []fakeStep, calls *[][]string) func(context.Context, string, ...string) *exec.Cmd {
	idx := 0
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if calls != nil {
			*calls = append(*calls, append([]string{name}, args...))
		}
		var step fakeStep
		if idx < len(steps) {
			step = steps[idx]
			idx++
		}
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		env := append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_STDOUT="+step.stdout,
			"HELPER_STDERR="+step.stderr,
			fmt.Sprintf("HELPER_EXIT_CODE=%d", step.exitCode),
		)
		if step.dumpStdin {
			env = append(env, "HELPER_DUMP_STDIN=1")
		}
		cmd.Env = env
		return cmd
	}
}

func TestKindManagerName(t *testing.T) {
	km := NewKindManager()
	if km.Name() != "kind" {
		t.Fatalf("expected kind, got %s", km.Name())
	}
}

func TestBuildKindConfig(t *testing.T) {
	cfg := buildKindConfig()
	for _, want := range []string{
		"kind: Cluster",
		"apiVersion: kind.x-k8s.io/v1alpha4",
		"role: control-plane",
		`node-labels: "ingress-ready=true"`,
		"containerPort: 80",
		"hostPort: 80",
		"containerPort: 443",
		"hostPort: 443",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("kind config missing %q", want)
		}
	}
}

func TestKindManagerExists(t *testing.T) {
	t.Run("finds matching cluster in multi-line CRLF output", func(t *testing.T) {
		var calls [][]string
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stdout: "dev-a\r\nskipper-local\r\nother\r\n"}}, &calls),
		}
		exists, err := km.Exists(context.Background(), "skipper-local")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatalf("expected cluster to exist")
		}
		if len(calls) != 1 || strings.Join(calls[0], " ") != "kind get clusters" {
			t.Fatalf("unexpected command calls: %v", calls)
		}
	})

	t.Run("returns false when cluster not listed", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stdout: "No kind clusters found.\n"}}, nil),
		}
		exists, err := km.Exists(context.Background(), "skipper-local")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatalf("expected cluster not to exist")
		}
	})

	t.Run("empty cluster name returns false without invoking kind", func(t *testing.T) {
		var calls [][]string
		km := &KindManager{
			execCommand: makeFakeExec(nil, &calls),
		}
		exists, err := km.Exists(context.Background(), "   ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatalf("expected false for empty cluster name")
		}
		if len(calls) != 0 {
			t.Fatalf("expected no command calls for empty cluster name, got %v", calls)
		}
	})

	t.Run("propagates kind command error", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stderr: "docker not running", exitCode: 1}}, nil),
		}
		_, err := km.Exists(context.Background(), "skipper-local")
		if err == nil || !strings.Contains(err.Error(), "failed to list kind clusters") {
			t.Fatalf("expected list error, got %v", err)
		}
	})
}

func TestKindManagerCreate(t *testing.T) {
	t.Run("creates cluster when it does not exist", func(t *testing.T) {
		var calls [][]string
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{
				{stdout: ""},                // kind get clusters
				{stdout: "cluster created"}, // kind create cluster
			}, &calls),
		}
		if err := km.Create(context.Background(), "skipper-dev"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(calls) != 2 {
			t.Fatalf("expected 2 commands, got %d: %v", len(calls), calls)
		}
		wantCreate := "kind create cluster --name skipper-dev --config -"
		if strings.Join(calls[1], " ") != wantCreate {
			t.Fatalf("expected %q, got %q", wantCreate, strings.Join(calls[1], " "))
		}
	})

	t.Run("returns error when cluster already exists", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stdout: "skipper-dev\n"}}, nil),
		}
		err := km.Create(context.Background(), "skipper-dev")
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected already exists error, got %v", err)
		}
	})

	t.Run("returns error for empty cluster name", func(t *testing.T) {
		km := NewKindManager()
		if err := km.Create(context.Background(), ""); err == nil {
			t.Fatalf("expected error for empty cluster name")
		}
	})

	t.Run("propagates Exists error", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stderr: "boom", exitCode: 1}}, nil),
		}
		err := km.Create(context.Background(), "skipper-dev")
		if err == nil || !strings.Contains(err.Error(), "failed to list kind clusters") {
			t.Fatalf("expected list error, got %v", err)
		}
	})

	t.Run("propagates kind create error with output", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{
				{stdout: ""},
				{stderr: "port 80 already allocated", exitCode: 1},
			}, nil),
		}
		err := km.Create(context.Background(), "skipper-dev")
		if err == nil || !strings.Contains(err.Error(), "kind create cluster failed") || !strings.Contains(err.Error(), "port 80 already allocated") {
			t.Fatalf("expected create error with output, got %v", err)
		}
	})
}

func TestKindManagerDelete(t *testing.T) {
	t.Run("deletes cluster", func(t *testing.T) {
		var calls [][]string
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stdout: "Deleted nodes"}}, &calls),
		}
		if err := km.Delete(context.Background(), "skipper-dev"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "kind delete cluster --name skipper-dev"
		if len(calls) != 1 || strings.Join(calls[0], " ") != want {
			t.Fatalf("expected %q, got %v", want, calls)
		}
	})

	t.Run("returns error for empty cluster name", func(t *testing.T) {
		km := NewKindManager()
		if err := km.Delete(context.Background(), " "); err == nil {
			t.Fatalf("expected error for empty cluster name")
		}
	})

	t.Run("propagates delete error", func(t *testing.T) {
		km := &KindManager{
			execCommand: makeFakeExec([]fakeStep{{stderr: "delete failed", exitCode: 1}}, nil),
		}
		err := km.Delete(context.Background(), "skipper-dev")
		if err == nil || !strings.Contains(err.Error(), "kind delete cluster failed") {
			t.Fatalf("expected delete error, got %v", err)
		}
	})
}

func TestKindManagerLiveSafeExistsCheck(t *testing.T) {
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("kind binary not installed; skipping live check")
	}
	km := NewKindManager()
	exists, err := km.Exists(context.Background(), "skipper-nonexistent-unit-test-cluster-99999")
	if err != nil {
		t.Fatalf("live kind get clusters failed: %v", err)
	}
	if exists {
		t.Fatalf("expected nonexistent test cluster not to exist")
	}
}

func TestKindManagerFlagInjectionRejection(t *testing.T) {
	km := NewKindManager()
	for _, bad := range []string{"--kubeconfig=/etc/passwd", "-n", "cluster with space", "cluster\nnewline"} {
		if _, err := km.Exists(context.Background(), bad); err == nil {
			t.Errorf("Exists should reject unsafe cluster name %q", bad)
		}
		if err := km.Create(context.Background(), bad); err == nil {
			t.Errorf("Create should reject unsafe cluster name %q", bad)
		}
		if err := km.Delete(context.Background(), bad); err == nil {
			t.Errorf("Delete should reject unsafe cluster name %q", bad)
		}
	}
}
