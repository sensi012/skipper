package dev

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/runtime"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

type mockDevRuntime struct {
	deployCount atomic.Int32
	stopCount   atomic.Int32
	deployErr   func() error
}

func (m *mockDevRuntime) Name() string                         { return "mock" }
func (m *mockDevRuntime) Initialize(ctx context.Context) error { return nil }
func (m *mockDevRuntime) Deploy(ctx context.Context, spec *config.AppSpec) (*runtime.WorkloadStatus, error) {
	m.deployCount.Add(1)
	if m.deployErr != nil {
		if err := m.deployErr(); err != nil {
			return nil, err
		}
	}
	return &runtime.WorkloadStatus{Name: spec.Name, State: "Running"}, nil
}
func (m *mockDevRuntime) Status(ctx context.Context, workloadName string) (*runtime.WorkloadStatus, error) {
	return &runtime.WorkloadStatus{Name: workloadName, State: "Running"}, nil
}
func (m *mockDevRuntime) Stop(ctx context.Context, workloadName string) error {
	m.stopCount.Add(1)
	return nil
}
func (m *mockDevRuntime) Logs(ctx context.Context, workloadName string, opts runtime.LogOptions, out io.Writer) error {
	return nil
}
func (m *mockDevRuntime) Debug(ctx context.Context, opts runtime.DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return 0, nil
}

type mockDevBuilder struct {
	buildCount atomic.Int32
	buildErr   func() error
}

func (b *mockDevBuilder) Detect(ctx context.Context, dir string) (*build.DetectionResult, error) {
	return &build.DetectionResult{Runtime: build.RuntimeGo}, nil
}
func (b *mockDevBuilder) Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error) {
	b.buildCount.Add(1)
	if b.buildErr != nil {
		if err := b.buildErr(); err != nil {
			return "", err
		}
	}
	return "test-image:latest", nil
}

func TestNewDevServerDefaults(t *testing.T) {
	s := NewDevServer(DevOptions{})
	if s.opts.WatchDir != "." {
		t.Fatalf("expected default WatchDir '.', got %q", s.opts.WatchDir)
	}
	if s.opts.Debounce != 400*time.Millisecond {
		t.Fatalf("expected default Debounce 400ms, got %v", s.opts.Debounce)
	}
}

func TestShouldIgnorePath(t *testing.T) {
	tests := []struct {
		path   string
		ignore bool
	}{
		{".git", true},
		{".git/HEAD", true},
		{".skipper/state.json", true},
		{".env", true},
		{"node_modules/express", true},
		{"vendor/mod/file.go", true},
		{"dist/bundle.js", true},
		{"bin/skipper.exe", true},
		{"__pycache__/mod.pyc", true},
		{".vscode/settings.json", true},
		{".", false},
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
	rt := &mockDevRuntime{}
	builder := &mockDevBuilder{}
	server := NewDevServer(DevOptions{
		WatchDir:   dir,
		Debounce:   10 * time.Millisecond,
		Spec:       &config.AppSpec{Name: "test-app"},
		RuntimeEng: rt,
		Builder:    builder,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- server.Start(ctx, nil)
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error during shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("dev server failed to shut down within timeout")
	}

	if rt.deployCount.Load() != 1 {
		t.Fatalf("expected 1 initial deploy, got %d", rt.deployCount.Load())
	}
	if rt.stopCount.Load() != 1 {
		t.Fatalf("expected Stop to be called once on shutdown, got %d", rt.stopCount.Load())
	}
}

func TestDevServerInvalidWatchDir(t *testing.T) {
	server := NewDevServer(DevOptions{
		WatchDir:   filepath.Join(t.TempDir(), "does-not-exist"),
		Debounce:   10 * time.Millisecond,
		Spec:       &config.AppSpec{Name: "test-app"},
		RuntimeEng: &mockDevRuntime{},
		Builder:    &mockDevBuilder{},
	})

	err := server.Start(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "failed to watch directory") {
		t.Fatalf("expected watch directory error, got %v", err)
	}
}

func TestDevServerDebounceReloadAndSubdirWatch(t *testing.T) {
	dir := t.TempDir()
	// Pre-create an ignored directory to exercise SkipDir in addWatchRecursive
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}

	rt := &mockDevRuntime{}
	builder := &mockDevBuilder{}
	var out syncBuffer

	server := NewDevServer(DevOptions{
		WatchDir:   dir,
		Debounce:   200 * time.Millisecond,
		Spec:       &config.AppSpec{Name: "reload-app"},
		RuntimeEng: rt,
		Builder:    builder,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Start(ctx, &out)
	}()

	// Wait for initial build+deploy
	waitFor(t, 2*time.Second, func() bool { return rt.deployCount.Load() >= 1 })

	// 1. Write inside ignored directory -> should NOT trigger rebuild
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "ignored.js"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}
	time.Sleep(280 * time.Millisecond)
	if rt.deployCount.Load() != 1 {
		t.Fatalf("expected ignored file not to trigger rebuild, deploys=%d", rt.deployCount.Load())
	}

	// 2. Rapid burst of writes to a tracked file -> debounced into a single rebuild
	target := filepath.Join(dir, "main.go")
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(target, []byte("package main // "+string(rune('a'+i))), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	waitFor(t, 3*time.Second, func() bool { return rt.deployCount.Load() >= 2 })
	time.Sleep(250 * time.Millisecond)
	if got := rt.deployCount.Load(); got != 2 {
		t.Fatalf("expected rapid burst to coalesce into 1 reload (total 2 deploys), got %d", got)
	}

	// 3. Dynamically create a subdirectory and write a file inside it
	subDir := filepath.Join(dir, "handlers")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir handlers: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return rt.deployCount.Load() >= 3 })

	if err := os.WriteFile(filepath.Join(subDir, "user.go"), []byte("package handlers"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return rt.deployCount.Load() >= 4 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("dev server did not exit")
	}

	if !strings.Contains(out.String(), "Change Detected:") {
		t.Errorf("expected Change Detected log, got:\n%s", out.String())
	}
}

func TestDevServerBuildAndDeployErrorPaths(t *testing.T) {
	dir := t.TempDir()
	var failBuild atomic.Bool
	failBuild.Store(true)

	rt := &mockDevRuntime{
		deployErr: func() error { return errors.New("deploy boom") },
	}
	builder := &mockDevBuilder{
		buildErr: func() error {
			if failBuild.Load() {
				return errors.New("compile error")
			}
			return nil
		},
	}

	var out syncBuffer
	server := NewDevServer(DevOptions{
		WatchDir:   dir,
		Debounce:   30 * time.Millisecond,
		Spec:       &config.AppSpec{Name: "err-app"},
		RuntimeEng: rt,
		Builder:    builder,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Start(ctx, &out)
	}()

	// Initial run fails at Build
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "Initial run failed: compile error")
	})

	// Next reload passes Build but fails at Deploy
	failBuild.Store(false)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write app.go: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "Rebuild failed: deploy boom")
	})

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Since deploy never succeeded, d.status remained nil and Stop should not be called.
	if rt.stopCount.Load() != 0 {
		t.Fatalf("expected Stop not to be called when no workload ever started, got %d", rt.stopCount.Load())
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
