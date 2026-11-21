package rollout

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sensi012/skipper/pkg/opmem"
)

// RollbackOptions configures a live or dry-run Kubernetes deployment rollback.
type RollbackOptions struct {
	WorkloadName string `json:"workloadName"`
	Namespace    string `json:"namespace,omitempty"`
	Revision     string `json:"revision,omitempty"`
	Reason       string `json:"reason,omitempty"`
	ContextName  string `json:"contextName,omitempty"`
	DryRun       bool   `json:"dryRun,omitempty"`
}

// RollbackResult describes the outcome of a deployment rollback operation.
type RollbackResult struct {
	Workload       string    `json:"workload"`
	Namespace      string    `json:"namespace"`
	TargetRevision string    `json:"targetRevision"`
	Reason         string    `json:"reason,omitempty"`
	Status         string    `json:"status"`
	ChangeID       string    `json:"changeId"`
	Command        []string  `json:"command"`
	DryRun         bool      `json:"dryRun"`
	Executed       bool      `json:"executed"`
	RecordedInMem  bool      `json:"recordedInMem"`
	Output         string    `json:"output,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
}

// RollbackManager executes Kubernetes deployment rollbacks and records them in operational memory.
type RollbackManager struct {
	Ledger      *opmem.OperationalMemoryLedger
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewRollbackManager creates a RollbackManager optionally backed by an OperationalMemoryLedger.
func NewRollbackManager(ledger *opmem.OperationalMemoryLedger) *RollbackManager {
	return &RollbackManager{
		Ledger:      ledger,
		execCommand: exec.CommandContext,
	}
}

func validateRollbackToken(field, val string, disallowSlashes bool) error {
	if strings.HasPrefix(val, "-") {
		return fmt.Errorf("invalid %s %q: must not start with '-'", field, val)
	}
	if strings.ContainsAny(val, " \t\r\n\x00") {
		return fmt.Errorf("invalid %s %q: must not contain whitespace or control characters", field, val)
	}
	if disallowSlashes && (strings.ContainsAny(val, "/\\") || strings.Contains(val, "..")) {
		return fmt.Errorf("invalid %s %q: must not contain path separators", field, val)
	}
	return nil
}

// BuildCommand constructs the kubectl rollout undo command and arguments for the given options.
func (m *RollbackManager) BuildCommand(opts RollbackOptions) (string, []string, error) {
	workload := strings.TrimSpace(opts.WorkloadName)
	if workload == "" {
		return "", nil, fmt.Errorf("workload name is required for rollback")
	}
	if err := validateRollbackToken("workload name", workload, true); err != nil {
		return "", nil, err
	}

	ns := strings.TrimSpace(opts.Namespace)
	if ns == "" {
		ns = "default"
	}
	if err := validateRollbackToken("namespace", ns, true); err != nil {
		return "", nil, err
	}

	var args []string
	if ctxName := strings.TrimSpace(opts.ContextName); ctxName != "" {
		if err := validateRollbackToken("context name", ctxName, false); err != nil {
			return "", nil, err
		}
		args = append(args, "--context", ctxName)
	}

	args = append(args, "rollout", "undo", fmt.Sprintf("deployment/%s", workload), "-n", ns)

	rev := strings.TrimSpace(opts.Revision)
	if rev != "" && !strings.EqualFold(rev, "previous") {
		if err := validateRollbackToken("revision", rev, true); err != nil {
			return "", nil, err
		}
		args = append(args, fmt.Sprintf("--to-revision=%s", rev))
	}

	return "kubectl", args, nil
}

// Execute runs (or dry-runs) the deployment rollback and persists audit records to operational memory.
func (m *RollbackManager) Execute(ctx context.Context, opts RollbackOptions) (*RollbackResult, error) {
	bin, args, err := m.BuildCommand(opts)
	if err != nil {
		return nil, err
	}

	workload := strings.TrimSpace(opts.WorkloadName)
	ns := strings.TrimSpace(opts.Namespace)
	if ns == "" {
		ns = "default"
	}
	targetRev := strings.TrimSpace(opts.Revision)
	if targetRev == "" {
		targetRev = "previous"
	}

	now := time.Now().UTC()
	changeID := fmt.Sprintf("rb-%d", now.UnixNano())
	fullCmd := append([]string{bin}, args...)

	result := &RollbackResult{
		Workload:       workload,
		Namespace:      ns,
		TargetRevision: targetRev,
		Reason:         strings.TrimSpace(opts.Reason),
		ChangeID:       changeID,
		Command:        fullCmd,
		DryRun:         opts.DryRun,
		Timestamp:      now,
	}

	var execErr error
	if opts.DryRun {
		result.Status = "dry-run"
		result.Executed = false
		result.Output = fmt.Sprintf("[dry-run] would execute: %s", strings.Join(fullCmd, " "))
	} else {
		execFn := m.execCommand
		if execFn == nil {
			execFn = exec.CommandContext
		}
		cmd := execFn(ctx, bin, args...)
		out, runErr := cmd.CombinedOutput()
		result.Output = strings.TrimSpace(string(out))
		if runErr != nil {
			result.Status = "failed"
			result.Executed = false
			execErr = fmt.Errorf("kubectl rollout undo failed for deployment/%s: %w (output: %s)", workload, runErr, result.Output)
		} else {
			result.Status = "rolled_back"
			result.Executed = true
		}
	}

	if m.Ledger != nil {
		if m.recordRollback(opts, result) == nil {
			result.RecordedInMem = true
		}
	}

	return result, execErr
}

func (m *RollbackManager) recordRollback(opts RollbackOptions, res *RollbackResult) error {
	changeRec := &opmem.ChangeRecord{
		ID:        res.ChangeID,
		Timestamp: res.Timestamp,
		Actor: opmem.ChangeActor{
			Type:       "user",
			Identifier: "skipper-rollback",
		},
		TargetWorkload: opmem.TargetWorkload{
			Cluster:   strings.TrimSpace(opts.ContextName),
			Namespace: res.Namespace,
			Name:      res.Workload,
			Kind:      "Deployment",
		},
		ChangeType: "rollback",
		Diff: map[string]interface{}{
			"targetRevision": res.TargetRevision,
			"reason":         res.Reason,
			"dryRun":         res.DryRun,
			"status":         res.Status,
			"command":        strings.Join(res.Command, " "),
		},
	}

	if err := m.Ledger.RecordChange(changeRec); err != nil {
		return err
	}

	severity := "INFO"
	switch {
	case res.Status == "failed":
		severity = "CRITICAL"
	case !res.DryRun:
		severity = "WARN"
	}

	desc := fmt.Sprintf("Rollback deployment/%s in namespace %s to revision %s (status=%s)",
		res.Workload, res.Namespace, res.TargetRevision, res.Status)
	if res.Reason != "" {
		desc += fmt.Sprintf(" reason=%q", res.Reason)
	}
	if res.DryRun {
		desc = "[dry-run] " + desc
	}

	return m.Ledger.RecordTelemetryEvent(&opmem.CausalEvent{
		Timestamp:   res.Timestamp,
		Type:        "MUTATION",
		Source:      "rollout-rollback",
		Description: desc,
		Severity:    severity,
	})
}
