package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
)

// LanguageRuntime identifies the auto-detected stack
type LanguageRuntime string

const (
	RuntimeGo         LanguageRuntime = "go"
	RuntimeNodeJS     LanguageRuntime = "nodejs"
	RuntimePython     LanguageRuntime = "python"
	RuntimeJava       LanguageRuntime = "java"
	RuntimeRust       LanguageRuntime = "rust"
	RuntimeDockerfile LanguageRuntime = "dockerfile"
	RuntimeUnknown    LanguageRuntime = "unknown"
)

// DetectionResult holds details discovered from source inspection
type DetectionResult struct {
	Runtime    LanguageRuntime
	Entrypoint string
	FilesFound []string
	Builder    string
}

// BuilderEngine defines the contract for building standard OCI container images
type BuilderEngine interface {
	Detect(ctx context.Context, sourceDir string) (*DetectionResult, error)
	Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error)
}

// buildCmdRunner executes an external build tool command.
type buildCmdRunner func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error

// CNBBuilder handles language auto-detection and OCI image builds via Cloud Native Buildpacks or BuildKit
type CNBBuilder struct {
	DefaultBuilder string
	runCmd         buildCmdRunner
}

// NewCNBBuilder creates a new CNB / BuildKit detection and build engine
func NewCNBBuilder() *CNBBuilder {
	return &CNBBuilder{
		DefaultBuilder: "paketobuildpacks/builder:base",
	}
}

func (b *CNBBuilder) execCmd(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	if b.runCmd != nil {
		return b.runCmd(ctx, stdin, stdout, stderr, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// Detect inspects the source directory to determine language, framework and required builder
func (b *CNBBuilder) Detect(ctx context.Context, sourceDir string) (*DetectionResult, error) {
	if sourceDir == "" {
		sourceDir = "."
	}

	// 1. Check for explicit Dockerfile first
	dockerfilePath := filepath.Join(sourceDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeDockerfile,
			Entrypoint: "Dockerfile",
			FilesFound: []string{"Dockerfile"},
			Builder:    "docker-buildkit",
		}, nil
	}

	// 2. Check for Go
	if _, err := os.Stat(filepath.Join(sourceDir, "go.mod")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeGo,
			Entrypoint: "main.go",
			FilesFound: []string{"go.mod"},
			Builder:    b.DefaultBuilder,
		}, nil
	}

	// 3. Check for Node.js
	if _, err := os.Stat(filepath.Join(sourceDir, "package.json")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeNodeJS,
			Entrypoint: "package.json",
			FilesFound: []string{"package.json"},
			Builder:    b.DefaultBuilder,
		}, nil
	}

	// 4. Check for Python
	if _, err := os.Stat(filepath.Join(sourceDir, "requirements.txt")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimePython,
			Entrypoint: "requirements.txt",
			FilesFound: []string{"requirements.txt"},
			Builder:    b.DefaultBuilder,
		}, nil
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "pyproject.toml")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimePython,
			Entrypoint: "pyproject.toml",
			FilesFound: []string{"pyproject.toml"},
			Builder:    b.DefaultBuilder,
		}, nil
	}

	// 5. Check for Rust
	if _, err := os.Stat(filepath.Join(sourceDir, "Cargo.toml")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeRust,
			Entrypoint: "Cargo.toml",
			FilesFound: []string{"Cargo.toml"},
			Builder:    b.DefaultBuilder,
		}, nil
	}

	// 6. Check for Java
	if _, err := os.Stat(filepath.Join(sourceDir, "pom.xml")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeJava,
			Entrypoint: "pom.xml",
			FilesFound: []string{"pom.xml"},
			Builder:    b.DefaultBuilder,
		}, nil
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "build.gradle")); err == nil {
		return &DetectionResult{
			Runtime:    RuntimeJava,
			Entrypoint: "build.gradle",
			FilesFound: []string{"build.gradle"},
			Builder:    b.DefaultBuilder,
		}, nil
	}

	return &DetectionResult{
		Runtime: RuntimeUnknown,
		Builder: b.DefaultBuilder,
	}, nil
}

// Build compiles source code into an OCI image
func (b *CNBBuilder) Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("app spec must not be nil")
	}
	if out == nil {
		out = io.Discard
	}

	sourceDir := spec.Build.Context
	if sourceDir == "" {
		sourceDir = "."
	}

	detection, err := b.Detect(ctx, sourceDir)
	if err != nil {
		return "", fmt.Errorf("language detection failed: %w", err)
	}

	targetTag := spec.Image
	if targetTag == "" {
		if strings.TrimSpace(spec.Name) == "" {
			return "", fmt.Errorf("either spec.Image or spec.Name must be specified")
		}
		targetTag = fmt.Sprintf("%s:latest", spec.Name)
	}

	_, _ = fmt.Fprintf(out, "==> Auto-detected stack: [%s] (Files: %s)\n", detection.Runtime, strings.Join(detection.FilesFound, ", "))

	// Check if pack CLI is available
	hasPack := b.execCmd(ctx, nil, nil, nil, "pack", "version") == nil

	if hasPack && detection.Runtime != RuntimeDockerfile && spec.Build.Dockerfile == "" {
		// Use native Cloud Native Buildpacks via pack CLI
		builder := spec.Build.Builder
		if builder == "" {
			builder = detection.Builder
		}
		_, _ = fmt.Fprintf(out, "==> Building OCI image with Cloud Native Buildpacks (Builder: %s)...\n", builder)
		if err := b.execCmd(ctx, nil, out, out, "pack", "build", targetTag, "--path", sourceDir, "--builder", builder); err != nil {
			return "", fmt.Errorf("pack build failed: %w", err)
		}
		return targetTag, nil
	}

	// Fallback path: If Dockerfile exists or explicit Dockerfile is configured, use Docker BuildKit
	if detection.Runtime == RuntimeDockerfile || spec.Build.Dockerfile != "" {
		dockerfile := "Dockerfile"
		if spec.Build.Dockerfile != "" {
			dockerfile = spec.Build.Dockerfile
		}
		_, _ = fmt.Fprintf(out, "==> Building OCI image via Docker BuildKit with %s...\n", dockerfile)
		if err := b.execCmd(ctx, nil, out, out, "docker", "build", "-t", targetTag, "-f", filepath.Join(sourceDir, dockerfile), sourceDir); err != nil {
			return "", fmt.Errorf("docker build failed: %w", err)
		}
		return targetTag, nil
	}

	// Synthesize automated multi-stage Dockerfile if pack is absent for zero-setup ergonomics
	_, _ = fmt.Fprintf(out, "==> Synthesizing optimal multi-stage container build for [%s]...\n", detection.Runtime)
	synthDockerfile, err := b.synthesizeDockerfile(detection)
	if err != nil {
		return "", err
	}

	if err := b.execCmd(ctx, strings.NewReader(synthDockerfile), out, out, "docker", "build", "-t", targetTag, "-f", "-", sourceDir); err != nil {
		return "", fmt.Errorf("synthesized container build failed: %w", err)
	}

	return targetTag, nil
}

func (b *CNBBuilder) synthesizeDockerfile(det *DetectionResult) (string, error) {
	switch det.Runtime {
	case RuntimeGo:
		return `FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server ./cmd/app || CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server .

FROM gcr.io/distroless/static-debian12:nonroot
USER nonroot:nonroot
WORKDIR /app
COPY --from=builder /app/server /app/server
ENTRYPOINT ["/app/server"]
`, nil

	case RuntimeNodeJS:
		return `FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production || npm install --production
COPY . .

FROM gcr.io/distroless/nodejs20-debian12:nonroot
USER nonroot:nonroot
WORKDIR /app
COPY --from=builder /app /app
ENTRYPOINT ["node", "index.js"]
`, nil

	case RuntimePython:
		return `FROM python:3.11-slim AS builder
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt || true

FROM python:3.11-slim
WORKDIR /app
COPY --from=builder /install /usr/local
COPY . .
RUN useradd -u 10001 appuser && chown -R appuser /app
USER appuser
CMD ["python", "app.py"]
`, nil

	default:
		return "", fmt.Errorf("unsupported runtime '%s' without Dockerfile; please supply a Dockerfile or install pack", det.Runtime)
	}
}
