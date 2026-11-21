package secret

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateExternalSecretManifest(t *testing.T) {
	t.Run("vault provider with explicit keys", func(t *testing.T) {
		yamlOut, err := GenerateExternalSecretManifest(ExternalSecretOptions{
			Name:       "payments-secrets",
			Namespace:  "prod",
			Provider:   ProviderVault,
			RemotePath: "apps/payments",
			VaultAddr:  "https://vault.internal:8200",
			VaultMount: "kv",
		}, []string{"STRIPE_KEY", "DB_PASSWORD"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"apiVersion: external-secrets.io/v1beta1",
			"kind: SecretStore",
			"kind: ExternalSecret",
			"namespace: prod",
			`server: "https://vault.internal:8200"`,
			`path: "kv"`,
			"secretKey: DB_PASSWORD",
			"secretKey: STRIPE_KEY",
			`key: "apps/payments"`,
		} {
			if !strings.Contains(yamlOut, want) {
				t.Errorf("expected %q in manifest:\n%s", want, yamlOut)
			}
		}
		// Keys should be sorted deterministically (DB_PASSWORD before STRIPE_KEY)
		if strings.Index(yamlOut, "DB_PASSWORD") > strings.Index(yamlOut, "STRIPE_KEY") {
			t.Errorf("expected keys to be sorted alphabetically")
		}
	})

	t.Run("aws provider with dataFrom extract when keys empty", func(t *testing.T) {
		yamlOut, err := GenerateExternalSecretManifest(ExternalSecretOptions{
			Name:       "api-secrets",
			Provider:   ProviderAWS,
			RemotePath: "prod/api",
			AWSRegion:  "eu-west-1",
		}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"service: SecretsManager",
			`region: "eu-west-1"`,
			"namespace: default",
			"dataFrom:",
			`key: "prod/api"`,
		} {
			if !strings.Contains(yamlOut, want) {
				t.Errorf("expected %q in AWS manifest:\n%s", want, yamlOut)
			}
		}
	})

	t.Run("gcp provider", func(t *testing.T) {
		yamlOut, err := GenerateExternalSecretManifest(ExternalSecretOptions{
			Name:         "gcp-secrets",
			Provider:     ProviderGCP,
			RemotePath:   "app-config",
			GCPProjectID: "my-gcp-project",
		}, []string{"API_KEY"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(yamlOut, "gcpsm:") || !strings.Contains(yamlOut, `projectID: "my-gcp-project"`) {
			t.Fatalf("unexpected GCP manifest:\n%s", yamlOut)
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		cases := []ExternalSecretOptions{
			{Provider: ProviderVault, RemotePath: "a/b"},                                  // missing name
			{Name: "s", Provider: ProviderVault},                                          // missing remotePath
			{Name: "s", Provider: ProviderGCP, RemotePath: "a/b"},                         // missing gcpProjectID
			{Name: "s", Provider: ExternalSecretProvider("azure"), RemotePath: "a/b"},     // unsupported provider
		}
		for i, tc := range cases {
			if _, err := GenerateExternalSecretManifest(tc, nil); err == nil {
				t.Errorf("case %d: expected validation error, got nil", i)
			}
		}
	})
}

func TestVaultSyncClient(t *testing.T) {
	store := map[string]map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "valid-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}

		switch r.Method {
		case http.MethodPost:
			var body struct {
				Data map[string]string `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store[r.URL.Path] = body.Data
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"version":1}}`))

		case http.MethodGet:
			data, ok := store[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"errors":[]}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"data": data,
				},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	t.Run("constructor validation", func(t *testing.T) {
		if _, err := NewVaultSyncClient("", "tok"); err == nil {
			t.Fatal("expected error for empty address")
		}
		if _, err := NewVaultSyncClient(srv.URL, ""); err == nil {
			t.Fatal("expected error for empty token")
		}
	})

	t.Run("push and pull round-trip", func(t *testing.T) {
		client, err := NewVaultSyncClient(srv.URL, "valid-token")
		if err != nil {
			t.Fatalf("NewVaultSyncClient: %v", err)
		}

		want := map[string]string{
			"DB_URL":     "postgres://localhost/app",
			"JWT_SECRET": "super-secret",
		}
		if err := client.Push(context.Background(), "", "apps/orders", want); err != nil {
			t.Fatalf("Push failed: %v", err)
		}

		got, err := client.Pull(context.Background(), "secret", "apps/orders")
		if err != nil {
			t.Fatalf("Pull failed: %v", err)
		}
		if len(got) != len(want) || got["DB_URL"] != want["DB_URL"] || got["JWT_SECRET"] != want["JWT_SECRET"] {
			t.Fatalf("round-trip mismatch: got=%v want=%v", got, want)
		}
	})

	t.Run("auth errors (403)", func(t *testing.T) {
		badClient, _ := NewVaultSyncClient(srv.URL, "wrong-token")
		if err := badClient.Push(context.Background(), "secret", "apps/orders", map[string]string{"A": "1"}); err == nil || !strings.Contains(err.Error(), "authentication/authorization failed") {
			t.Fatalf("expected auth error on Push, got %v", err)
		}
		if _, err := badClient.Pull(context.Background(), "secret", "apps/orders"); err == nil || !strings.Contains(err.Error(), "authentication/authorization failed") {
			t.Fatalf("expected auth error on Pull, got %v", err)
		}
	})

	t.Run("404 not found on pull", func(t *testing.T) {
		client, _ := NewVaultSyncClient(srv.URL, "valid-token")
		if _, err := client.Pull(context.Background(), "secret", "apps/does-not-exist"); err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("expected 404 error, got %v", err)
		}
	})

	t.Run("empty path and canceled context", func(t *testing.T) {
		client, _ := NewVaultSyncClient(srv.URL, "valid-token")
		if err := client.Push(context.Background(), "secret", "", map[string]string{}); err == nil {
			t.Fatal("expected error for empty path on Push")
		}
		if _, err := client.Pull(context.Background(), "secret", ""); err == nil {
			t.Fatal("expected error for empty path on Pull")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := client.Push(ctx, "secret", "apps/x", map[string]string{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on Push, got %v", err)
		}
		if _, err := client.Pull(ctx, "secret", "apps/x"); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on Pull, got %v", err)
		}
	})
}
