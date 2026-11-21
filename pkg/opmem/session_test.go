package opmem

import (
	"strings"
	"testing"
	"time"
)

func TestRecordDebugSessionWithEventAndReplay(t *testing.T) {
	ledger, err := NewOperationalMemoryLedger(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	if err := ledger.RecordDebugSessionWithEvent(nil); err == nil {
		t.Fatalf("expected error for nil session")
	}

	sess := &DebugSessionRecord{
		WorkloadName: "payments-api",
		TargetPod:    "payments-api-7f9b",
		Runtime:      "kubernetes",
		Namespace:    "prod",
		DebugImage:   "nicolaka/netshoot:latest",
		ExitCode:     137,
		CommandsExecuted: []ExecutedCommand{
			{Command: "curl -v http://localhost:8080/health", ExecutedAt: time.Now().UTC(), ExitCode: 0},
		},
		ResolutionSummary: "Identified DNS resolution timeout",
	}

	if err := ledger.RecordDebugSessionWithEvent(sess); err != nil {
		t.Fatalf("RecordDebugSessionWithEvent failed: %v", err)
	}
	if sess.SessionID == "" || sess.Operator != "skipper-cli" || sess.StartedAt.IsZero() || sess.EndedAt.IsZero() {
		t.Fatalf("expected default metadata populated, got %+v", sess)
	}

	// Verify session lookup by ID
	got, err := ledger.GetDebugSession(sess.SessionID)
	if err != nil {
		t.Fatalf("GetDebugSession failed: %v", err)
	}
	if got.WorkloadName != "payments-api" || got.DebugImage != "nicolaka/netshoot:latest" {
		t.Fatalf("unexpected session: %+v", got)
	}

	// Missing & empty ID errors
	if _, err := ledger.GetDebugSession(""); err == nil {
		t.Fatalf("expected error for empty session ID")
	}
	if _, err := ledger.GetDebugSession("nonexistent"); err == nil {
		t.Fatalf("expected error for missing session ID")
	}

	// Verify timeline event was appended with WARN severity for non-zero exit
	events, err := ledger.QueryTimeline("payments-api", 10*time.Minute)
	if err != nil {
		t.Fatalf("QueryTimeline failed: %v", err)
	}
	if len(events) != 1 || events[0].Type != "DEBUG_SESSION" || events[0].Severity != "WARN" {
		t.Fatalf("expected 1 WARN DEBUG_SESSION event, got %+v", events)
	}

	// Dry-run session with exit 0 -> INFO severity
	drySess := &DebugSessionRecord{
		SessionID:    "dbg-dry-1",
		WorkloadName: "web",
		TargetPod:    "pod/web",
		Runtime:      "docker",
		DebugImage:   "busybox",
		DryRun:       true,
		ExitCode:     0,
	}
	if err := ledger.RecordDebugSessionWithEvent(drySess); err != nil {
		t.Fatalf("RecordDebugSessionWithEvent dry-run failed: %v", err)
	}
	events, _ = ledger.QueryTimeline("web", 10*time.Minute)
	if len(events) != 2 || events[1].Severity != "INFO" || !strings.Contains(events[1].Description, "[dry-run]") {
		t.Fatalf("expected dry-run INFO event, got %+v", events)
	}

	// Replay formatting
	if FormatDebugSessionReplay(nil) != "" {
		t.Fatalf("expected empty replay for nil session")
	}
	replay := FormatDebugSessionReplay(got)
	for _, want := range []string{
		"Session:  " + sess.SessionID,
		"Workload: payments-api (target: payments-api-7f9b)",
		"Runtime:  kubernetes (namespace: prod)",
		"Image:    nicolaka/netshoot:latest",
		"curl -v http://localhost:8080/health",
		"Summary:  Identified DNS resolution timeout",
	} {
		if !strings.Contains(replay, want) {
			t.Errorf("replay missing %q:\n%s", want, replay)
		}
	}
}
