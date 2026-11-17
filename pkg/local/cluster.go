package local

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ClusterManager defines the lifecycle interface for ephemeral local dev clusters
type ClusterManager interface {
	Name() string
	Exists(ctx context.Context, clusterName string) (bool, error)
	Create(ctx context.Context, clusterName string) error
	Delete(ctx context.Context, clusterName string) error
}

// KindManager manages local Kubernetes clusters via Kind
type KindManager struct{}

// NewKindManager constructs a Kind cluster manager
func NewKindManager() *KindManager {
	return &KindManager{}
}

func (k *KindManager) Name() string {
	return "kind"
}

// Exists checks if the cluster already exists
func (k *KindManager) Exists(ctx context.Context, clusterName string) (bool, error) {
	cmd := exec.CommandContext(ctx, "kind", "get", "clusters")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to list kind clusters: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	clusters := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, c := range clusters {
		if strings.TrimSpace(c) == clusterName {
			return true, nil
		}
	}
	return false, nil
}

// Create provisions a lightweight local development cluster with ingress ready port mappings
func (k *KindManager) Create(ctx context.Context, clusterName string) error {
	exists, err := k.Exists(ctx, clusterName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("local cluster '%s' already exists", clusterName)
	}

	// Kind cluster configuration with standard port mappings for local ingress (80, 443)
	configYaml := `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
  - containerPort: 443
    hostPort: 443
    protocol: TCP
`

	cmd := exec.CommandContext(ctx, "kind", "create", "cluster", "--name", clusterName, "--config", "-")
	cmd.Stdin = strings.NewReader(configYaml)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kind create cluster failed: %w: %s", err, outBuf.String())
	}

	return nil
}

// Delete removes the local development cluster
func (k *KindManager) Delete(ctx context.Context, clusterName string) error {
	cmd := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", clusterName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind delete cluster failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
