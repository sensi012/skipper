package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/opmem"
)

// TestRollbackHelperProcess is invoked as a subprocess to mock kubectl rollout undo.
func TestRollbackHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ROLLBACK_HELPER_PROCESS") != "1" {
		return
	}
	if out := os.Getenv("RB_MOCK_STDOUT"); out != "" {
		_, _ = fmt.Fprint(os.Stdout, out)
	}
	if errOut := os.Getenv("RB_MOCK_STDERR"); errOut != "" {
		_, _ = fmt.Fprint(os.Stderr, errOut)
	}
	if os.Getenv("RB_MOCK_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func mockRollbackExecCommand(stdout, stderr string, fail bool, captured *[]string) func(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if captured != nil {
			*captured = append([]string{name}, arg...)
		}
		cs := []string{"-test.run=TestRollbackHelperProcess", "--", name}
		cs = append(cs, arg...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		env := append(os.Environ(),
			"GO_WANT_ROLLBACK_HELPER_PROCESS=1",
			"RB_MOCK_STDOUT="+stdout,
			"RB_MOCK_STDERR="+stderr,
		)
		if fail {
			env = append(env, "RB_MOCK_EXIT=1")
		}
		cmd.Env = env
		return cmd
	}
}

func TestRollbackManager_BuildCommand(t *testing.T) {
	mgr := NewRollbackManager(nil)

	t.Run("EmptyWorkloadReturnsError", func(t *testing.T) {
		if _, _, err := mgr.BuildCommand(RollbackOptions{}); err == nil {
			t.Fatalf("expected error for empty workload name")
		}
	})

	t.Run("DefaultNamespaceAndPreviousRevision", func(t *testing.T) {
		bin, args, err := mgr.BuildCommand(RollbackOptions{
			WorkloadName: "payments-api",
			Revision:     "previous",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if bin != "kubectl" {
			t.Fatalf("expected kubectl binary, got %q", bin)
		}
		want := "rollout undo deployment/payments-api -n default"
		if got := strings.Join(args, " "); got != want {
			t.Fatalf("expected args %q, got %q", want, got)
		}
	})

	t.Run("WithContextAndSpecificRevision", func(t *testing.T) {
		bin, args, err := mgr.BuildCommand(RollbackOptions{
			WorkloadName: "orders",
			Namespace:    "prod",
			Revision:     "4",
			ContextName:  "gke-prod-us",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "kubectl --context gke-prod-us rollout undo deployment/orders -n prod --to-revision=4"
		got := strings.Join(append([]string{bin}, args...), " ")
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})
}

func TestRollbackManager_ExecuteDryRunWithLedger(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger, err := opmem.NewOperationalMemoryLedger(ledgerDir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	called := false
	mgr := NewRollbackManager(ledger)
	mgr.execCommand = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		called = true
		return exec.CommandContext(ctx, "false")
	}

	res, err := mgr.Execute(context.Background(), RollbackOptions{
		WorkloadName: "checkout",
		Namespace:    "prod",
		Revision:     "3",
		Reason:       "elevated 5xx rate",
		ContextName:  "eks-prod",
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatalf("execCommand must not be invoked during dry-run")
	}
	if !res.DryRun || res.Executed || res.Status != "dry-run" {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
	if !res.RecordedInMem {
		t.Fatalf("expected RecordedInMem=true")
	}
	if !strings.Contains(res.Output, "[dry-run]") || !strings.Contains(res.Output, "--to-revision=3") {
		t.Fatalf("unexpected dry-run output: %q", res.Output)
	}

	// Verify operational memory ChangeRecord was persisted
	rawChanges, err := os.ReadFile(filepath.Join(ledgerDir, "changes.jsonl"))
	if err != nil {
		t.Fatalf("expected changes.jsonl to exist: %v", err)
	}
	var change opmem.ChangeRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(rawChanges))), &change); err != nil {
		t.Fatalf("failed to unmarshal ChangeRecord: %v", err)
	}
	if change.ID != res.ChangeID || change.ChangeType != "rollback" || change.TargetWorkload.Name != "checkout" {
		t.Fatalf("unexpected ChangeRecord: %+v", change)
	}

	// Verify CausalEvent was persisted
	events, err := ledger.QueryTimeline("checkout", 5*time.Minute)
	if err != nil {
		t.Fatalf("QueryTimeline failed: %v", err)
	}
	if len(events) != 1 || events[0].Type != "MUTATION" || events[0].Severity != "INFO" {
		t.Fatalf("expected 1 INFO MUTATION event, got %+v", events)
	}
}

func TestRollbackManager_ExecuteLiveSuccess(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger, err := opmem.NewOperationalMemoryLedger(ledgerDir)
	if err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}

	var captured []string
	mgr := NewRollbackManager(ledger)
	mgr.execCommand = mockRollbackExecCommand("deployment.apps/checkout rolled back\n", "", false, &captured)

	res, err := mgr.Execute(context.Background(), RollbackOptions{
		WorkloadName: "checkout",
		Namespace:    "staging",
		Reason:       "bad release",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCmd := "kubectl rollout undo deployment/checkout -n staging"
	if got := strings.Join(captured, " "); got != wantCmd {
		t.Fatalf("expected command %q, got %q", wantCmd, got)
	}
	if res.Status != "rolled_back" || !res.Executed || res.DryRun {
		t.Fatalf("unexpected live result: %+v", res)
	}
	if res.TargetRevision != "previous" {
		t.Fatalf("expected TargetRevision 'previous', got %q", res.TargetRevision)
	}
	if res.Output != "deployment.apps/checkout rolled back" {
		t.Fatalf("unexpected output: %q", res.Output)
	}
	if !res.RecordedInMem {
		t.Fatalf("expected RecordedInMem=true")
	}

	events, err := ledger.QueryTimeline("checkout", 5*time.Minute)
	if err != nil {
		t.Fatalf("QueryTimeline failed: %v", err)
	}
	if len(events) != 1 || events[0].Severity != "WARN" || !strings.Contains(events[0].Description, "bad release") {
		t.Fatalf("expected 1 WARN event with reason, got %+v", events)
	}
}

func TestRollbackManager_ExecuteKubectlErrorAndNilLedger(t *testing.T) {
	t.Run("KubectlFailureRecordedAsCritical", func(t *testing.T) {
		ledger, err := opmem.NewOperationalMemoryLedger(t.TempDir())
		if err != nil {
			t.Fatalf("failed to create ledger: %v", err)
		}

		mgr := NewRollbackManager(ledger)
		mgr.execCommand = mockRollbackExecCommand("", "Error from server (NotFound): deployments.apps \"ghost\" not found\n", true, nil)

		res, err := mgr.Execute(context.Background(), RollbackOptions{
			WorkloadName: "ghost",
			Namespace:    "prod",
			Revision:     "2",
		})
		if err == nil {
			t.Fatalf("expected error on kubectl failure")
		}
		if res == nil {
			t.Fatalf("expected structured RollbackResult even on failure")
		}
		if res.Status != "failed" || res.Executed || !res.RecordedInMem {
			t.Fatalf("unexpected failure result: %+v", res)
		}
		if !strings.Contains(res.Output, "NotFound") {
			t.Fatalf("expected stderr output captured, got %q", res.Output)
		}

		events, qErr := ledger.QueryTimeline("ghost", 5*time.Minute)
		if qErr != nil || len(events) != 1 || events[0].Severity != "CRITICAL" {
			t.Fatalf("expected 1 CRITICAL event, got %+v (err=%v)", events, qErr)
		}
	})

	t.Run("NilLedgerAndEmptyWorkload", func(t *testing.T) {
		mgr := NewRollbackManager(nil)
		if _, err := mgr.Execute(context.Background(), RollbackOptions{}); err == nil {
			t.Fatalf("expected error for empty workload")
		}

		mgr.execCommand = mockRollbackExecCommand("deployment.apps/api rolled back\n", "", false, nil)
		res, err := mgr.Execute(context.Background(), RollbackOptions{WorkloadName: "api"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RecordedInMem {
			t.Fatalf("expected RecordedInMem=false when ledger is nil")
		}
	})
}
