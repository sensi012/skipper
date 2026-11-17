package secret

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SecretStore struct {
	storagePath string
}

func NewSecretStore(customDir string) (*SecretStore, error) {
	if customDir == "" {
		home, _ := os.UserHomeDir()
		customDir = filepath.Join(home, ".skipper", "secrets")
	}
	if err := os.MkdirAll(customDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secret directory: %w", err)
	}
	return &SecretStore{
		storagePath: filepath.Join(customDir, "secrets.env"),
	}, nil
}

func (s *SecretStore) Set(key, value string) error {
	secrets, _ := s.List()
	secrets[key] = value
	return s.save(secrets)
}

func (s *SecretStore) Get(key string) (string, error) {
	secrets, err := s.List()
	if err != nil {
		return "", err
	}
	val, ok := secrets[key]
	if !ok {
		return "", fmt.Errorf("secret '%s' not found", key)
	}
	return val, nil
}

func (s *SecretStore) List() (map[string]string, error) {
	result := make(map[string]string)
	if _, err := os.Stat(s.storagePath); os.IsNotExist(err) {
		return result, nil
	}

	data, err := os.ReadFile(s.storagePath)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result, nil
}

func (s *SecretStore) ExportKubernetesManifest(secretName, namespace string) (string, error) {
	secrets, err := s.List()
	if err != nil {
		return "", err
	}
	if namespace == "" {
		namespace = "default"
	}

	var buf bytes.Buffer
	buf.WriteString("apiVersion: v1\nkind: Secret\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n", secretName, namespace))

	for k, v := range secrets {
		encoded := base64.StdEncoding.EncodeToString([]byte(v))
		buf.WriteString(fmt.Sprintf("  %s: %s\n", k, encoded))
	}

	return buf.String(), nil
}

func (s *SecretStore) save(secrets map[string]string) error {
	var buf bytes.Buffer
	for k, v := range secrets {
		buf.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}
	return os.WriteFile(s.storagePath, buf.Bytes(), 0600)
}
