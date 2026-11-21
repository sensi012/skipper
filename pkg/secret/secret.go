package secret

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type SecretStore struct {
	mu          sync.RWMutex
	storagePath string
}

func NewSecretStore(customDir string) (*SecretStore, error) {
	if strings.ContainsAny(customDir, "\r\n\x00") {
		return nil, fmt.Errorf("invalid secret store directory: must not contain control characters")
	}
	if customDir == "" {
		home, _ := os.UserHomeDir()
		customDir = filepath.Join(home, ".skipper", "secrets")
	}
	customDir = filepath.Clean(customDir)
	if err := os.MkdirAll(customDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secret directory: %w", err)
	}
	return &SecretStore{
		storagePath: filepath.Join(customDir, "secrets.env"),
	}, nil
}

func validateSecretKey(key string) error {
	if key == "" {
		return fmt.Errorf("secret key must not be empty")
	}
	if strings.TrimSpace(key) != key || strings.HasPrefix(key, "#") || strings.HasPrefix(key, "-") {
		return fmt.Errorf("invalid secret key %q", key)
	}
	if strings.ContainsAny(key, "= \t\r\n\x00") {
		return fmt.Errorf("invalid secret key %q: must not contain '=' or whitespace/control characters", key)
	}
	return nil
}

func validateSecretValue(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid secret value: must not contain newline or null characters")
	}
	return nil
}

func (s *SecretStore) Set(key, value string) error {
	if err := validateSecretKey(key); err != nil {
		return err
	}
	if err := validateSecretValue(value); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	secrets, err := s.listUnlocked()
	if err != nil {
		return err
	}
	secrets[key] = value
	return s.saveUnlocked(secrets)
}

func (s *SecretStore) Get(key string) (string, error) {
	if err := validateSecretKey(key); err != nil {
		return "", err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	secrets, err := s.listUnlocked()
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listUnlocked()
}

func (s *SecretStore) listUnlocked() (map[string]string, error) {
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
	secretName = strings.TrimSpace(secretName)
	if secretName == "" || strings.HasPrefix(secretName, "-") || strings.ContainsAny(secretName, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid secret name %q", secretName)
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	} else if strings.HasPrefix(namespace, "-") || strings.ContainsAny(namespace, " \t\r\n\x00") {
		return "", fmt.Errorf("invalid namespace %q", namespace)
	}

	secrets, err := s.List()
	if err != nil {
		return "", err
	}

	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("apiVersion: v1\nkind: Secret\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n", secretName, namespace))

	for _, k := range keys {
		encoded := base64.StdEncoding.EncodeToString([]byte(secrets[k]))
		buf.WriteString(fmt.Sprintf("  %s: %s\n", k, encoded))
	}

	return buf.String(), nil
}

func (s *SecretStore) saveUnlocked(secrets map[string]string) error {
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.WriteString(fmt.Sprintf("%s=%s\n", k, secrets[k]))
	}
	return os.WriteFile(s.storagePath, buf.Bytes(), 0600)
}
