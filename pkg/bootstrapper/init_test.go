package bootstrapper

import (
	"context"
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
