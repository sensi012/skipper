package secret

import (
	"fmt"
	"strings"
	"sync"
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

	if _, err := store.Get("NONEXISTENT_KEY"); err == nil {
		t.Fatal("expected error for missing secret key")
	}

	list, err := store.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(list))
	}

	// K8s Secret Export (default and custom namespace)
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

	defaultNSManifest, err := store.ExportKubernetesManifest("app-secrets", "")
	if err != nil || !strings.Contains(defaultNSManifest, "namespace: default") {
		t.Fatalf("expected default namespace in manifest, err=%v", err)
	}
}

func TestSecretStore_ValidationAndInjectionPrevention(t *testing.T) {
	if _, err := NewSecretStore("bad\x00dir"); err == nil {
		t.Fatal("expected error for directory containing null byte")
	}

	store, err := NewSecretStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	invalidKeys := []string{
		"",
		"   ",
		"KEY=INJECT",
		"KEY\nINJECT=1",
		"-FLAG_KEY",
		"#COMMENT_KEY",
		"KEY WITH SPACE",
		"KEY\x00NULL",
	}
	for _, k := range invalidKeys {
		if err := store.Set(k, "val"); err == nil {
			t.Errorf("expected Set to reject invalid key %q", k)
		}
		if _, err := store.Get(k); err == nil {
			t.Errorf("expected Get to reject invalid key %q", k)
		}
	}

	if err := store.Set("VALID_KEY", "line1\nINJECTED_KEY=pwned"); err == nil {
		t.Fatal("expected Set to reject newline in secret value")
	}
	if err := store.Set("VALID_KEY", "null\x00byte"); err == nil {
		t.Fatal("expected Set to reject null byte in secret value")
	}

	if _, err := store.ExportKubernetesManifest("", "default"); err == nil {
		t.Fatal("expected ExportKubernetesManifest to reject empty secret name")
	}
	if _, err := store.ExportKubernetesManifest("bad\nname", "default"); err == nil {
		t.Fatal("expected ExportKubernetesManifest to reject newline in secret name")
	}
	if _, err := store.ExportKubernetesManifest("good", "bad\nns"); err == nil {
		t.Fatal("expected ExportKubernetesManifest to reject newline in namespace")
	}
}

func TestSecretStore_ConcurrentReadWrite(t *testing.T) {
	store, err := NewSecretStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	const workers = 25
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("KEY_%02d", i)
			val := fmt.Sprintf("VAL_%02d", i)
			if err := store.Set(key, val); err != nil {
				t.Errorf("concurrent Set(%s) failed: %v", key, err)
				return
			}
			got, err := store.Get(key)
			if err != nil || got != val {
				t.Errorf("concurrent Get(%s)=%q err=%v, want %q", key, got, err, val)
			}
			if _, err := store.ExportKubernetesManifest("concurrent-sec", "default"); err != nil {
				t.Errorf("concurrent ExportKubernetesManifest failed: %v", err)
			}
		}()
	}
	wg.Wait()

	all, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != workers {
		t.Fatalf("expected %d persisted secrets after concurrent writes, got %d", workers, len(all))
	}
}
