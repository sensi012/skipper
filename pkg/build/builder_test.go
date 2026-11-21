package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
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
		{"Java maven", "pom.xml", RuntimeJava},
		{"Java gradle", "build.gradle", RuntimeJava},
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
			if len(res.FilesFound) != 1 || res.FilesFound[0] != tc.markerFile {
				t.Fatalf("expected FilesFound [%s], got %v", tc.markerFile, res.FilesFound)
			}
		})
	}
}

func TestDetectEmptyDirDefaultsToDot(t *testing.T) {
	builder := NewCNBBuilder()
	res, err := builder.Detect(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil detection result")
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
	if res.Builder != "docker-buildkit" {
		t.Fatalf("expected docker-buildkit builder, got %s", res.Builder)
	}
}

func TestSynthesizeDockerfile(t *testing.T) {
	builder := NewCNBBuilder()

	t.Run("Go", func(t *testing.T) {
		df, err := builder.synthesizeDockerfile(&DetectionResult{Runtime: RuntimeGo})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(df, "FROM golang:") || !strings.Contains(df, "distroless/static") || !strings.Contains(df, "USER nonroot:nonroot") {
			t.Fatalf("unexpected Go synthesized Dockerfile:\n%s", df)
		}
	})

	t.Run("NodeJS", func(t *testing.T) {
		df, err := builder.synthesizeDockerfile(&DetectionResult{Runtime: RuntimeNodeJS})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(df, "FROM node:") || !strings.Contains(df, "distroless/nodejs") || !strings.Contains(df, "USER nonroot:nonroot") {
			t.Fatalf("unexpected NodeJS synthesized Dockerfile:\n%s", df)
		}
	})

	t.Run("Python", func(t *testing.T) {
		df, err := builder.synthesizeDockerfile(&DetectionResult{Runtime: RuntimePython})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(df, "FROM python:") || !strings.Contains(df, "USER appuser") {
			t.Fatalf("unexpected Python synthesized Dockerfile:\n%s", df)
		}
	})

	for _, rt := range []LanguageRuntime{RuntimeRust, RuntimeJava, RuntimeUnknown} {
		t.Run("unsupported_"+string(rt), func(t *testing.T) {
			if _, err := builder.synthesizeDockerfile(&DetectionResult{Runtime: rt}); err == nil {
				t.Fatalf("expected error for unsupported runtime %s", rt)
			}
		})
	}
}

func TestBuild_ValidationErrors(t *testing.T) {
	builder := NewCNBBuilder()

	if _, err := builder.Build(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil spec")
	}

	dir := t.TempDir()
	spec := &config.AppSpec{
		Build: config.BuildConfig{Context: dir},
	}
	if _, err := builder.Build(context.Background(), spec, nil); err == nil {
		t.Fatal("expected error when both Image and Name are empty")
	}
}

func TestBuild_PackCNBPath(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app"), 0644)

	t.Run("pack build succeeds with default builder and derived tag", func(t *testing.T) {
		var recorded [][]string
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			recorded = append(recorded, append([]string{name}, args...))
			return nil
		}

		var out bytes.Buffer
		tag, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "orders-api",
			Build: config.BuildConfig{Context: dir},
		}, &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "orders-api:latest" {
			t.Fatalf("expected orders-api:latest, got %s", tag)
		}
		if len(recorded) != 2 || recorded[1][0] != "pack" || recorded[1][1] != "build" || recorded[1][2] != "orders-api:latest" {
			t.Fatalf("unexpected pack commands: %v", recorded)
		}
		if recorded[1][6] != builder.DefaultBuilder {
			t.Fatalf("expected default builder %s, got %s", builder.DefaultBuilder, recorded[1][6])
		}
	})

	t.Run("pack build uses custom builder and returns wrapped error on failure", func(t *testing.T) {
		var usedBuilder string
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" && len(args) > 0 && args[0] == "version" {
				return nil
			}
			for i := range args {
				if args[i] == "--builder" && i+1 < len(args) {
					usedBuilder = args[i+1]
				}
			}
			return errors.New("exit status 1")
		}

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "orders-api",
			Image: "ghcr.io/org/orders:v2",
			Build: config.BuildConfig{
				Context: dir,
				Builder: "gcr.io/buildpacks/builder:v1",
			},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "pack build failed") {
			t.Fatalf("expected wrapped pack build error, got %v", err)
		}
		if usedBuilder != "gcr.io/buildpacks/builder:v1" {
			t.Fatalf("expected custom builder, got %s", usedBuilder)
		}
	})

	t.Run("explicit spec.Build.Dockerfile overrides pack even when pack is installed", func(t *testing.T) {
		var buildCmd []string
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" && len(args) > 0 && args[0] == "version" {
				return nil
			}
			buildCmd = append([]string{name}, args...)
			return nil
		}

		tag, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "orders-api",
			Image: "orders:custom",
			Build: config.BuildConfig{
				Context:    dir,
				Dockerfile: "Dockerfile.prod",
			},
		}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "orders:custom" {
			t.Fatalf("expected orders:custom, got %s", tag)
		}
		if len(buildCmd) == 0 || buildCmd[0] != "docker" || buildCmd[1] != "build" {
			t.Fatalf("expected docker build when explicit Dockerfile set, got %v", buildCmd)
		}
		wantDockerfile := filepath.Join(dir, "Dockerfile.prod")
		if buildCmd[5] != wantDockerfile {
			t.Fatalf("expected dockerfile arg %s, got %s", wantDockerfile, buildCmd[5])
		}
	})
}

func TestBuild_DockerfileFallbackPath(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0644)

	t.Run("docker build succeeds", func(t *testing.T) {
		var buildCmd []string
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("pack not found")
			}
			buildCmd = append([]string{name}, args...)
			return nil
		}

		var out bytes.Buffer
		tag, err := builder.Build(context.Background(), &config.AppSpec{
			Name: "web",
			Build: config.BuildConfig{
				Context: dir,
				Args:    map[string]string{"VERSION": "1.2.3", "ENV": "prod"},
			},
		}, &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "web:latest" {
			t.Fatalf("expected web:latest, got %s", tag)
		}
		if len(buildCmd) < 10 || buildCmd[0] != "docker" || buildCmd[1] != "build" || buildCmd[5] != filepath.Join(dir, "Dockerfile") {
			t.Fatalf("unexpected docker build command: %v", buildCmd)
		}
		joined := strings.Join(buildCmd, " ")
		if !strings.Contains(joined, "--build-arg ENV=prod --build-arg VERSION=1.2.3") {
			t.Fatalf("expected sorted --build-arg flags in docker build command, got: %s", joined)
		}
	})

	t.Run("docker build failure wrapped", func(t *testing.T) {
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("pack not found")
			}
			return errors.New("daemon unavailable")
		}

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "web",
			Build: config.BuildConfig{Context: dir},
		}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "docker build failed") {
			t.Fatalf("expected wrapped docker build error, got %v", err)
		}
	})
}

func TestBuild_SynthesizedDockerfileFallback(t *testing.T) {
	t.Run("synthesizes and pipes Go Dockerfile matching go.mod version when pack absent", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc\n\ngo 1.23.4\n"), 0644)

		var stdinContent string
		var buildCmd []string
		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("pack not found")
			}
			buildCmd = append([]string{name}, args...)
			if stdin != nil {
				b, _ := io.ReadAll(stdin)
				stdinContent = string(b)
			}
			return nil
		}

		var out bytes.Buffer
		tag, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "go-svc",
			Build: config.BuildConfig{Context: dir},
		}, &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "go-svc:latest" {
			t.Fatalf("expected go-svc:latest, got %s", tag)
		}
		if len(buildCmd) < 6 || buildCmd[0] != "docker" || buildCmd[4] != "-f" || buildCmd[5] != "-" {
			t.Fatalf("expected docker build -f -, got %v", buildCmd)
		}
		if !strings.Contains(stdinContent, "FROM golang:1.23-alpine AS builder") {
			t.Fatalf("expected synthesized Go 1.23 Dockerfile on stdin, got:\n%s", stdinContent)
		}
	})

	t.Run("synthesized build failure wrapped", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"web"}`), 0644)

		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("pack not found")
			}
			return errors.New("buildkit failed")
		}

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "node-svc",
			Build: config.BuildConfig{Context: dir},
		}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "synthesized container build failed") {
			t.Fatalf("expected synthesized build error, got %v", err)
		}
	})

	t.Run("unsupported runtime without pack or Dockerfile returns error", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname='x'"), 0644)

		builder := NewCNBBuilder()
		builder.runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			return errors.New("pack not found")
		}

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "rust-svc",
			Build: config.BuildConfig{Context: dir},
		}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unsupported runtime") {
			t.Fatalf("expected unsupported runtime error, got %v", err)
		}
	})
}
