package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRuntimes(t *testing.T) {
	tests := []struct {
		name        string
		markerFile  string
		expectedRun LanguageRuntime
	}{
		{"Dockerfile", "Dockerfile", RuntimeDockerfile},
		{"Go", "go.mod", RuntimeGo},
		{"NodeJS", "package.json", RuntimeNodeJS},
		{"Python requirements", "requirements.txt", RuntimePython},
		{"Python pyproject", "pyproject.toml", RuntimePython},
		{"Rust", "Cargo.toml", RuntimeRust},
	}

	builder := NewCNBBuilder()
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			markerPath := filepath.Join(dir, tc.markerFile)
			if err := os.WriteFile(markerPath, []byte("content"), 0644); err != nil {
				t.Fatalf("failed to create marker file: %v", err)
			}

			res, err := builder.Detect(ctx, dir)
			if err != nil {
				t.Fatalf("unexpected detect error: %v", err)
			}
			if res.Runtime != tc.expectedRun {
				t.Fatalf("expected runtime %s, got %s", tc.expectedRun, res.Runtime)
			}
		})
	}
}

func TestDetectUnknown(t *testing.T) {
	builder := NewCNBBuilder()
	dir := t.TempDir()
	res, err := builder.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Runtime != RuntimeUnknown {
		t.Fatalf("expected RuntimeUnknown, got %s", res.Runtime)
	}
}

func TestDetectPriorityDockerfile(t *testing.T) {
	builder := NewCNBBuilder()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	res, err := builder.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Runtime != RuntimeDockerfile {
		t.Fatalf("expected Dockerfile priority, got %s", res.Runtime)
	}
}
