package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/unified-platform/container/pkg/config"
)

// KubernetesAdapter implements RuntimeEngine using kubectl and Kubernetes cluster API
type KubernetesAdapter struct {
	Namespace string
}

// NewKubernetesAdapter constructs a Kubernetes runtime driver
func NewKubernetesAdapter(namespace string) *KubernetesAdapter {
	if namespace == "" {
		namespace = "default"
	}
	return &KubernetesAdapter{Namespace: namespace}
}

func (k *KubernetesAdapter) Name() string {
	return "kubernetes"
}

func (k *KubernetesAdapter) Initialize(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kubectl", "version", "--client")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl client unavailable: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *KubernetesAdapter) Deploy(ctx context.Context, spec *config.AppSpec) (*WorkloadStatus, error) {
	manifest, err := k.GenerateStandardManifests(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate manifests: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-n", k.Namespace, "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl apply failed: %w: %s", err, string(out))
	}

	return k.Status(ctx, spec.Name)
}

func (k *KubernetesAdapter) Status(ctx context.Context, workloadName string) (*WorkloadStatus, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "deployment", workloadName, "-n", k.Namespace, "-o", "jsonpath={.status.replicas}|{.status.availableReplicas}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment status: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	replicas := 0
	available := 0
	if len(parts) > 0 && parts[0] != "" {
		replicas, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 && parts[1] != "" {
		available, _ = strconv.Atoi(parts[1])
	}

	state := "Degraded"
	if replicas > 0 && replicas == available {
		state = "Running"
	} else if replicas == 0 {
		state = "Stopped"
	}

	return &WorkloadStatus{
		Name:      workloadName,
		Target:    "kubernetes",
		State:     state,
		Replicas:  replicas,
		Available: available,
		CreatedAt: time.Now(),
		Labels: map[string]string{
			"runtime":   "kubernetes",
			"namespace": k.Namespace,
		},
	}, nil
}

func (k *KubernetesAdapter) Stop(ctx context.Context, workloadName string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "scale", "deployment", workloadName, "-n", k.Namespace, "--replicas=0")
	return cmd.Run()
}

func (k *KubernetesAdapter) Logs(ctx context.Context, workloadName string, opts LogOptions, out io.Writer) error {
	args := []string{"logs", fmt.Sprintf("deployment/%s", workloadName), "-n", k.Namespace}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.TailLines > 0 {
		args = append(args, fmt.Sprintf("--tail=%d", opts.TailLines))
	}
	if opts.Timestamps {
		args = append(args, "--timestamps")
	}
	if opts.Container != "" {
		args = append(args, "-c", opts.Container)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func (k *KubernetesAdapter) Debug(ctx context.Context, opts DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	// Native ephemeral container debug session
	targetPod := opts.TargetPod
	if targetPod == "" {
		// Discover first running pod for deployment
		cmd := exec.CommandContext(ctx, "kubectl", "get", "pods", "-l", fmt.Sprintf("app=%s", opts.WorkloadName), "-n", k.Namespace, "-o", "jsonpath={.items[0].metadata.name}")
		out, err := cmd.CombinedOutput()
		if err != nil || len(out) == 0 {
			return 1, fmt.Errorf("no pods found to attach debug session: %s", string(out))
		}
		targetPod = strings.TrimSpace(string(out))
	}

	image := opts.DebugImage
	if image == "" {
		image = "nicolaka/netshoot:latest"
	}

	args := []string{"debug", "-it", targetPod, "-n", k.Namespace, fmt.Sprintf("--image=%s", image), "--", "/bin/sh"}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// GenerateStandardManifests produces vanilla, un-locked-in standard K8s Deployments & Services
func (k *KubernetesAdapter) GenerateStandardManifests(spec *config.AppSpec) (string, error) {
	replicas := 1
	if spec.Runtime.Replicas.Min > 0 {
		replicas = spec.Runtime.Replicas.Min
	}

	image := spec.Image
	if image == "" {
		image = fmt.Sprintf("%s:latest", spec.Name)
	}

	var buf bytes.Buffer
	// 1. Deployment
	buf.WriteString("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n  labels:\n    app: %s\nspec:\n", spec.Name, spec.Name))
	buf.WriteString(fmt.Sprintf("  replicas: %d\n  selector:\n    matchLabels:\n      app: %s\n", replicas, spec.Name))
	buf.WriteString("  template:\n    metadata:\n      labels:\n")
	buf.WriteString(fmt.Sprintf("        app: %s\n    spec:\n", spec.Name))

	// Security context defaults
	buf.WriteString("      securityContext:\n        runAsNonRoot: true\n      containers:\n")
	buf.WriteString(fmt.Sprintf("      - name: %s\n        image: %s\n", spec.Name, image))

	if len(spec.Endpoints) > 0 {
		buf.WriteString("        ports:\n")
		for _, ep := range spec.Endpoints {
			buf.WriteString(fmt.Sprintf("        - containerPort: %d\n          name: %s\n", ep.Port, ep.Name))
		}
	}

	// Resources
	buf.WriteString("        resources:\n          requests:\n            cpu: 100m\n            memory: 128Mi\n          limits:\n            cpu: 500m\n            memory: 512Mi\n")

	// 2. Service if endpoints are defined
	if len(spec.Endpoints) > 0 {
		buf.WriteString("---\napiVersion: v1\nkind: Service\nmetadata:\n")
		buf.WriteString(fmt.Sprintf("  name: %s\nspec:\n  selector:\n    app: %s\n  ports:\n", spec.Name, spec.Name))
		for _, ep := range spec.Endpoints {
			buf.WriteString(fmt.Sprintf("  - port: %d\n    targetPort: %d\n    name: %s\n", ep.Port, ep.Port, ep.Name))
		}
	}

	return buf.String(), nil
}
