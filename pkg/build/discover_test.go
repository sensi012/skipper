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

func TestDiscoverServices(t *testing.T) {
	root := t.TempDir()

	// Root itself has no markers; subfolders do:
	mkFile := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mkFile("frontend/package.json", `{"name":"frontend"}`)
	mkFile("backend/go.mod", "module example.com/backend\ngo 1.25\n")
	mkFile("backend/sub/go.mod", "module example.com/backend/sub\ngo 1.25\n") // nested inside detected service -> excluded
	mkFile("node_modules/x/package.json", `{}`)                               // ignored dir
	mkFile(".git/x/go.mod", "module ignored\n")                               // hidden dir
	mkFile("a/b/c/go.mod", "module depth3\n")                                 // depth 3 -> excluded with maxDepth 2

	builder := NewCNBBuilder()
	svcs, err := builder.DiscoverServices(context.Background(), root, 2)
	if err != nil {
		t.Fatalf("DiscoverServices failed: %v", err)
	}

	if len(svcs) != 2 {
		t.Fatalf("expected 2 discovered services (backend, frontend), got %d: %+v", len(svcs), svcs)
	}
	if svcs[0].Name != "backend" || svcs[0].Runtime != RuntimeGo {
		t.Errorf("expected first service backend (go), got %+v", svcs[0])
	}
	if svcs[1].Name != "frontend" || svcs[1].Runtime != RuntimeNodeJS {
		t.Errorf("expected second service frontend (nodejs), got %+v", svcs[1])
	}

	// With maxDepth 3, depth-3 service should appear
	svcsDeep, err := builder.DiscoverServices(context.Background(), root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcsDeep) != 3 {
		t.Fatalf("expected 3 services at maxDepth 3, got %d: %+v", len(svcsDeep), svcsDeep)
	}
	if svcsDeep[0].Name != "a-b-c" {
		t.Errorf("expected depth-3 name 'a-b-c', got %q", svcsDeep[0].Name)
	}
}

func TestBuildAll(t *testing.T) {
	dir := t.TempDir()
	okDir := filepath.Join(dir, "api")
	failDir := filepath.Join(dir, "worker")
	_ = os.MkdirAll(okDir, 0755)
	_ = os.MkdirAll(failDir, 0755)
	_ = os.WriteFile(filepath.Join(okDir, "Dockerfile"), []byte("FROM alpine\n"), 0644)
	_ = os.WriteFile(filepath.Join(failDir, "Dockerfile"), []byte("FROM alpine\n"), 0644)

	builder := NewCNBBuilderWithRunner(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		if name == "pack" {
			return errors.New("no pack")
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "worker:latest") {
			return errors.New("broken dockerfile")
		}
		return nil
	})

	targets := []ServiceBuildTarget{
		{
			Name:       "db",
			Spec:       &config.AppSpec{Name: "db", Image: "postgres:16"},
			SkipReason: "prebuilt image postgres:16",
		},
		{
			Name: "worker",
			Spec: &config.AppSpec{Name: "worker", Image: "worker:latest", Build: config.BuildConfig{Context: failDir}},
		},
		{
			Name: "api",
			Spec: &config.AppSpec{Name: "api", Image: "api:latest", Build: config.BuildConfig{Context: okDir}},
		},
	}

	var out bytes.Buffer
	results := builder.BuildAll(context.Background(), targets, &out)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Status != "skipped" {
		t.Errorf("expected db status=skipped, got %+v", results[0])
	}
	if results[1].Status != "failed" || !strings.Contains(results[1].Error, "broken dockerfile") {
		t.Errorf("expected worker status=failed with error, got %+v", results[1])
	}
	if results[2].Status != "built" || results[2].Image != "api:latest" {
		t.Errorf("expected api status=built after failure, got %+v", results[2])
	}
	if !strings.Contains(out.String(), "==> [3/3] Building service 'api'") {
		t.Errorf("expected progress header in output, got:\n%s", out.String())
	}
}

func TestBuild_TargetAbsDockerfileAndUnknownHint(t *testing.T) {
	t.Run("passes --target and preserves absolute Dockerfile path", func(t *testing.T) {
		dir := t.TempDir()
		absDF := filepath.Join(t.TempDir(), "Dockerfile.shared")
		_ = os.WriteFile(absDF, []byte("FROM alpine AS prod\n"), 0644)

		var gotArgs []string
		builder := NewCNBBuilderWithRunner(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			gotArgs = append([]string{name}, args...)
			return nil
		})

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name: "svc",
			Build: config.BuildConfig{
				Context:    dir,
				Dockerfile: absDF,
				Target:     "prod",
			},
		}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		joined := strings.Join(gotArgs, " ")
		if !strings.Contains(joined, "-f "+absDF) {
			t.Errorf("expected absolute dockerfile path %q, got: %s", absDF, joined)
		}
		if !strings.Contains(joined, "--target prod") {
			t.Errorf("expected --target prod in build args, got: %s", joined)
		}
	})

	t.Run("passes --target on synthesized build path", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/a\n"), 0644)

		var gotArgs []string
		builder := NewCNBBuilderWithRunner(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			gotArgs = append([]string{name}, args...)
			return nil
		})

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name: "svc",
			Build: config.BuildConfig{
				Context: dir,
				Target:  "builder",
			},
		}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(strings.Join(gotArgs, " "), "--target builder") {
			t.Errorf("expected --target builder in synthesized build args, got: %v", gotArgs)
		}
	})

	t.Run("unknown runtime includes subfolder service hint", func(t *testing.T) {
		root := t.TempDir()
		_ = os.MkdirAll(filepath.Join(root, "frontend"), 0755)
		_ = os.MkdirAll(filepath.Join(root, "backend"), 0755)
		_ = os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte(`{}`), 0644)
		_ = os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module x\n"), 0644)

		builder := NewCNBBuilderWithRunner(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			t.Fatalf("should not invoke external command %s on unknown runtime", name)
			return nil
		})

		_, err := builder.Build(context.Background(), &config.AppSpec{
			Name:  "root-app",
			Build: config.BuildConfig{Context: root},
		}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for unknown runtime")
		}
		msg := err.Error()
		for _, want := range []string{"no Dockerfile or supported stack marker", "frontend (nodejs)", "backend (go)", "skipper build --discover"} {
			if !strings.Contains(msg, want) {
				t.Errorf("expected error to contain %q, got:\n%s", want, msg)
			}
		}
	})
}
