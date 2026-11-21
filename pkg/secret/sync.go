package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ExternalSecretProvider identifies the backend secret manager for External Secrets Operator.
type ExternalSecretProvider string

const (
	ProviderVault ExternalSecretProvider = "vault"
	ProviderAWS   ExternalSecretProvider = "aws"
	ProviderGCP   ExternalSecretProvider = "gcp"
)

// ExternalSecretOptions configures generation of External Secrets Operator
// SecretStore and ExternalSecret Kubernetes manifests.
type ExternalSecretOptions struct {
	Name            string                 `json:"name"`
	Namespace       string                 `json:"namespace"`
	Provider        ExternalSecretProvider `json:"provider"`
	StoreName       string                 `json:"storeName,omitempty"`
	RefreshInterval string                 `json:"refreshInterval,omitempty"`
	RemotePath      string                 `json:"remotePath"`
	VaultAddr       string                 `json:"vaultAddr,omitempty"`
	VaultMount      string                 `json:"vaultMount,omitempty"`
	VaultAuthSecret string                 `json:"vaultAuthSecret,omitempty"`
	AWSRegion       string                 `json:"awsRegion,omitempty"`
	GCPProjectID    string                 `json:"gcpProjectID,omitempty"`
}

func validateVaultPathSegment(val, field string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(val), "/")
	if trimmed == "" {
		return "", fmt.Errorf("%s must not be empty", field)
	}
	if strings.ContainsAny(trimmed, "\\?#\r\n\x00") {
		return "", fmt.Errorf("invalid %s %q: contains prohibited characters", field, val)
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid %s %q: path traversal is not allowed", field, val)
		}
	}
	return trimmed, nil
}

func sanitizeVaultError(msg, token string) string {
	clean := strings.TrimSpace(msg)
	if token != "" {
		clean = strings.ReplaceAll(clean, token, "[REDACTED]")
	}
	if len(clean) > 256 {
		clean = clean[:256] + "...(truncated)"
	}
	return clean
}

// GenerateExternalSecretManifest generates a multi-document Kubernetes YAML
// containing a SecretStore and ExternalSecret (external-secrets.io/v1beta1)
// for Vault, AWS Secrets Manager, or GCP Secret Manager.
func GenerateExternalSecretManifest(opts ExternalSecretOptions, keys []string) (string, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\r\n\x00") {
		return "", fmt.Errorf("external secret name must be a non-empty valid identifier")
	}
	remotePath, err := validateVaultPathSegment(opts.RemotePath, "remote secret path")
	if err != nil {
		return "", err
	}

	ns := strings.TrimSpace(opts.Namespace)
	if ns == "" {
		ns = "default"
	} else if strings.HasPrefix(ns, "-") || strings.ContainsAny(ns, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid namespace %q", ns)
	}
	storeName := strings.TrimSpace(opts.StoreName)
	if storeName == "" {
		storeName = name + "-store"
	} else if strings.HasPrefix(storeName, "-") || strings.ContainsAny(storeName, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid storeName %q", storeName)
	}
	refresh := strings.TrimSpace(opts.RefreshInterval)
	if refresh == "" {
		refresh = "1h"
	} else if strings.ContainsAny(refresh, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid refreshInterval %q", refresh)
	}

	for _, k := range keys {
		if err := validateSecretKey(k); err != nil {
			return "", err
		}
	}

	provider := ExternalSecretProvider(strings.ToLower(strings.TrimSpace(string(opts.Provider))))
	var providerBlock string
	switch provider {
	case ProviderVault:
		vaultAddr := strings.TrimSpace(opts.VaultAddr)
		if vaultAddr == "" {
			vaultAddr = "http://vault.vault.svc:8200"
		}
		vaultMount := strings.TrimSpace(opts.VaultMount)
		if vaultMount == "" {
			vaultMount = "secret"
		}
		authSecret := strings.TrimSpace(opts.VaultAuthSecret)
		if authSecret == "" {
			authSecret = "vault-token"
		}
		providerBlock = fmt.Sprintf(`    vault:
      server: %q
      path: %q
      version: "v2"
      auth:
        tokenSecretRef:
          name: %q
          key: "token"
`, vaultAddr, vaultMount, authSecret)

	case ProviderAWS:
		region := strings.TrimSpace(opts.AWSRegion)
		if region == "" {
			region = "us-east-1"
		}
		providerBlock = fmt.Sprintf(`    aws:
      service: SecretsManager
      region: %q
`, region)

	case ProviderGCP:
		projectID := strings.TrimSpace(opts.GCPProjectID)
		if projectID == "" {
			return "", fmt.Errorf("gcpProjectID is required for gcp provider")
		}
		providerBlock = fmt.Sprintf(`    gcpsm:
      projectID: %q
`, projectID)

	default:
		return "", fmt.Errorf("unsupported external secret provider %q (supported: vault, aws, gcp)", opts.Provider)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, `apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: %s
  namespace: %s
spec:
  provider:
%s---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: %s
  namespace: %s
spec:
  refreshInterval: %q
  secretStoreRef:
    name: %s
    kind: SecretStore
  target:
    name: %s
    creationPolicy: Owner
`, storeName, ns, providerBlock, name, ns, refresh, storeName, name)

	if len(keys) == 0 {
		fmt.Fprintf(&buf, `  dataFrom:
    - extract:
        key: %q
`, remotePath)
	} else {
		sortedKeys := make([]string, len(keys))
		copy(sortedKeys, keys)
		sort.Strings(sortedKeys)
		buf.WriteString("  data:\n")
		for _, k := range sortedKeys {
			fmt.Fprintf(&buf, `    - secretKey: %s
      remoteRef:
        key: %q
        property: %q
`, k, remotePath, k)
		}
	}

	return buf.String(), nil
}

// VaultSyncClient synchronizes secrets with a HashiCorp Vault KV v2 secrets engine
// over HTTP using only the Go standard library.
type VaultSyncClient struct {
	Address    string
	Token      string
	HTTPClient *http.Client
}

// NewVaultSyncClient constructs a Vault KV v2 client.
func NewVaultSyncClient(address, token string) (*VaultSyncClient, error) {
	addr := strings.TrimRight(strings.TrimSpace(address), "/")
	if addr == "" {
		return nil, fmt.Errorf("vault address must not be empty")
	}
	tok := strings.TrimSpace(token)
	if tok == "" {
		return nil, fmt.Errorf("vault token must not be empty")
	}
	return &VaultSyncClient{
		Address: addr,
		Token:   tok,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *VaultSyncClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *VaultSyncClient) kvV2URL(mount, secretPath string) (string, error) {
	rawMount := strings.TrimSpace(mount)
	if rawMount == "" {
		rawMount = "secret"
	}
	m, err := validateVaultPathSegment(rawMount, "vault mount")
	if err != nil {
		return "", err
	}
	p, err := validateVaultPathSegment(secretPath, "secret path")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/v1/%s/data/%s", strings.TrimRight(c.Address, "/"), m, p), nil
}

// Push writes a map of secrets to a Vault KV v2 path.
func (c *VaultSyncClient) Push(ctx context.Context, mount, secretPath string, secrets map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoint, err := c.kvV2URL(mount, secretPath)
	if err != nil {
		return err
	}

	payload := map[string]interface{}{"data": secrets}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal vault payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create vault push request: %w", err)
	}
	req.Header.Set("X-Vault-Token", c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("vault push request failed: %s", sanitizeVaultError(err.Error(), c.Token))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("vault authentication/authorization failed (HTTP %d): %s", resp.StatusCode, sanitizeVaultError(string(respBody), c.Token))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vault push failed (HTTP %d): %s", resp.StatusCode, sanitizeVaultError(string(respBody), c.Token))
	}
	return nil
}

// Pull reads a map of secrets from a Vault KV v2 path.
func (c *VaultSyncClient) Pull(ctx context.Context, mount, secretPath string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, err := c.kvV2URL(mount, secretPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault pull request: %w", err)
	}
	req.Header.Set("X-Vault-Token", c.Token)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault pull request failed: %s", sanitizeVaultError(err.Error(), c.Token))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("vault authentication/authorization failed (HTTP %d): %s", resp.StatusCode, sanitizeVaultError(string(respBody), c.Token))
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("vault secret not found at %s (HTTP 404)", secretPath)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vault pull failed (HTTP %d): %s", resp.StatusCode, sanitizeVaultError(string(respBody), c.Token))
	}

	var envelope struct {
		Data struct {
			Data map[string]interface{} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("failed to decode vault KV v2 response: %w", err)
	}

	out := make(map[string]string, len(envelope.Data.Data))
	for k, v := range envelope.Data.Data {
		if v == nil {
			out[k] = ""
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out, nil
}
