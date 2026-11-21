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
type KindManager struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewKindManager constructs a Kind cluster manager
func NewKindManager() *KindManager {
	return &KindManager{
		execCommand: exec.CommandContext,
	}
}

func (k *KindManager) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if k.execCommand != nil {
		return k.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func (k *KindManager) Name() string {
	return "kind"
}

// Exists checks if the cluster already exists
func (k *KindManager) Exists(ctx context.Context, clusterName string) (bool, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return false, nil
	}

	cmd := k.command(ctx, "kind", "get", "clusters")
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

func buildKindConfig() string {
	return `kind: Cluster
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
}

// Create provisions a lightweight local development cluster with ingress ready port mappings
func (k *KindManager) Create(ctx context.Context, clusterName string) error {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return fmt.Errorf("cluster name cannot be empty")
	}

	exists, err := k.Exists(ctx, clusterName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("local cluster '%s' already exists", clusterName)
	}

	// Kind cluster configuration with standard port mappings for local ingress (80, 443)
	configYaml := buildKindConfig()

	cmd := k.command(ctx, "kind", "create", "cluster", "--name", clusterName, "--config", "-")
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
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return fmt.Errorf("cluster name cannot be empty")
	}

	cmd := k.command(ctx, "kind", "delete", "cluster", "--name", clusterName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind delete cluster failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
