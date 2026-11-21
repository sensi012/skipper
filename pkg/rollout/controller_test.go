package rollout

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/runtime"
)

type mockRuntimeEngine struct {
	state string
}

func (m *mockRuntimeEngine) Name() string                         { return "mock" }
func (m *mockRuntimeEngine) Initialize(ctx context.Context) error { return nil }
func (m *mockRuntimeEngine) Deploy(ctx context.Context, spec *config.AppSpec) (*runtime.WorkloadStatus, error) {
	return &runtime.WorkloadStatus{Name: spec.Name, State: m.state}, nil
}
func (m *mockRuntimeEngine) Status(ctx context.Context, workloadName string) (*runtime.WorkloadStatus, error) {
	return &runtime.WorkloadStatus{Name: workloadName, State: m.state}, nil
}
func (m *mockRuntimeEngine) Stop(ctx context.Context, workloadName string) error { return nil }
func (m *mockRuntimeEngine) Logs(ctx context.Context, workloadName string, opts runtime.LogOptions, out io.Writer) error {
	return nil
}
func (m *mockRuntimeEngine) Debug(ctx context.Context, opts runtime.DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return 0, nil
}

func TestExecuteCanarySuccess(t *testing.T) {
	mock := &mockRuntimeEngine{state: "Running"}
	ctrl := NewController(mock)

	spec := &config.AppSpec{Name: "checkout"}
	cfg := RolloutConfig{
		Strategy:     StrategyCanary,
		CanarySteps:  []int{10, 50, 100},
		StepInterval: 1 * time.Millisecond,
	}

	var buf bytes.Buffer
	status, err := ctrl.ExecuteCanary(context.Background(), spec, cfg, &buf)
	if err != nil {
		t.Fatalf("unexpected canary error: %v", err)
	}

	if status.Phase != "Promoted" || status.CurrentWeight != 100 {
		t.Fatalf("expected Promoted at 100%%, got %+v", status)
	}
}

func TestExecuteCanaryAutoRollback(t *testing.T) {
	mock := &mockRuntimeEngine{state: "Degraded"}
	ctrl := NewController(mock)

	spec := &config.AppSpec{Name: "checkout"}
	cfg := RolloutConfig{
		Strategy:     StrategyCanary,
		CanarySteps:  []int{10, 50, 100},
		StepInterval: 1 * time.Millisecond,
		AutoRollback: true,
	}

	var buf bytes.Buffer
	status, err := ctrl.ExecuteCanary(context.Background(), spec, cfg, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.Phase != "RolledBack" {
		t.Fatalf("expected RolledBack phase, got %+v", status)
	}
}

func TestExecuteCanaryValidationAndCancellation(t *testing.T) {
	mock := &mockRuntimeEngine{state: "Running"}
	ctrl := NewController(mock)

	if _, err := ctrl.ExecuteCanary(context.Background(), nil, RolloutConfig{}, nil); err == nil {
		t.Error("expected error for nil spec")
	}
	if _, err := (&Controller{}).ExecuteCanary(context.Background(), &config.AppSpec{Name: "app"}, RolloutConfig{}, nil); err == nil {
		t.Error("expected error for nil runtime engine")
	}

	// Nil writer and default steps with pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ctrl.ExecuteCanary(ctx, &config.AppSpec{Name: "app"}, RolloutConfig{}, nil); err == nil {
		t.Error("expected context cancellation error")
	}

	// Context cancelled during StepInterval sleep
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	if _, err := ctrl.ExecuteCanary(ctx2, &config.AppSpec{Name: "app"}, RolloutConfig{
		CanarySteps:  []int{10, 100},
		StepInterval: 500 * time.Millisecond,
	}, nil); err == nil {
		t.Error("expected timeout error during StepInterval wait")
	}
}
