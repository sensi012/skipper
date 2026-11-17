package opmem

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestOperationalMemoryLedger(t *testing.T) {
	dir := t.TempDir()
	ledger, err := NewOperationalMemoryLedger(dir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	// Record Telemetry
	now := time.Now()
	err = ledger.RecordTelemetryEvent(&CausalEvent{
		Timestamp:   now,
		Type:        "METRIC_ANOMALY",
		Source:      "cpu-monitor",
		Description: "High CPU usage",
		Severity:    "WARN",
	})
	if err != nil {
		t.Fatalf("failed to record telemetry: %v", err)
	}

	// Query Timeline
	events, err := ledger.QueryTimeline("my-app", 10*time.Minute)
	if err != nil {
		t.Fatalf("failed to query timeline: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Description != "High CPU usage" {
		t.Fatalf("unexpected event description: %s", events[0].Description)
	}

	// Expired Window Check
	eventsOld, err := ledger.QueryTimeline("my-app", 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	eventsOld, err = ledger.QueryTimeline("my-app", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to query timeline: %v", err)
	}
	if len(eventsOld) != 0 {
		t.Fatalf("expected 0 events for expired window, got %d", len(eventsOld))
	}
}

func TestDebugSessionLedger(t *testing.T) {
	dir := t.TempDir()
	ledger, err := NewOperationalMemoryLedger(dir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	sess1 := &DebugSessionRecord{
		SessionID:    "sess-1",
		WorkloadName: "frontend",
		Operator:     "alice",
		CommandsExecuted: []ExecutedCommand{
			{Command: "ls -la", ExitCode: 0},
		},
	}
	sess2 := &DebugSessionRecord{
		SessionID:    "sess-2",
		WorkloadName: "backend",
		Operator:     "bob",
	}

	if err := ledger.RecordDebugSession(sess1); err != nil {
		t.Fatalf("failed to record session: %v", err)
	}
	if err := ledger.RecordDebugSession(sess2); err != nil {
		t.Fatalf("failed to record session: %v", err)
	}

	// List all
	all, err := ledger.ListDebugSessions("")
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(all))
	}

	// Filter
	fe, err := ledger.ListDebugSessions("frontend")
	if err != nil {
		t.Fatalf("failed to filter sessions: %v", err)
	}
	if len(fe) != 1 || fe[0].SessionID != "sess-1" {
		t.Fatalf("expected frontend session only")
	}
}

func TestConcurrentLedgerWrites(t *testing.T) {
	dir := t.TempDir()
	ledger, err := NewOperationalMemoryLedger(dir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	var wg sync.WaitGroup
	workers := 20
	writesPerWorker := 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(wId int) {
			defer wg.Done()
			for j := 0; j < writesPerWorker; j++ {
				_ = ledger.RecordTelemetryEvent(&CausalEvent{
					Timestamp:   time.Now(),
					Type:        "LOG_ANOMALY",
					Source:      fmt.Sprintf("worker-%d", wId),
					Description: fmt.Sprintf("event-%d", j),
					Severity:    "INFO",
				})
			}
		}(i)
	}

	wg.Wait()
	events, err := ledger.QueryTimeline("", 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to query timeline: %v", err)
	}
	if len(events) != workers*writesPerWorker {
		t.Fatalf("expected %d events, got %d", workers*writesPerWorker, len(events))
	}
}
