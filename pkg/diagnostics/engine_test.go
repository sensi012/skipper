package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/unified-platform/container/pkg/opmem"
)

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

	// Should trigger both CrashLoop and OOMKilled
	if len(findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(findings))
	}

	// Verify events recorded into operational memory ledger
	events, err := ledger.QueryTimeline("cart-service", 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to query timeline: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected events in ledger, got %d", len(events))
	}
}
