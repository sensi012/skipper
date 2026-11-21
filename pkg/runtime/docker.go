package runtime

import (
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sensi012/skipper/pkg/config"
)

// DockerAdapter implements RuntimeEngine using local Docker daemon
type DockerAdapter struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewDockerAdapter constructs a Docker runtime driver
func NewDockerAdapter() *DockerAdapter {
	return &DockerAdapter{
		execCommand: exec.CommandContext,
	}
}

func (d *DockerAdapter) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if d.execCommand != nil {
		return d.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func (d *DockerAdapter) Name() string {
	return "docker"
}

func (d *DockerAdapter) Initialize(ctx context.Context) error {
	cmd := d.command(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to connect to local Docker daemon: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// normalizeDockerMemory converts Kubernetes memory quantities into Docker CLI-compatible
// memory strings. Binary units (Ki/Mi/Gi/Ti) map 1:1 to Docker's k/m/g. Decimal units
// (K/M/G) are rounded to the nearest binary k/m so local limits mirror production without
// unreadable multi-digit byte strings (e.g. 500M -> 477m, 1G -> 954m).
func normalizeDockerMemory(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	switch {
	case strings.HasSuffix(s, "Ki"):
		return strings.TrimSuffix(s, "Ki") + "k"
	case strings.HasSuffix(s, "Mi"):
		return strings.TrimSuffix(s, "Mi") + "m"
	case strings.HasSuffix(s, "Gi"):
		return strings.TrimSuffix(s, "Gi") + "g"
	case strings.HasSuffix(s, "Ti"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "Ti"), 64); err == nil {
			return strconv.FormatFloat(n*1024, 'f', -1, 64) + "g"
		}
		return strings.TrimSuffix(s, "Ti") + "g"
	case strings.HasSuffix(s, "K"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "K"), 64); err == nil && n > 0 {
			kib := int64(math.Round(n * 1000.0 / 1024.0))
			if kib < 1 {
				kib = 1
			}
			return fmt.Sprintf("%dk", kib)
		}
		return strings.TrimSuffix(s, "K") + "k"
	case strings.HasSuffix(s, "M"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "M"), 64); err == nil && n > 0 {
			mib := int64(math.Round(n * 1e6 / (1024.0 * 1024.0)))
			if mib < 1 {
				mib = 1
			}
			return fmt.Sprintf("%dm", mib)
		}
		return strings.TrimSuffix(s, "M") + "m"
	case strings.HasSuffix(s, "G"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "G"), 64); err == nil && n > 0 {
			mib := int64(math.Round(n * 1e9 / (1024.0 * 1024.0)))
			if mib < 1 {
				mib = 1
			}
			return fmt.Sprintf("%dm", mib)
		}
		return strings.TrimSuffix(s, "G") + "g"
	default:
		return s
	}
}

// normalizeDockerCPU converts Kubernetes CPU quantities (e.g. 500m, 1500m, 2)
// into Docker --cpus decimal values (e.g. 0.5, 1.5, 2).
func normalizeDockerCPU(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, "m") {
		milliStr := strings.TrimSuffix(s, "m")
		if milli, err := strconv.ParseFloat(milliStr, 64); err == nil && milli > 0 {
			return strconv.FormatFloat(milli/1000.0, 'f', -1, 64)
		}
		return ""
	}
	if cores, err := strconv.ParseFloat(s, 64); err == nil && cores > 0 {
		return strconv.FormatFloat(cores, 'f', -1, 64)
	}
	return ""
}

func buildDockerRunArgs(spec *config.AppSpec) []string {
	args := []string{"run", "-d", "--name", spec.Name}

	for _, ep := range spec.Endpoints {
		mapping := fmt.Sprintf("%d:%d", ep.Port, ep.Port)
		if strings.EqualFold(ep.Protocol, "UDP") {
			mapping += "/udp"
		}
		args = append(args, "-p", mapping)
	}

	for _, env := range spec.Env {
		if env.Value != "" {
			args = append(args, "-e", fmt.Sprintf("%s=%s", env.Name, env.Value))
		}
	}

	// Apply security defaults
	if spec.Security.RunAsNonRoot {
		args = append(args, "--security-opt=no-new-privileges:true")
	}
	if spec.Security.ReadOnlyRootFilesystem {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid")
	}

	// Memory limits if set (normalize Kubernetes Mi/Gi/Ki and decimal K/M/G units for Docker CLI)
	if mem := normalizeDockerMemory(spec.Runtime.Resources.Memory.Limit); mem != "" {
		args = append(args, "-m", mem)
	}

	// CPU limits if set (normalize Kubernetes millicores e.g. 500m -> 0.5)
	if cpus := normalizeDockerCPU(spec.Runtime.Resources.CPU.Limit); cpus != "" {
		args = append(args, "--cpus", cpus)
	}

	// Translate Kubernetes liveness probe to Docker healthcheck
	if spec.Health.Liveness != nil && strings.TrimSpace(spec.Health.Liveness.Path) != "" {
		probePort := spec.Health.Liveness.Port
		if probePort == 0 && len(spec.Endpoints) > 0 {
			probePort = spec.Endpoints[0].Port
		}
		if probePort == 0 {
			probePort = 8080
		}
		args = append(args, "--health-cmd", fmt.Sprintf("wget -qO- http://127.0.0.1:%d%s || exit 1", probePort, spec.Health.Liveness.Path))
		if spec.Health.Liveness.PeriodSeconds > 0 {
			args = append(args, "--health-interval", fmt.Sprintf("%ds", spec.Health.Liveness.PeriodSeconds))
		}
	}

	targetImage := spec.Image
	if targetImage == "" {
		targetImage = fmt.Sprintf("%s:latest", spec.Name)
	}
	args = append(args, targetImage)
	return args
}

func (d *DockerAdapter) Deploy(ctx context.Context, spec *config.AppSpec) (*WorkloadStatus, error) {
	if spec == nil || strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("app spec with a non-empty name is required")
	}

	// 1. Remove existing container if running
	_ = d.command(ctx, "docker", "rm", "-f", spec.Name).Run()

	// 2. Build docker run arguments
	args := buildDockerRunArgs(spec)

	cmd := d.command(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if spec.Image == "" && (strings.Contains(outStr, "Unable to find image") || strings.Contains(outStr, "No such image")) {
			return nil, fmt.Errorf("image '%s:latest' not found locally; run 'skipper build' first or set 'image:' in skipper.yaml (%w: %s)", spec.Name, err, strings.TrimSpace(outStr))
		}
		return nil, fmt.Errorf("docker run failed: %w: %s", err, outStr)
	}

	return d.Status(ctx, spec.Name)
}

func (d *DockerAdapter) Status(ctx context.Context, workloadName string) (*WorkloadStatus, error) {
	cmd := d.command(ctx, "docker", "inspect", workloadName, "--format", "{{.State.Status}}|{{.State.Running}}|{{.Created}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", workloadName, err)
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	state := "Stopped"
	replicas := 0
	if len(parts) > 1 && parts[1] == "true" {
		state = "Running"
		replicas = 1
	}

	createdAt := time.Now()
	if len(parts) > 2 && parts[2] != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, parts[2]); err == nil {
			createdAt = parsed
		}
	}

	return &WorkloadStatus{
		Name:      workloadName,
		Target:    "docker",
		State:     state,
		Replicas:  replicas,
		Available: replicas,
		CreatedAt: createdAt,
		Labels: map[string]string{
			"runtime": "docker",
		},
	}, nil
}

func (d *DockerAdapter) Stop(ctx context.Context, workloadName string) error {
	cmd := d.command(ctx, "docker", "stop", workloadName)
	return cmd.Run()
}

func (d *DockerAdapter) Logs(ctx context.Context, workloadName string, opts LogOptions, out io.Writer) error {
	args := []string{"logs"}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.TailLines > 0 {
		args = append(args, fmt.Sprintf("--tail=%d", opts.TailLines))
	}
	if opts.Timestamps {
		args = append(args, "-t")
	}
	args = append(args, workloadName)

	cmd := d.command(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func (d *DockerAdapter) Debug(ctx context.Context, opts DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	// Execute interactive command inside target container
	args := []string{"exec", "-it", opts.WorkloadName}
	if len(opts.Command) > 0 {
		args = append(args, opts.Command...)
	} else {
		args = append(args, "/bin/sh")
	}

	cmd := d.command(ctx, "docker", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}
