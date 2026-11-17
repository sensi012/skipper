package dev

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/unified-platform/container/pkg/build"
	"github.com/unified-platform/container/pkg/config"
	"github.com/unified-platform/container/pkg/runtime"
)

type mockDevRuntime struct{}

func (m *mockDevRuntime) Name() string { return "mock" }
func (m *mockDevRuntime) Initialize(ctx context.Context) error { return nil }
func (m *mockDevRuntime) Deploy(ctx context.Context, spec *config.AppSpec) (*runtime.WorkloadStatus, error) {
	return &runtime.WorkloadStatus{Name: spec.Name, State: "Running"}, nil
}
func (m *mockDevRuntime) Status(ctx context.Context, workloadName string) (*runtime.WorkloadStatus, error) {
	return &runtime.WorkloadStatus{Name: workloadName, State: "Running"}, nil
}
func (m *mockDevRuntime) Stop(ctx context.Context, workloadName string) error { return nil }
func (m *mockDevRuntime) Logs(ctx context.Context, workloadName string, opts runtime.LogOptions, out io.Writer) error {
	return nil
}
func (m *mockDevRuntime) Debug(ctx context.Context, opts runtime.DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return 0, nil
}

type mockDevBuilder struct{}

func (b *mockDevBuilder) Detect(ctx context.Context, dir string) (*build.DetectionResult, error) {
	return &build.DetectionResult{Runtime: build.RuntimeGo}, nil
}
func (b *mockDevBuilder) Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error) {
	return "test-image:latest", nil
}

func TestShouldIgnorePath(t *testing.T) {
	tests := []struct {
		path   string
		ignore bool
	}{
		{".git", true},
		{"node_modules/express", true},
		{"bin/skipper.exe", true},
		{"pkg/config/spec.go", false},
		{"main.go", false},
	}

	for _, tc := range tests {
		res := shouldIgnorePath(tc.path)
		if res != tc.ignore {
			t.Errorf("for path %s expected ignore=%v, got %v", tc.path, tc.ignore, res)
		}
	}
}

func TestDevServerGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	server := NewDevServer(DevOptions{
		WatchDir:   dir,
		Debounce:   10 * time.Millisecond,
		Spec:       &config.AppSpec{Name: "test-app"},
		RuntimeEng: &mockDevRuntime{},
		Builder:    &mockDevBuilder{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)

	go func() {
		done <- server.Start(ctx, io.Discard)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel() // Trigger shutdown

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error during shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("dev server failed to shut down within timeout")
	}
}
