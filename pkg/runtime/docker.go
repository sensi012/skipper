package runtime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
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

func buildDockerRunArgs(spec *config.AppSpec) []string {
	args := []string{"run", "-d", "--name", spec.Name}

	for _, ep := range spec.Endpoints {
		args = append(args, "-p", fmt.Sprintf("%d:%d", ep.Port, ep.Port))
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

	// Memory limits if set
	if spec.Runtime.Resources.Memory.Limit != "" {
		args = append(args, "-m", spec.Runtime.Resources.Memory.Limit)
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
		return nil, fmt.Errorf("docker run failed: %w: %s", err, string(out))
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
