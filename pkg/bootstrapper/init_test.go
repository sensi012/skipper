package bootstrapper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectInitializer(t *testing.T) {
	tempDir := t.TempDir()
	// Create marker file for Node.js
	_ = os.WriteFile(filepath.Join(tempDir, "package.json"), []byte("{}"), 0644)

	initr := NewProjectInitializer()
	spec, err := initr.InspectAndGenerate(context.Background(), InitOptions{
		Name:      "cart_service",
		SourceDir: tempDir,
	})
	if err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}

	if spec.Name != "cart-service" {
		t.Fatalf("expected sanitized name cart-service, got %s", spec.Name)
	}
	if len(spec.Endpoints) != 1 || spec.Endpoints[0].Port != 3000 {
		t.Fatalf("expected default Node.js port 3000, got %+v", spec.Endpoints)
	}

	targetPath := filepath.Join(tempDir, "skipper.yaml")
	if err := initr.WriteSpecFile(spec, targetPath); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	// Verify duplicate write prevention
	if err := initr.WriteSpecFile(spec, targetPath); err == nil {
		t.Fatalf("expected error on duplicate write")
	}
}

func TestProjectInitializer_EdgeCasesAndValidation(t *testing.T) {
	initr := NewProjectInitializer()

	// Python default port 8000 and fallback name from directory
	pyDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(pyDir, "requirements.txt"), []byte("flask==3.0.0\n"), 0644)
	spec, err := initr.InspectAndGenerate(context.Background(), InitOptions{
		SourceDir: pyDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Endpoints[0].Port != 8000 {
		t.Fatalf("expected Python default port 8000, got %d", spec.Endpoints[0].Port)
	}

	// Go default port 8080 and degenerate workload name ("___" -> "my-service")
	goDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module app\n"), 0644)
	goSpec, err := initr.InspectAndGenerate(context.Background(), InitOptions{
		Name:      "___---___",
		SourceDir: goDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if goSpec.Name != "my-service" || goSpec.Endpoints[0].Port != 8080 {
		t.Fatalf("expected fallback name my-service and port 8080, got name=%q port=%d", goSpec.Name, goSpec.Endpoints[0].Port)
	}

	// Invalid port out of range
	if _, err := initr.InspectAndGenerate(context.Background(), InitOptions{Port: -1, SourceDir: goDir}); err == nil {
		t.Fatal("expected error for negative port")
	}
	if _, err := initr.InspectAndGenerate(context.Background(), InitOptions{Port: 70000, SourceDir: goDir}); err == nil {
		t.Fatal("expected error for port > 65535")
	}

	// Canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := initr.InspectAndGenerate(ctx, InitOptions{SourceDir: goDir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// WriteSpecFile validation
	if err := initr.WriteSpecFile(nil, filepath.Join(goDir, "skipper.yaml")); err == nil {
		t.Fatal("expected error when writing nil spec")
	}
	if err := initr.WriteSpecFile(goSpec, "   "); err == nil {
		t.Fatal("expected error when writing to empty targetPath")
	}
	if err := initr.WriteSpecFile(goSpec, "bad\x00path.yaml"); err == nil {
		t.Fatal("expected error when targetPath contains null byte")
	}
}
