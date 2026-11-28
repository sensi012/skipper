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

	"github.com/sensi012/skipper/pkg/config"
)

// KubernetesAdapter implements RuntimeEngine using kubectl and Kubernetes cluster API
type KubernetesAdapter struct {
	Namespace   string
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewKubernetesAdapter constructs a Kubernetes runtime driver
func NewKubernetesAdapter(namespace string) *KubernetesAdapter {
	if namespace == "" {
		namespace = "default"
	}
	return &KubernetesAdapter{
		Namespace:   namespace,
		execCommand: exec.CommandContext,
	}
}

func (k *KubernetesAdapter) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if k.execCommand != nil {
		return k.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func (k *KubernetesAdapter) Name() string {
	return "kubernetes"
}

func (k *KubernetesAdapter) Initialize(ctx context.Context) error {
	cmd := k.command(ctx, "kubectl", "version", "--client")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl client unavailable: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *KubernetesAdapter) Deploy(ctx context.Context, spec *config.AppSpec) (*WorkloadStatus, error) {
	if err := validateOptionalSafeArg("namespace", k.Namespace); err != nil {
		return nil, err
	}
	manifest, err := k.GenerateStandardManifests(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate manifests: %w", err)
	}

	cmd := k.command(ctx, "kubectl", "apply", "-n", k.Namespace, "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl apply failed: %w: %s", err, string(out))
	}

	return k.Status(ctx, spec.Name)
}

func (k *KubernetesAdapter) Status(ctx context.Context, workloadName string) (*WorkloadStatus, error) {
	if err := validateSafeArg("workload name", workloadName); err != nil {
		return nil, err
	}
	if err := validateOptionalSafeArg("namespace", k.Namespace); err != nil {
		return nil, err
	}
	cmd := k.command(ctx, "kubectl", "get", "deployment", workloadName, "-n", k.Namespace, "-o", "jsonpath={.status.replicas}|{.status.availableReplicas}")
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
	if err := validateSafeArg("workload name", workloadName); err != nil {
		return err
	}
	if err := validateOptionalSafeArg("namespace", k.Namespace); err != nil {
		return err
	}
	cmd := k.command(ctx, "kubectl", "scale", "deployment", workloadName, "-n", k.Namespace, "--replicas=0")
	return cmd.Run()
}

func (k *KubernetesAdapter) Logs(ctx context.Context, workloadName string, opts LogOptions, out io.Writer) error {
	if err := validateSafeArg("workload name", workloadName); err != nil {
		return err
	}
	if err := validateOptionalSafeArg("namespace", k.Namespace); err != nil {
		return err
	}
	if err := validateOptionalSafeArg("container", opts.Container); err != nil {
		return err
	}
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

	cmd := k.command(ctx, "kubectl", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func (k *KubernetesAdapter) Debug(ctx context.Context, opts DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if err := validateOptionalSafeArg("namespace", k.Namespace); err != nil {
		return 1, err
	}
	if err := validateOptionalSafeArg("workload name", opts.WorkloadName); err != nil {
		return 1, err
	}
	if err := validateOptionalSafeArg("target pod", opts.TargetPod); err != nil {
		return 1, err
	}
	if err := validateOptionalSafeArg("target container", opts.TargetContainer); err != nil {
		return 1, err
	}
	if err := validateOptionalSafeArg("debug image", opts.DebugImage); err != nil {
		return 1, err
	}
	// Native ephemeral container debug session
	targetPod := opts.TargetPod
	if targetPod == "" {
		// Discover first running pod for deployment
		cmd := k.command(ctx, "kubectl", "get", "pods", "-l", fmt.Sprintf("app=%s", opts.WorkloadName), "-n", k.Namespace, "-o", "jsonpath={.items[0].metadata.name}")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return 1, fmt.Errorf("no pods found to attach debug session: %s", string(out))
		}
		targetPod = strings.TrimSpace(string(out))
	}

	image := opts.DebugImage
	if image == "" {
		image = "nicolaka/netshoot:latest"
	}

	args := []string{"debug", "-it", targetPod, "-n", k.Namespace, fmt.Sprintf("--image=%s", image)}
	if opts.TargetContainer != "" {
		args = append(args, fmt.Sprintf("--target=%s", opts.TargetContainer))
	}
	args = append(args, "--")
	if len(opts.Command) > 0 {
		args = append(args, opts.Command...)
	} else {
		args = append(args, "/bin/sh")
	}

	cmd := k.command(ctx, "kubectl", args...)
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
	if spec == nil || strings.TrimSpace(spec.Name) == "" {
		return "", fmt.Errorf("app spec with a non-empty name is required")
	}
	if err := validateSafeArg("workload name", spec.Name); err != nil {
		return "", err
	}
	if err := validateOptionalSafeArg("image", spec.Image); err != nil {
		return "", err
	}

	replicas := 1
	if spec.Runtime.Replicas.Min > 0 {
		replicas = spec.Runtime.Replicas.Min
	}

	image := spec.Image
	if image == "" {
		image = fmt.Sprintf("%s:latest", spec.Name)
	}

	cpuReq := config.NormalizeKubernetesCPU(spec.Runtime.Resources.CPU.Request)
	if cpuReq == "" {
		cpuReq = "100m"
	}
	memReq := config.NormalizeKubernetesMemory(spec.Runtime.Resources.Memory.Request)
	if memReq == "" {
		memReq = "128Mi"
	}
	cpuLim := config.NormalizeKubernetesCPU(spec.Runtime.Resources.CPU.Limit)
	if cpuLim == "" {
		cpuLim = "500m"
	}
	memLim := config.NormalizeKubernetesMemory(spec.Runtime.Resources.Memory.Limit)
	if memLim == "" {
		memLim = "512Mi"
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
	buf.WriteString(fmt.Sprintf("        resources:\n          requests:\n            cpu: %s\n            memory: %s\n          limits:\n            cpu: %s\n            memory: %s\n", cpuReq, memReq, cpuLim, memLim))

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
