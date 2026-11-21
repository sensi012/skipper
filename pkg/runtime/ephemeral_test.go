package runtime

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/opmem"
)

func TestEphemeralSandboxKubernetesLaunchAndLedger(t *testing.T) {
	ledger, err := opmem.NewOperationalMemoryLedger(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	var calls [][]string
	launcher := NewEphemeralSandboxLauncher(ledger)
	launcher.execCommand = makeFakeExec([]fakeStep{
		{stdout: "orders-api-6d9f-abc\n", exitCode: 0}, // kubectl get pods
		{stdout: "connected\n", exitCode: 0},            // kubectl debug
	}, &calls)

	var out, errBuf bytes.Buffer
	res, err := launcher.Launch(context.Background(), EphemeralSandboxOptions{
		WorkloadName:    "orders-api",
		Namespace:       "prod",
		Runtime:         "kubernetes",
		TargetContainer: "app",
		CopyPod:         true,
		ShareProcesses:  true,
		Command:         []string{"sh", "-c", "ps aux"},
		Operator:        "sre-bot",
	}, nil, &out, &errBuf)
	if err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if !res.Recorded || res.ExitCode != 0 || res.Target != "orders-api-6d9f-abc" {
		t.Fatalf("unexpected result: %+v", res)
	}

	wantCmd := []string{
		"kubectl", "debug", "-it", "orders-api-6d9f-abc",
		"-n", "prod",
		"--image=nicolaka/netshoot:latest",
		"--target=app",
		"--copy-to=orders-api-debug",
		"--share-processes",
		"--", "sh", "-c", "ps aux",
	}
	if !reflect.DeepEqual(res.Command, wantCmd) {
		t.Fatalf("command mismatch:\ngot:  %v\nwant: %v", res.Command, wantCmd)
	}

	// Verify session was recorded in opmem ledger and timeline
	sessions, err := ledger.ListDebugSessions("orders-api")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected 1 recorded session, got %d (err=%v)", len(sessions), err)
	}
	if sessions[0].SessionID != res.SessionID || sessions[0].Operator != "sre-bot" {
		t.Fatalf("unexpected session record: %+v", sessions[0])
	}
	events, err := ledger.QueryTimeline("orders-api", 5*time.Minute)
	if err != nil || len(events) != 1 || events[0].Type != "DEBUG_SESSION" {
		t.Fatalf("expected DEBUG_SESSION timeline event, got %+v (err=%v)", events, err)
	}
}

func TestEphemeralSandboxDockerAndDryRun(t *testing.T) {
	ledger, err := opmem.NewOperationalMemoryLedger(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	t.Run("docker live execution with non-zero exit", func(t *testing.T) {
		var calls [][]string
		launcher := NewEphemeralSandboxLauncher(ledger)
		launcher.execCommand = makeFakeExec([]fakeStep{{stderr: "exit 2", exitCode: 2}}, &calls)

		var out, errBuf bytes.Buffer
		res, err := launcher.Launch(context.Background(), EphemeralSandboxOptions{
			WorkloadName: "payments-ctr",
			Runtime:      "docker",
			DebugImage:   "busybox:1.36",
		}, nil, &out, &errBuf)
		if err != nil {
			t.Fatalf("expected nil err on exit code, got %v", err)
		}
		if res.ExitCode != 2 || !res.Recorded {
			t.Fatalf("expected exit 2 and recorded=true, got %+v", res)
		}
		want := []string{
			"docker", "run", "--rm", "-it",
			"--pid=container:payments-ctr",
			"--network=container:payments-ctr",
			"busybox:1.36", "/bin/sh",
		}
		if !reflect.DeepEqual(res.Command, want) {
			t.Fatalf("docker command mismatch:\ngot:  %v\nwant: %v", res.Command, want)
		}
	})

	t.Run("kubernetes dry-run does not execute kubectl", func(t *testing.T) {
		var calls [][]string
		launcher := NewEphemeralSandboxLauncher(ledger)
		launcher.execCommand = makeFakeExec(nil, &calls)

		res, err := launcher.Launch(context.Background(), EphemeralSandboxOptions{
			WorkloadName: "web-api",
			CopyPod:      true,
			CopyPodName:  "custom-copy",
			DryRun:       true,
		}, nil, nil, nil)
		if err != nil {
			t.Fatalf("dry-run failed: %v", err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected 0 exec calls in dry-run, got %v", calls)
		}
		if !res.DryRun || res.Target != "pod/web-api" || !strings.Contains(strings.Join(res.Command, " "), "--copy-to=custom-copy") {
			t.Fatalf("unexpected dry-run result: %+v", res)
		}
	})

	t.Run("validation and pod discovery error paths", func(t *testing.T) {
		launcher := NewEphemeralSandboxLauncher(nil)
		if _, _, _, err := launcher.BuildCommand(context.Background(), EphemeralSandboxOptions{Runtime: "docker"}); err == nil {
			t.Fatalf("expected error for missing docker workload")
		}
		if _, _, _, err := launcher.BuildCommand(context.Background(), EphemeralSandboxOptions{Runtime: "kubernetes"}); err == nil {
			t.Fatalf("expected error for missing k8s workload/pod")
		}
		if _, _, _, err := launcher.BuildCommand(context.Background(), EphemeralSandboxOptions{Runtime: "nomad", WorkloadName: "x"}); err == nil {
			t.Fatalf("expected error for unsupported runtime")
		}

		launcher.execCommand = makeFakeExec([]fakeStep{{stdout: "  \n", exitCode: 0}}, nil)
		if _, err := launcher.Launch(context.Background(), EphemeralSandboxOptions{WorkloadName: "ghost"}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "no pods found") {
			t.Fatalf("expected no pods found error, got %v", err)
		}
	})
}
