package doctor

import (
	"context"
	"net"
	"testing"
)

func TestDoctorPortConflicts(t *testing.T) {
	engine := NewDoctorEngine()

	// Occupy a temporary port to trigger a collision
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on ephemeral port: %v", err)
	}
	defer l.Close()

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
