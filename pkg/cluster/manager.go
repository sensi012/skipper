package cluster

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ClusterContext represents a configured Kubernetes kubeconfig context.
type ClusterContext struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	User      string `json:"user"`
	Namespace string `json:"namespace"`
	Current   bool   `json:"current"`
	Reachable bool   `json:"reachable"`
	Server    string `json:"server,omitempty"`
}

// FleetSummary summarizes multi-cluster contexts and reachability.
type FleetSummary struct {
	CurrentContext string           `json:"currentContext"`
	TotalClusters  int              `json:"totalClusters"`
	ReachableCount int              `json:"reachableCount"`
	Contexts       []ClusterContext `json:"contexts"`
}

// ContextManager manages multi-cluster kubeconfig contexts via kubectl.
type ContextManager struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewContextManager constructs a multi-cluster ContextManager.
func NewContextManager() *ContextManager {
	return &ContextManager{
		execCommand: exec.CommandContext,
	}
}

func (m *ContextManager) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if m.execCommand != nil {
		return m.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

// ListContexts parses `kubectl config get-contexts --no-headers` into structured ClusterContexts.
func (m *ContextManager) ListContexts(ctx context.Context) ([]ClusterContext, error) {
	cmd := m.command(ctx, "kubectl", "config", "get-contexts", "--no-headers")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list kubernetes contexts: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseContextsOutput(string(out)), nil
}

func parseContextsOutput(raw string) []ClusterContext {
	var contexts []ClusterContext
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		var cc ClusterContext
		offset := 0
		if fields[0] == "*" {
			cc.Current = true
			offset = 1
		}
		if len(fields) <= offset {
			continue
		}
		cc.Name = fields[offset]
		if len(fields) > offset+1 {
			cc.Cluster = fields[offset+1]
		}
		if len(fields) > offset+2 {
			cc.User = fields[offset+2]
		}
		if len(fields) > offset+3 {
			cc.Namespace = fields[offset+3]
		} else {
			cc.Namespace = "default"
		}
		contexts = append(contexts, cc)
	}
	return contexts
}

// CurrentContext returns the active kubeconfig context name.
func (m *ContextManager) CurrentContext(ctx context.Context) (string, error) {
	cmd := m.command(ctx, "kubectl", "config", "current-context")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get current context: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// UseContext switches the active kubeconfig context.
func (m *ContextManager) UseContext(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("context name is required")
	}
	cmd := m.command(ctx, "kubectl", "config", "use-context", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to switch context to %q: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

var serverURLPattern = regexp.MustCompile(`https?://[^\s]+`)

// FleetStatus returns all configured contexts along with live control-plane reachability.
func (m *ContextManager) FleetStatus(ctx context.Context) (*FleetSummary, error) {
	contexts, err := m.ListContexts(ctx)
	if err != nil {
		return nil, err
	}

	summary := &FleetSummary{
		TotalClusters: len(contexts),
		Contexts:      make([]ClusterContext, 0, len(contexts)),
	}

	for _, cc := range contexts {
		if cc.Current {
			summary.CurrentContext = cc.Name
		}
		probe := m.command(ctx, "kubectl", "--context", cc.Name, "cluster-info", "--request-timeout=3s")
		out, err := probe.CombinedOutput()
		if err == nil {
			cc.Reachable = true
			summary.ReachableCount++
			if match := serverURLPattern.FindString(string(out)); match != "" {
				cc.Server = match
			}
		}
		summary.Contexts = append(summary.Contexts, cc)
	}

	return summary, nil
}
