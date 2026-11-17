package secret

import (
	"strings"
	"testing"
)

func TestSecretStore(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewSecretStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}

	// Set & Get
	if err := store.Set("DATABASE_URL", "postgres://admin:secret@db:5432/app"); err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}
	if err := store.Set("STRIPE_KEY", "sk_test_123456"); err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}

	val, err := store.Get("DATABASE_URL")
	if err != nil || val != "postgres://admin:secret@db:5432/app" {
		t.Fatalf("expected postgres url, got %s (err: %v)", val, err)
	}

	list, err := store.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(list))
	}

	// K8s Secret Export
	k8sManifest, err := store.ExportKubernetesManifest("app-secrets", "prod")
	if err != nil {
		t.Fatalf("failed to export manifest: %v", err)
	}

	if !strings.Contains(k8sManifest, "kind: Secret") {
		t.Errorf("expected kind: Secret")
	}
	if !strings.Contains(k8sManifest, "namespace: prod") {
		t.Errorf("expected namespace: prod")
	}
	if !strings.Contains(k8sManifest, "STRIPE_KEY:") {
		t.Errorf("expected encoded key in manifest")
	}
}
