package opmem

import (
	"fmt"
	"strings"
	"time"
)

// RecordDebugSessionWithEvent persists a debug session record and appends a
// corresponding DEBUG_SESSION causal event to the operational memory timeline
// so `skipper time-travel` and MCP incident tools can replay debug sessions.
func (l *OperationalMemoryLedger) RecordDebugSessionWithEvent(sess *DebugSessionRecord) error {
	if sess == nil {
		return fmt.Errorf("debug session record cannot be nil")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UTC()
	if strings.TrimSpace(sess.SessionID) == "" {
		sess.SessionID = fmt.Sprintf("dbg-%d", now.UnixNano())
	} else if err := validateSessionID(sess.SessionID); err != nil {
		return err
	}
	if sess.StartedAt.IsZero() {
		sess.StartedAt = now
	}
	if sess.EndedAt.IsZero() {
		sess.EndedAt = now
	}
	if strings.TrimSpace(sess.Operator) == "" {
		sess.Operator = "skipper-cli"
	}

	if err := l.recordDebugSessionLocked(sess); err != nil {
		return err
	}

	severity := "INFO"
	if sess.ExitCode != 0 {
		severity = "WARN"
	}

	desc := fmt.Sprintf("Ephemeral debug session %s on workload %s (target=%s, runtime=%s, image=%s, exit=%d)",
		sess.SessionID, sess.WorkloadName, sess.TargetPod, sess.Runtime, sess.DebugImage, sess.ExitCode)
	if sess.DryRun {
		desc = "[dry-run] " + desc
	}

	return l.recordTelemetryEventLocked(&CausalEvent{
		Timestamp:   sess.StartedAt,
		Type:        "DEBUG_SESSION",
		Source:      "ephemeral-sandbox",
		Description: desc,
		Severity:    severity,
	})
}

// GetDebugSession retrieves a previously recorded debug session by its SessionID.
func (l *OperationalMemoryLedger) GetDebugSession(sessionID string) (*DebugSessionRecord, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	sessions, err := l.ListDebugSessions("")
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].SessionID == sessionID {
			return &sessions[i], nil
		}
	}
	return nil, fmt.Errorf("debug session %q not found", sessionID)
}

// FormatDebugSessionReplay formats a debug session into a human-readable replay transcript.
func FormatDebugSessionReplay(sess *DebugSessionRecord) string {
	if sess == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Session:  %s\n", sess.SessionID)
	fmt.Fprintf(&b, "Workload: %s (target: %s)\n", sess.WorkloadName, sess.TargetPod)
	if sess.Runtime != "" || sess.Namespace != "" {
		fmt.Fprintf(&b, "Runtime:  %s (namespace: %s)\n", sess.Runtime, sess.Namespace)
	}
	if sess.DebugImage != "" {
		fmt.Fprintf(&b, "Image:    %s\n", sess.DebugImage)
	}
	fmt.Fprintf(&b, "Operator: %s\n", sess.Operator)
	fmt.Fprintf(&b, "Window:   %s -> %s (exit=%d)\n",
		sess.StartedAt.Format(time.RFC3339), sess.EndedAt.Format(time.RFC3339), sess.ExitCode)
	if len(sess.CommandsExecuted) > 0 {
		b.WriteString("Commands:\n")
		for _, cmd := range sess.CommandsExecuted {
			fmt.Fprintf(&b, "  [%s] $ %s (exit=%d)\n", cmd.ExecutedAt.Format(time.RFC3339), cmd.Command, cmd.ExitCode)
		}
	}
	if sess.ResolutionSummary != "" {
		fmt.Fprintf(&b, "Summary:  %s\n", sess.ResolutionSummary)
	}
	return b.String()
}
