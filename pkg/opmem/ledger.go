package opmem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

func validateSessionID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("session ID is required")
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, `/\`) {
		return fmt.Errorf("invalid session ID %q: path traversal or separators not allowed", id)
	}
	if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, ".") {
		return fmt.Errorf("invalid session ID %q: must not start with '-' or '.'", id)
	}
	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid session ID %q: whitespace or control characters not allowed", id)
		}
	}
	return nil
}

// ChangeActor represents who or what triggered a system mutation
type ChangeActor struct {
	Type       string `json:"type"` // user | ci-pipeline | gitops-controller | ai-agent
	Identifier string `json:"identifier"`
	IPAddress  string `json:"ipAddress,omitempty"`
}

// TargetWorkload identifies the subject of a mutation
type TargetWorkload struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
}

// GitContext preserves source control lineage
type GitContext struct {
	CommitSha string `json:"commitSha,omitempty"`
	Branch    string `json:"branch,omitempty"`
	RepoURL   string `json:"repoUrl,omitempty"`
	Author    string `json:"author,omitempty"`
}

// ChangeRecord represents an immutable mutation in the cluster ledger
type ChangeRecord struct {
	ID             string                 `json:"id"`
	Timestamp      time.Time              `json:"timestamp"`
	Actor          ChangeActor            `json:"actor"`
	TargetWorkload TargetWorkload         `json:"targetWorkload"`
	ChangeType     string                 `json:"changeType"` // image_rollout, config_update, scale_event
	GitContext     GitContext             `json:"gitContext,omitempty"`
	Diff           map[string]interface{} `json:"diff"`
}

// ExecutedCommand records an interactive command executed inside a debug container
type ExecutedCommand struct {
	Command       string    `json:"command"`
	ExecutedAt    time.Time `json:"executedAt"`
	ExitCode      int       `json:"exitCode"`
	StdoutSnippet string    `json:"stdoutSnippet,omitempty"`
	StderrSnippet string    `json:"stderrSnippet,omitempty"`
}

// DebugSessionRecord preserves ephemeral troubleshooting sessions permanently
type DebugSessionRecord struct {
	SessionID         string            `json:"sessionId"`
	StartedAt         time.Time         `json:"startedAt"`
	EndedAt           time.Time         `json:"endedAt"`
	Operator          string            `json:"operator"`
	TargetPod         string            `json:"targetPod"`
	WorkloadName      string            `json:"workloadName"`
	Runtime           string            `json:"runtime,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	DebugImage        string            `json:"debugImage,omitempty"`
	DryRun            bool              `json:"dryRun,omitempty"`
	CommandsExecuted  []ExecutedCommand `json:"commandsExecuted"`
	ExitCode          int               `json:"exitCode"`
	ResolutionSummary string            `json:"resolutionSummary,omitempty"`
}

// CausalEvent correlates mutations, telemetry signals, and symptoms chronologically
type CausalEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"` // MUTATION | METRIC_ANOMALY | SYSTEM_SIGNAL | LOG_ANOMALY
	Source      string    `json:"source"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"` // INFO | WARN | CRITICAL
}

// OperationalMemoryLedger manages persistent operational context
type OperationalMemoryLedger struct {
	mu         sync.RWMutex
	storageDir string
}

// NewOperationalMemoryLedger initializes local operational memory storage
func NewOperationalMemoryLedger(storageDir string) (*OperationalMemoryLedger, error) {
	if storageDir == "" {
		home, _ := os.UserHomeDir()
		storageDir = filepath.Join(home, ".skipper", "opmem")
	}
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create operational memory directory: %w", err)
	}
	return &OperationalMemoryLedger{storageDir: storageDir}, nil
}

// RecordChange writes a mutation to the immutable append-only ledger
func (l *OperationalMemoryLedger) RecordChange(rec *ChangeRecord) error {
	if rec == nil {
		return fmt.Errorf("change record cannot be nil")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	recPath := filepath.Join(l.storageDir, "changes.jsonl")
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(recPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(string(data) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// RecordDebugSession saves a completed interactive debug session
func (l *OperationalMemoryLedger) RecordDebugSession(sess *DebugSessionRecord) error {
	if sess == nil {
		return fmt.Errorf("debug session record cannot be nil")
	}
	if strings.TrimSpace(sess.SessionID) != "" {
		if err := validateSessionID(sess.SessionID); err != nil {
			return err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.recordDebugSessionLocked(sess)
}

func (l *OperationalMemoryLedger) recordDebugSessionLocked(sess *DebugSessionRecord) error {
	sessPath := filepath.Join(l.storageDir, "debug-sessions.jsonl")
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(sessPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(string(data) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// RecordTelemetryEvent records an eBPF or runtime signal into the causal ledger
func (l *OperationalMemoryLedger) RecordTelemetryEvent(evt *CausalEvent) error {
	if evt == nil {
		return fmt.Errorf("causal event cannot be nil")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.recordTelemetryEventLocked(evt)
}

func (l *OperationalMemoryLedger) recordTelemetryEventLocked(evt *CausalEvent) error {
	evtPath := filepath.Join(l.storageDir, "events.jsonl")
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(evtPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(string(data) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// QueryTimeline retrieves all causal events, mutations, and debug sessions within a time window
func (l *OperationalMemoryLedger) QueryTimeline(workload string, window time.Duration) ([]CausalEvent, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	evtPath := filepath.Join(l.storageDir, "events.jsonl")
	if _, err := os.Stat(evtPath); os.IsNotExist(err) {
		return []CausalEvent{}, nil
	}

	data, err := os.ReadFile(evtPath)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-window)
	var result []CausalEvent

	lines := splitLines(string(data))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var evt CausalEvent
		if err := json.Unmarshal([]byte(line), &evt); err == nil {
			if evt.Timestamp.After(cutoff) {
				result = append(result, evt)
			}
		}
	}
	return result, nil
}

// ListDebugSessions returns past preserved debugging sessions
func (l *OperationalMemoryLedger) ListDebugSessions(workload string) ([]DebugSessionRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	sessPath := filepath.Join(l.storageDir, "debug-sessions.jsonl")
	if _, err := os.Stat(sessPath); os.IsNotExist(err) {
		return []DebugSessionRecord{}, nil
	}

	data, err := os.ReadFile(sessPath)
	if err != nil {
		return nil, err
	}

	var result []DebugSessionRecord
	lines := splitLines(string(data))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var sess DebugSessionRecord
		if err := json.Unmarshal([]byte(line), &sess); err == nil {
			if workload == "" || sess.WorkloadName == workload {
				result = append(result, sess)
			}
		}
	}
	return result, nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
