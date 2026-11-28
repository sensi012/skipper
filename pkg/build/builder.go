package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// BuildPlan describes the resolved build strategy, command, and optional synthesized Dockerfile
// for a single target without executing the build.
type BuildPlan struct {
	Service               string          `json:"service"`
	Image                 string          `json:"image"`
	Context               string          `json:"context"`
	Runtime               LanguageRuntime `json:"runtime"`
	Strategy              string          `json:"strategy"`
	Command               []string        `json:"command"`
	SynthesizedDockerfile string          `json:"synthesizedDockerfile,omitempty"`
}

func (b *CNBBuilder) resolveBuildPlan(ctx context.Context, spec *config.AppSpec, verbose bool, out io.Writer) (*BuildPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, fmt.Errorf("app spec must not be nil")
	}

	sourceDir := strings.TrimSpace(spec.Build.Context)
	if sourceDir == "" {
		sourceDir = "."
	} else {
		var err error
		sourceDir, err = validateBuildArg(sourceDir, "build context", false)
		if err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(spec.Build.Target) != "" {
		if _, err := validateBuildArg(spec.Build.Target, "build target", true); err != nil {
			return nil, err
		}
	}
	if err := validateBuildArgsMap(spec.Build.Args); err != nil {
		return nil, err
	}

	detection, err := b.Detect(ctx, sourceDir)
	if err != nil {
		return nil, fmt.Errorf("language detection failed: %w", err)
	}

	targetTag := strings.TrimSpace(spec.Image)
	if targetTag == "" {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, fmt.Errorf("either spec.Image or spec.Name must be specified")
		}
		targetTag = fmt.Sprintf("%s:latest", strings.TrimSpace(spec.Name))
	}
	targetTag, err = validateBuildArg(targetTag, "target image tag", true)
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("%s; found subfolder services: %s — run 'skipper build --discover', or run from the repo root with a compose file", baseMsg, strings.Join(parts, ", "))
		}
		return nil, fmt.Errorf("%s", baseMsg)
	}

	if verbose && out != nil {
		_, _ = fmt.Fprintf(out, "==> Auto-detected stack: [%s] (Files: %s)\n", detection.Runtime, strings.Join(detection.FilesFound, ", "))
	}

	// Check if pack CLI is available
	hasPack := b.execCmd(ctx, nil, nil, nil, "pack", "version") == nil

	if hasPack && detection.Runtime != RuntimeDockerfile && strings.TrimSpace(spec.Build.Dockerfile) == "" {
		builder := strings.TrimSpace(spec.Build.Builder)
		if builder == "" {
			builder = detection.Builder
		}
		builder, err = validateBuildArg(builder, "CNB builder", true)
		if err != nil {
			return nil, err
		}
		return &BuildPlan{
			Service:  spec.Name,
			Image:    targetTag,
			Context:  sourceDir,
			Runtime:  detection.Runtime,
			Strategy: "pack",
			Command:  []string{"pack", "build", targetTag, "--path", sourceDir, "--builder", builder},
		}, nil
	}

	if detection.Runtime == RuntimeDockerfile || strings.TrimSpace(spec.Build.Dockerfile) != "" {
		dockerfile := "Dockerfile"
		if strings.TrimSpace(spec.Build.Dockerfile) != "" {
			dockerfile = strings.TrimSpace(spec.Build.Dockerfile)
		}
		dfPath, err := resolveSafeDockerfilePath(sourceDir, dockerfile)
		if err != nil {
			return nil, err
		}
		buildArgs := []string{"docker", "build", "-t", targetTag, "-f", dfPath}
		if strings.TrimSpace(spec.Build.Target) != "" {
			buildArgs = append(buildArgs, "--target", strings.TrimSpace(spec.Build.Target))
		}
		buildArgs = appendBuildArgFlags(buildArgs, spec.Build.Args)
		buildArgs = append(buildArgs, sourceDir)
		return &BuildPlan{
			Service:  spec.Name,
			Image:    targetTag,
			Context:  sourceDir,
			Runtime:  detection.Runtime,
			Strategy: "dockerfile",
			Command:  buildArgs,
		}, nil
	}

	synthDockerfile, err := b.synthesizeDockerfileForDir(detection, sourceDir)
	if err != nil {
		return nil, err
	}
	buildArgs := []string{"docker", "build", "-t", targetTag, "-f", "-"}
	if strings.TrimSpace(spec.Build.Target) != "" {
		buildArgs = append(buildArgs, "--target", strings.TrimSpace(spec.Build.Target))
	}
	buildArgs = appendBuildArgFlags(buildArgs, spec.Build.Args)
	buildArgs = append(buildArgs, sourceDir)
	return &BuildPlan{
		Service:               spec.Name,
		Image:                 targetTag,
		Context:               sourceDir,
		Runtime:               detection.Runtime,
		Strategy:              "synthesized-dockerfile",
		Command:               buildArgs,
		SynthesizedDockerfile: synthDockerfile,
	}, nil
}

// Plan inspects the build context and resolves the build strategy, command, and any synthesized Dockerfile without executing a build.
func (b *CNBBuilder) Plan(ctx context.Context, spec *config.AppSpec) (*BuildPlan, error) {
	return b.resolveBuildPlan(ctx, spec, false, nil)
}

// Build compiles source code into an OCI image
func (b *CNBBuilder) Build(ctx context.Context, spec *config.AppSpec, out io.Writer) (string, error) {
	if out == nil {
		out = io.Discard
	}

	plan, err := b.resolveBuildPlan(ctx, spec, true, out)
	if err != nil {
		return "", err
	}

	switch plan.Strategy {
	case "pack":
		builder := plan.Command[len(plan.Command)-1]
		_, _ = fmt.Fprintf(out, "==> Building OCI image with Cloud Native Buildpacks (Builder: %s)...\n", builder)
		if err := b.execCmd(ctx, nil, out, out, plan.Command[0], plan.Command[1:]...); err != nil {
			return "", fmt.Errorf("pack build failed: %w", err)
		}
	case "dockerfile":
		_, _ = fmt.Fprintf(out, "==> Building OCI image via Docker BuildKit with %s...\n", plan.Command[5])
		if err := b.execCmd(ctx, nil, out, out, plan.Command[0], plan.Command[1:]...); err != nil {
			return "", fmt.Errorf("docker build failed: %w", err)
		}
	default:
		_, _ = fmt.Fprintf(out, "==> Synthesizing optimal multi-stage container build for [%s]...\n", plan.Runtime)
		if err := b.execCmd(ctx, strings.NewReader(plan.SynthesizedDockerfile), out, out, plan.Command[0], plan.Command[1:]...); err != nil {
			return "", fmt.Errorf("synthesized container build failed: %w", err)
		}
	}

	return plan.Image, nil
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

type nodeBuildPlan struct {
	IsSPA        bool
	OutputDir    string
	HasBuildStep bool
	Entrypoint   string
}

var spaFrontendPackages = []string{
	"vite",
	"react-scripts",
	"@angular/cli",
	"vue",
	"@vue/cli-service",
	"svelte",
	"parcel",
	"gatsby",
}

func sanitizeRelativeEntrypoint(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.ContainsAny(s, ";|&`$()<>\"'\\\r\n\x00 \t") {
		return ""
	}
	for strings.HasPrefix(s, "./") {
		s = strings.TrimPrefix(s, "./")
	}
	if s == "" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") {
		return ""
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == ".." || seg == "." {
			return ""
		}
	}
	return s
}

func extractNodeStartEntrypoint(script string) string {
	s := strings.TrimSpace(script)
	if s == "" || strings.ContainsAny(s, ";|&`$()<>\r\n\x00") {
		return ""
	}
	fields := strings.Fields(s)
	idx := 0
	// Skip leading environment variable assignments like NODE_ENV=production
	for idx < len(fields) && strings.Contains(fields[idx], "=") && !strings.HasPrefix(fields[idx], "-") {
		idx++
	}
	if idx >= len(fields) || fields[idx] != "node" {
		return ""
	}
	idx++
	for idx < len(fields) {
		tok := fields[idx]
		if strings.HasPrefix(tok, "-") {
			// Skip flags that take an argument (e.g. -r dotenv/config, --require ...)
			if (tok == "-r" || tok == "--require" || tok == "--import" || tok == "--loader" || tok == "--env-file") && idx+1 < len(fields) {
				idx += 2
				continue
			}
			idx++
			continue
		}
		return sanitizeRelativeEntrypoint(tok)
	}
	return ""
}

func detectNodeBuildPlan(sourceDir string) nodeBuildPlan {
	plan := nodeBuildPlan{
		Entrypoint: "index.js",
	}

	var pkg struct {
		Main            string            `json:"main"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}

	if sourceDir != "" {
		if raw, err := os.ReadFile(filepath.Join(sourceDir, "package.json")); err == nil {
			_ = json.Unmarshal(raw, &pkg)
		}
	}

	hasDep := func(name string) bool {
		if _, ok := pkg.Dependencies[name]; ok {
			return true
		}
		if _, ok := pkg.DevDependencies[name]; ok {
			return true
		}
		return false
	}

	hasBuildScript := strings.TrimSpace(pkg.Scripts["build"]) != ""
	startEntrypoint := extractNodeStartEntrypoint(pkg.Scripts["start"])

	hasSPAFramework := false
	for _, dep := range spaFrontendPackages {
		if hasDep(dep) {
			hasSPAFramework = true
			break
		}
	}

	if hasBuildScript && hasSPAFramework && startEntrypoint == "" {
		plan.IsSPA = true
		plan.HasBuildStep = true
		plan.OutputDir = "dist"
		if hasDep("react-scripts") && !hasDep("vite") {
			plan.OutputDir = "build"
		}
		return plan
	}

	plan.HasBuildStep = hasBuildScript

	if startEntrypoint != "" {
		plan.Entrypoint = startEntrypoint
		return plan
	}
	if mainEntry := sanitizeRelativeEntrypoint(pkg.Main); mainEntry != "" {
		plan.Entrypoint = mainEntry
		return plan
	}

	if sourceDir != "" {
		for _, candidate := range []string{"server.js", "app.js", "index.js"} {
			if _, err := os.Stat(filepath.Join(sourceDir, candidate)); err == nil {
				plan.Entrypoint = candidate
				return plan
			}
		}
	}

	return plan
}

type pythonBuildPlan struct {
	UsePyprojectOnly   bool
	PythonVersion      string
	Framework          string
	EntrypointFile     string
	EntrypointModule   string
	EntrypointCallable string
}

var (
	rePythonMinor   = regexp.MustCompile(`(?:^|\D)(3\.(\d+))`)
	reRequiresPyTom = regexp.MustCompile(`(?i)requires-python\s*=\s*["'][^"']*(3\.(\d+))`)
)

func parseSupportedPythonMinor(verStr, minorStr string) (string, bool) {
	minor, err := strconv.Atoi(minorStr)
	if err != nil || minor < 8 || minor > 20 {
		return "", false
	}
	return verStr, true
}

func detectPythonVersion(sourceDir string) string {
	const defaultPythonVersion = "3.12"
	if sourceDir == "" {
		return defaultPythonVersion
	}

	if pvData, err := os.ReadFile(filepath.Join(sourceDir, ".python-version")); err == nil {
		trimmed := strings.TrimSpace(string(pvData))
		if m := rePythonMinor.FindStringSubmatch(trimmed); len(m) == 3 {
			if ver, ok := parseSupportedPythonMinor(m[1], m[2]); ok {
				return ver
			}
		}
	}

	if pyprojData, err := os.ReadFile(filepath.Join(sourceDir, "pyproject.toml")); err == nil {
		if m := reRequiresPyTom.FindStringSubmatch(string(pyprojData)); len(m) == 3 {
			if ver, ok := parseSupportedPythonMinor(m[1], m[2]); ok {
				return ver
			}
		}
	}

	return defaultPythonVersion
}

func containsPythonPackage(depText, pkgName string) bool {
	for _, line := range strings.Split(strings.ToLower(depText), "\n") {
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		tokens := strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
		})
		for _, tok := range tokens {
			if tok == pkgName {
				return true
			}
		}
	}
	return false
}

func detectPythonBuildPlan(sourceDir string) pythonBuildPlan {
	plan := pythonBuildPlan{
		PythonVersion:      detectPythonVersion(sourceDir),
		EntrypointFile:     "app.py",
		EntrypointModule:   "app",
		EntrypointCallable: "app",
	}
	if sourceDir == "" {
		return plan
	}

	reqPath := filepath.Join(sourceDir, "requirements.txt")
	pyprojPath := filepath.Join(sourceDir, "pyproject.toml")

	reqData, reqErr := os.ReadFile(reqPath)
	pyprojData, pyprojErr := os.ReadFile(pyprojPath)

	hasReq := reqErr == nil
	hasPyproj := pyprojErr == nil
	plan.UsePyprojectOnly = !hasReq && hasPyproj

	combinedDeps := string(reqData) + "\n" + string(pyprojData)
	switch {
	case containsPythonPackage(combinedDeps, "fastapi") || containsPythonPackage(combinedDeps, "uvicorn"):
		plan.Framework = "uvicorn"
	case containsPythonPackage(combinedDeps, "gunicorn"):
		plan.Framework = "gunicorn"
	}

	candidates := []string{"main.py", "app.py", "server.py", "wsgi.py"}
	if plan.Framework == "gunicorn" {
		candidates = []string{"wsgi.py", "app.py", "main.py", "server.py"}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(sourceDir, candidate)); err == nil {
			plan.EntrypointFile = candidate
			plan.EntrypointModule = strings.TrimSuffix(candidate, ".py")
			return plan
		}
	}

	if plan.Framework == "gunicorn" {
		entries, err := os.ReadDir(sourceDir)
		if err == nil {
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})
			for _, ent := range entries {
				if !ent.IsDir() {
					continue
				}
				name := ent.Name()
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
					name == "venv" || name == ".venv" || name == "node_modules" {
					continue
				}
				wsgiPath := filepath.Join(sourceDir, name, "wsgi.py")
				if wsgiData, err := os.ReadFile(wsgiPath); err == nil {
					plan.EntrypointModule = name + ".wsgi"
					content := string(wsgiData)
					if strings.Contains(content, "app =") && !strings.Contains(content, "application") {
						plan.EntrypointCallable = "app"
					} else {
						plan.EntrypointCallable = "application"
					}
					return plan
				}
			}
		}
	}

	return plan
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
		plan := detectNodeBuildPlan(sourceDir)
		if plan.IsSPA {
			return fmt.Sprintf(`FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci || npm install
COPY . .
RUN npm run build

FROM nginx:alpine
RUN printf 'server {\n    listen 80;\n    server_name _;\n    root /usr/share/nginx/html;\n    index index.html;\n    location / {\n        try_files $uri $uri/ /index.html;\n    }\n}\n' > /etc/nginx/conf.d/default.conf
COPY --from=builder /app/%s /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
`, plan.OutputDir), nil
		}
		if plan.HasBuildStep {
			return fmt.Sprintf(`FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci || npm install
COPY . .
RUN npm run build
RUN npm prune --omit=dev || true

FROM gcr.io/distroless/nodejs20-debian12:nonroot
USER nonroot:nonroot
WORKDIR /app
COPY --from=builder /app /app
ENTRYPOINT ["/nodejs/bin/node", %q]
`, plan.Entrypoint), nil
		}
		return fmt.Sprintf(`FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production || npm install --production
COPY . .

FROM gcr.io/distroless/nodejs20-debian12:nonroot
USER nonroot:nonroot
WORKDIR /app
COPY --from=builder /app /app
ENTRYPOINT ["/nodejs/bin/node", %q]
`, plan.Entrypoint), nil

	case RuntimePython:
		plan := detectPythonBuildPlan(sourceDir)
		pyImage := fmt.Sprintf("python:%s-slim", plan.PythonVersion)
		builderStage := fmt.Sprintf(`FROM %s AS builder
WORKDIR /app
COPY requirements.txt ./
RUN mkdir -p /install && pip install --no-cache-dir --prefix=/install -r requirements.txt`, pyImage)
		if plan.UsePyprojectOnly {
			builderStage = fmt.Sprintf(`FROM %s AS builder
WORKDIR /app
COPY pyproject.toml ./
COPY . .
RUN mkdir -p /install && pip install --no-cache-dir --prefix=/install .`, pyImage)
		}

		var cmdLine string
		switch plan.Framework {
		case "uvicorn":
			cmdLine = fmt.Sprintf(`CMD ["uvicorn", "%s:app", "--host", "0.0.0.0", "--port", "8000"]`, plan.EntrypointModule)
		case "gunicorn":
			cmdLine = fmt.Sprintf(`CMD ["gunicorn", "--bind", "0.0.0.0:8000", "%s:%s"]`, plan.EntrypointModule, plan.EntrypointCallable)
		default:
			cmdLine = fmt.Sprintf(`CMD ["python", %q]`, plan.EntrypointFile)
		}

		return fmt.Sprintf(`%s

FROM %s
WORKDIR /app
COPY --from=builder /install /usr/local
COPY . .
RUN useradd -u 10001 appuser && chown -R appuser /app
USER appuser
%s
`, builderStage, pyImage, cmdLine), nil

	default:
		return "", fmt.Errorf("unsupported runtime '%s' without Dockerfile; please supply a Dockerfile or install pack", det.Runtime)
	}
}
