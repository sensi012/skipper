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

	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0644)

	// Flag injection in Image
	if _, err := builder.Build(context.Background(), &config.AppSpec{
		Name:  "app",
		Image: "--privileged",
		Build: config.BuildConfig{Context: dir},
	}, nil); err == nil {
		t.Fatal("expected error for flag-injection Image starting with '-'")
	}

	// Path traversal in Dockerfile
	if _, err := builder.Build(context.Background(), &config.AppSpec{
		Name: "app",
		Build: config.BuildConfig{
			Context:    dir,
			Dockerfile: "../../etc/passwd",
		},
	}, nil); err == nil {
		t.Fatal("expected error for path-traversal Dockerfile outside build context")
	}

	// Flag injection in Build.Target and Build.Args
	if _, err := builder.Build(context.Background(), &config.AppSpec{
		Name: "app",
		Build: config.BuildConfig{
			Context: dir,
			Target:  "--network=host",
		},
	}, nil); err == nil {
		t.Fatal("expected error for flag-injection Build.Target")
	}
	if _, err := builder.Build(context.Background(), &config.AppSpec{
		Name: "app",
		Build: config.BuildConfig{
			Context: dir,
			Args:    map[string]string{"--bad": "1"},
		},
	}, nil); err == nil {
		t.Fatal("expected error for flag-injection Build.Args key")
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

func TestSmartNodeDockerfileSynthesis(t *testing.T) {
	builder := NewCNBBuilder()
	det := &DetectionResult{Runtime: RuntimeNodeJS}

	t.Run("Vite SPA synthesizes node builder + nginx alpine with /app/dist", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{
			"name": "dashboard",
			"scripts": {"dev": "vite", "build": "vite build"},
			"dependencies": {"react": "^18.2.0"},
			"devDependencies": {"vite": "^5.0.0"}
		}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
			t.Fatal(err)
		}
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"FROM node:20-alpine AS builder",
			"RUN npm run build",
			"FROM nginx:alpine",
			"COPY --from=builder /app/dist /usr/share/nginx/html",
			"try_files $uri $uri/ /index.html;",
		} {
			if !strings.Contains(df, want) {
				t.Errorf("expected %q in Vite SPA Dockerfile:\n%s", want, df)
			}
		}
	})

	t.Run("CRA react-scripts SPA synthesizes nginx alpine with /app/build", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{
			"name": "portal",
			"scripts": {"start": "react-scripts start", "build": "react-scripts build"},
			"dependencies": {"react": "^18.2.0", "react-scripts": "5.0.1"}
		}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
			t.Fatal(err)
		}
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"FROM nginx:alpine",
			"COPY --from=builder /app/build /usr/share/nginx/html",
			"try_files $uri $uri/ /index.html;",
		} {
			if !strings.Contains(df, want) {
				t.Errorf("expected %q in CRA SPA Dockerfile:\n%s", want, df)
			}
		}
	})

	t.Run("Node backend uses package.json main field", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{"name": "api", "main": "./server.js"}`
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(df, `ENTRYPOINT ["node", "server.js"]`) || !strings.Contains(df, "distroless/nodejs20") {
			t.Fatalf("unexpected Node backend Dockerfile:\n%s", df)
		}
	})

	t.Run("Node TypeScript backend runs build, prunes dev deps, uses scripts.start entrypoint", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{
			"name": "ts-api",
			"main": "dist/index.js",
			"scripts": {
				"build": "tsc",
				"start": "NODE_ENV=production node --enable-source-maps dist/server.js"
			},
			"devDependencies": {"typescript": "^5.4.0"}
		}`
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"RUN npm ci || npm install",
			"RUN npm run build",
			"RUN npm prune --omit=dev || true",
			"FROM gcr.io/distroless/nodejs20-debian12:nonroot",
			`ENTRYPOINT ["node", "dist/server.js"]`,
		} {
			if !strings.Contains(df, want) {
				t.Errorf("expected %q in TypeScript backend Dockerfile:\n%s", want, df)
			}
		}
	})

	t.Run("Node backend rejects shell metacharacters in scripts.start and falls back to file probe", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{
			"name": "unsafe-api",
			"scripts": {"start": "node server.js; rm -rf /"}
		}`
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('hi')"), 0644)
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(df, "rm -rf") || !strings.Contains(df, `ENTRYPOINT ["node", "app.js"]`) {
			t.Fatalf("expected safe fallback to app.js, got:\n%s", df)
		}
	})

	t.Run("Node backend with vite devDep but explicit node start script stays backend", func(t *testing.T) {
		dir := t.TempDir()
		pkg := `{
			"name": "ssr-server",
			"scripts": {"build": "tsc", "start": "node dist/server.js"},
			"devDependencies": {"vite": "^5.0.0"}
		}`
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(df, "nginx:alpine") || !strings.Contains(df, `ENTRYPOINT ["node", "dist/server.js"]`) {
			t.Fatalf("expected distroless node backend when explicit node start exists, got:\n%s", df)
		}
	})
}

func TestSmartPythonDockerfileSynthesis(t *testing.T) {
	builder := NewCNBBuilder()
	det := &DetectionResult{Runtime: RuntimePython}

	t.Run("pyproject.toml-only with FastAPI and main.py", func(t *testing.T) {
		dir := t.TempDir()
		pyproject := `[project]
name = "orders-api"
dependencies = ["fastapi>=0.110", "uvicorn>=0.27"]
`
		_ = os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0644)
		_ = os.WriteFile(filepath.Join(dir, "main.py"), []byte("from fastapi import FastAPI\napp = FastAPI()\n"), 0644)

		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(df, "COPY requirements.txt") {
			t.Fatalf("pyproject-only project must not COPY requirements.txt:\n%s", df)
		}
		for _, want := range []string{
			"COPY pyproject.toml ./",
			"RUN pip install --no-cache-dir --prefix=/install . || true",
			`CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]`,
		} {
			if !strings.Contains(df, want) {
				t.Errorf("expected %q in FastAPI pyproject Dockerfile:\n%s", want, df)
			}
		}
	})

	t.Run("Gunicorn with requirements.txt and wsgi.py", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0.2\ngunicorn==21.2.0\n"), 0644)
		_ = os.WriteFile(filepath.Join(dir, "wsgi.py"), []byte("app = object()\n"), 0644)

		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"COPY requirements.txt ./",
			"RUN pip install --no-cache-dir --prefix=/install -r requirements.txt || true",
			`CMD ["gunicorn", "--bind", "0.0.0.0:8000", "wsgi:app"]`,
		} {
			if !strings.Contains(df, want) {
				t.Errorf("expected %q in Gunicorn Dockerfile:\n%s", want, df)
			}
		}
	})

	t.Run("Plain Python probes main.py before app.py", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests==2.31.0\n"), 0644)
		_ = os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('ok')\n"), 0644)

		df, err := builder.synthesizeDockerfileForDir(det, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(df, `CMD ["python", "main.py"]`) {
			t.Fatalf("expected main.py entrypoint, got:\n%s", df)
		}
	})
}
