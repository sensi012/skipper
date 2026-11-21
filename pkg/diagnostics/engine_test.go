package diagnostics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/opmem"
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

func fakeDiagExec(stdout string, exitCode int) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_STDOUT="+stdout,
			fmt.Sprintf("HELPER_EXIT_CODE=%d", exitCode),
		)
		return cmd
	}
}

func TestDetectors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		detector     Detector
		triggerLogs  string
		cleanLogs    string
		expectedName string
	}{
		{"OOMKilled", &OOMKilledDetector{}, "fatal error: exit code 137 triggered", "all normal", "OOMKilledDetector"},
		{"CrashLoop", &CrashLoopDetector{}, "panic: runtime error: invalid memory address", "all normal", "CrashLoopDetector"},
		{"ImagePull", &ImagePullDetector{}, "Failed to pull image: ErrImagePull", "all normal", "ImagePullDetector"},
		{"ReadinessProbe", &ReadinessProbeDetector{}, "Readiness probe failed: HTTP 500", "all normal", "ReadinessProbeDetector"},
		{"PendingPod", &PendingPodDetector{}, "0/1 nodes are available: Insufficient cpu", "all normal", "PendingPodDetector"},
		{"NetworkDrop", &NetworkDropDetector{}, "NetworkPolicy dropped cross-cluster packet", "all normal", "NetworkDropDetector"},
		{"IngressRouting", &IngressRoutingDetector{}, "503 Service Temporarily Unavailable", "all normal", "IngressRoutingDetector"},
		{"DNSResolution", &DNSResolutionDetector{}, "dial tcp: lookup auth.internal: server misbehaving", "all normal", "DNSResolutionDetector"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.detector.Name() != tc.expectedName {
				t.Fatalf("expected name %s, got %s", tc.expectedName, tc.detector.Name())
			}

			// Test trigger
			finding, err := tc.detector.Evaluate(ctx, "default", "test-pod", tc.triggerLogs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if finding == nil {
				t.Fatalf("expected finding for trigger logs")
			}

			// Test clean
			cleanFinding, err := tc.detector.Evaluate(ctx, "default", "test-pod", tc.cleanLogs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cleanFinding != nil {
				t.Fatalf("expected no finding for clean logs")
			}
		})
	}
}

func TestDeterministicMatrix(t *testing.T) {
	tempDir := t.TempDir()
	ledger, err := opmem.NewOperationalMemoryLedger(tempDir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	matrix := NewDeterministicMatrix(ledger)
	logs := `
		panic: nil pointer dereference
		exit code 137
	`
	findings, err := matrix.Diagnose(context.Background(), "default", "cart-service", logs)
	if err != nil {
		t.Fatalf("unexpected diagnose error: %v", err)
	}

	if len(findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(findings))
	}

	events, err := ledger.QueryTimeline("cart-service", 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to query timeline: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected events in ledger, got %d", len(events))
	}
}

func TestContextGroundedEngineAndInputValidation(t *testing.T) {
	ledger, err := opmem.NewOperationalMemoryLedger(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}
	_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
		Timestamp:   time.Now().UTC(),
		Type:        "MUTATION",
		Source:      "deploy",
		Description: "Rolled out v2.1",
		Severity:    "INFO",
	})

	eng := NewContextGroundedEngine(ledger)
	eng.execCommand = fakeDiagExec("OOMKilled: exit code 137\n", 0)

	findings, err := eng.RunDiagnostics(context.Background(), "", "orders-api")
	if err != nil {
		t.Fatalf("RunDiagnostics failed: %v", err)
	}
	if len(findings) != 1 || findings[0].DetectorName != "OOMKilledDetector" {
		t.Fatalf("expected OOMKilled finding, got %+v", findings)
	}
	lastEvidence := findings[0].Evidence[len(findings[0].Evidence)-1]
	if !strings.Contains(lastEvidence, "Preceded by mutation") {
		t.Errorf("expected timeline-grounded evidence, got %v", findings[0].Evidence)
	}

	// Nil ledger / nil matrix does not panic
	nilEng := &ContextGroundedEngine{
		execCommand: fakeDiagExec("ImagePullBackOff\n", 0),
	}
	if _, err := nilEng.RunDiagnostics(context.Background(), "prod", "orders-api"); err != nil {
		t.Fatalf("RunDiagnostics with nil ledger/matrix failed: %v", err)
	}

	// Flag injection & empty workload validation
	if _, err := eng.RunDiagnostics(context.Background(), "default", ""); err == nil {
		t.Errorf("expected error for empty workload")
	}
	for _, bad := range []string{"--kubeconfig=/etc/passwd", "-n", "bad name", "bad\nname"} {
		if _, err := eng.RunDiagnostics(context.Background(), "default", bad); err == nil {
			t.Errorf("RunDiagnostics should reject unsafe workload %q", bad)
		}
		if _, err := eng.RunDiagnostics(context.Background(), bad, "orders-api"); err == nil {
			t.Errorf("RunDiagnostics should reject unsafe namespace %q", bad)
		}
	}
}
