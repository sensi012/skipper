package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// NewCNBBuilderWithRunner creates a CNBBuilder with a custom command runner for testing.
func NewCNBBuilderWithRunner(r func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error) *CNBBuilder {
	return &CNBBuilder{
		DefaultBuilder: "paketobuildpacks/builder:base",
		runCmd:         r,
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

func validateBuildArg(val, field string, disallowWhitespace bool) (string, error) {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return "", fmt.Errorf("%s must not be empty", field)
	}
	if strings.HasPrefix(trimmed, "-") {
		return "", fmt.Errorf("invalid %s %q: must not start with '-'", field, val)
	}
	if strings.ContainsAny(trimmed, "\r\n\x00") {
		return "", fmt.Errorf("invalid %s: must not contain control characters", field)
	}
	if disallowWhitespace && strings.ContainsAny(trimmed, " \t") {
		return "", fmt.Errorf("invalid %s %q: must not contain whitespace", field, val)
	}
	return trimmed, nil
}

func validateBuildArgsMap(buildArgs map[string]string) error {
	for k, v := range buildArgs {
		tk := strings.TrimSpace(k)
		if tk == "" || tk != k || strings.HasPrefix(tk, "-") || strings.ContainsAny(tk, "= \t\r\n\x00") {
			return fmt.Errorf("invalid build arg key %q", k)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("invalid build arg value for %q: must not contain control characters", k)
		}
	}
	return nil
}

func resolveSafeDockerfilePath(sourceDir, dockerfile string) (string, error) {
	df, err := validateBuildArg(dockerfile, "dockerfile path", false)
	if err != nil {
		return "", err
	}
	for _, part := range strings.FieldsFunc(df, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("invalid dockerfile path %q: must not traverse outside build context", dockerfile)
		}
	}
	if filepath.IsAbs(df) {
		return filepath.Clean(df), nil
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", fmt.Errorf("invalid build context %q: %w", sourceDir, err)
	}
	joined := filepath.Join(absSource, df)
	rel, err := filepath.Rel(absSource, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid dockerfile path %q: must not traverse outside build context", dockerfile)
	}
	return filepath.Join(sourceDir, df), nil
}

// Build compiles source code into an OCI image
func (b *CNBBuilder) Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if spec == nil {
		return "", fmt.Errorf("app spec must not be nil")
	}
	if out == nil {
		out = io.Discard
	}

	sourceDir := strings.TrimSpace(spec.Build.Context)
	if sourceDir == "" {
		sourceDir = "."
	} else {
		var err error
		sourceDir, err = validateBuildArg(sourceDir, "build context", false)
		if err != nil {
			return "", err
		}
	}

	if strings.TrimSpace(spec.Build.Target) != "" {
		if _, err := validateBuildArg(spec.Build.Target, "build target", true); err != nil {
			return "", err
		}
	}
	if err := validateBuildArgsMap(spec.Build.Args); err != nil {
		return "", err
	}

	detection, err := b.Detect(ctx, sourceDir)
	if err != nil {
		return "", fmt.Errorf("language detection failed: %w", err)
	}

	targetTag := strings.TrimSpace(spec.Image)
	if targetTag == "" {
		if strings.TrimSpace(spec.Name) == "" {
			return "", fmt.Errorf("either spec.Image or spec.Name must be specified")
		}
		targetTag = fmt.Sprintf("%s:latest", strings.TrimSpace(spec.Name))
	}
	targetTag, err = validateBuildArg(targetTag, "target image tag", true)
	if err != nil {
		return "", err
	}

	if detection.Runtime == RuntimeUnknown && strings.TrimSpace(spec.Build.Dockerfile) == "" {
		absDir, absErr := filepath.Abs(sourceDir)
		if absErr != nil {
			absDir = sourceDir
		}
		baseMsg := fmt.Sprintf("no Dockerfile or supported stack marker (go.mod, package.json, requirements.txt, pyproject.toml, Cargo.toml, pom.xml, build.gradle) found in %q", absDir)
		if subServices, discErr := b.DiscoverServices(ctx, sourceDir, 2); discErr == nil && len(subServices) > 0 {
			parts := make([]string, 0, len(subServices))
			for _, svc := range subServices {
				parts = append(parts, fmt.Sprintf("%s (%s)", svc.Name, svc.Runtime))
			}
			return "", fmt.Errorf("%s; found subfolder services: %s — run 'skipper build --discover', or run from the repo root with a compose file", baseMsg, strings.Join(parts, ", "))
		}
		return "", fmt.Errorf("%s", baseMsg)
	}

	_, _ = fmt.Fprintf(out, "==> Auto-detected stack: [%s] (Files: %s)\n", detection.Runtime, strings.Join(detection.FilesFound, ", "))

	// Check if pack CLI is available
	hasPack := b.execCmd(ctx, nil, nil, nil, "pack", "version") == nil

	if hasPack && detection.Runtime != RuntimeDockerfile && strings.TrimSpace(spec.Build.Dockerfile) == "" {
		// Use native Cloud Native Buildpacks via pack CLI
		builder := strings.TrimSpace(spec.Build.Builder)
		if builder == "" {
			builder = detection.Builder
		}
		builder, err = validateBuildArg(builder, "CNB builder", true)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(out, "==> Building OCI image with Cloud Native Buildpacks (Builder: %s)...\n", builder)
		if err := b.execCmd(ctx, nil, out, out, "pack", "build", targetTag, "--path", sourceDir, "--builder", builder); err != nil {
			return "", fmt.Errorf("pack build failed: %w", err)
		}
		return targetTag, nil
	}

	// Fallback path: If Dockerfile exists or explicit Dockerfile is configured, use Docker BuildKit
	if detection.Runtime == RuntimeDockerfile || strings.TrimSpace(spec.Build.Dockerfile) != "" {
		dockerfile := "Dockerfile"
		if strings.TrimSpace(spec.Build.Dockerfile) != "" {
			dockerfile = strings.TrimSpace(spec.Build.Dockerfile)
		}
		dfPath, err := resolveSafeDockerfilePath(sourceDir, dockerfile)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(out, "==> Building OCI image via Docker BuildKit with %s...\n", dockerfile)
		buildArgs := []string{"build", "-t", targetTag, "-f", dfPath}
		if strings.TrimSpace(spec.Build.Target) != "" {
			buildArgs = append(buildArgs, "--target", strings.TrimSpace(spec.Build.Target))
		}
		buildArgs = appendBuildArgFlags(buildArgs, spec.Build.Args)
		buildArgs = append(buildArgs, sourceDir)
		if err := b.execCmd(ctx, nil, out, out, "docker", buildArgs...); err != nil {
			return "", fmt.Errorf("docker build failed: %w", err)
		}
		return targetTag, nil
	}

	// Synthesize automated multi-stage Dockerfile if pack is absent for zero-setup ergonomics
	_, _ = fmt.Fprintf(out, "==> Synthesizing optimal multi-stage container build for [%s]...\n", detection.Runtime)
	synthDockerfile, err := b.synthesizeDockerfileForDir(detection, sourceDir)
	if err != nil {
		return "", err
	}

	buildArgs := []string{"build", "-t", targetTag, "-f", "-"}
	if strings.TrimSpace(spec.Build.Target) != "" {
		buildArgs = append(buildArgs, "--target", strings.TrimSpace(spec.Build.Target))
	}
	buildArgs = appendBuildArgFlags(buildArgs, spec.Build.Args)
	buildArgs = append(buildArgs, sourceDir)
	if err := b.execCmd(ctx, strings.NewReader(synthDockerfile), out, out, "docker", buildArgs...); err != nil {
		return "", fmt.Errorf("synthesized container build failed: %w", err)
	}

	return targetTag, nil
}

func appendBuildArgFlags(args []string, buildArgs map[string]string) []string {
	if len(buildArgs) == 0 {
		return args
	}
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, buildArgs[k]))
	}
	return args
}

func detectGoVersionFromMod(sourceDir string) string {
	const fallback = "1.25"
	if sourceDir == "" {
		return fallback
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, "go.mod"))
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "go" {
			parts := strings.Split(fields[1], ".")
			if len(parts) >= 2 {
				return parts[0] + "." + parts[1]
			}
		}
	}
	return fallback
}

func (b *CNBBuilder) synthesizeDockerfile(det *DetectionResult) (string, error) {
	return b.synthesizeDockerfileForDir(det, ".")
}

func (b *CNBBuilder) synthesizeDockerfileForDir(det *DetectionResult, sourceDir string) (string, error) {
	switch det.Runtime {
	case RuntimeGo:
		goVer := detectGoVersionFromMod(sourceDir)
		return fmt.Sprintf(`FROM golang:%s-alpine AS builder
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
`, goVer), nil

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
