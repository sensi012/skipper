package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("HELPER_STDOUT"))
	if os.Getenv("HELPER_EXIT_CODE") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDoctorPortConflicts(t *testing.T) {
	engine := NewDoctorEngine()

	// Occupy a temporary port to trigger a collision
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on ephemeral port: %v", err)
	}
	defer func() { _ = l.Close() }()

	port := l.Addr().(*net.TCPAddr).Port
	checks := engine.checkPortConflicts([]int{port})
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != StatusWarn {
		t.Fatalf("expected StatusWarn for occupied port %d, got %s", port, checks[0].Status)
	}
}

func TestDoctorEngineRun(t *testing.T) {
	engine := NewDoctorEngine()
	report := engine.RunDiagnostics(context.Background())
	if report == nil {
		t.Fatalf("expected non-nil report")
	}
	if len(report.Checks) == 0 {
		t.Fatalf("expected diagnostic checks in report")
	}
}

func TestDoctorEngineFailureAndWarningBranches(t *testing.T) {
	engine := &DoctorEngine{
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			cs := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
			cmd := exec.CommandContext(ctx, os.Args[0], cs...)
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"HELPER_EXIT_CODE=1",
			)
			return cmd
		},
	}

	report := engine.RunDiagnostics(context.Background())
	if report.Passed {
		t.Fatalf("expected report.Passed=false when Docker check fails")
	}
	if report.Checks[0].Status != StatusFail || report.Checks[1].Status != StatusWarn || report.Checks[2].Status != StatusWarn {
		t.Fatalf("expected FAIL/WARN/WARN for docker/kubectl/kind failure branch, got %+v", report.Checks[:3])
	}
}
