package tunnel

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type PortForwardOptions struct {
	Namespace    string
	WorkloadName string
	LocalPort    int
	RemotePort   int
}

type PortForwarder struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

func NewPortForwarder() *PortForwarder {
	return &PortForwarder{
		execCommand: exec.CommandContext,
	}
}

func (p *PortForwarder) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if p.execCommand != nil {
		return p.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func (p *PortForwarder) Forward(ctx context.Context, opts PortForwardOptions, out, errOut io.Writer) error {
	if strings.TrimSpace(opts.WorkloadName) == "" {
		return fmt.Errorf("workload name is required for port-forward")
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}

	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}

	target := fmt.Sprintf("deployment/%s", opts.WorkloadName)
	portMapping := fmt.Sprintf("%d:%d", opts.LocalPort, opts.RemotePort)

	_, _ = fmt.Fprintf(out, "==> Establishing port-forward to %s [Local :%d -> Remote :%d] in namespace '%s'...\n",
		opts.WorkloadName, opts.LocalPort, opts.RemotePort, ns)

	cmd := p.command(ctx, "kubectl", "port-forward", target, portMapping, "-n", ns)
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("port-forward failed or interrupted: %w", err)
	}
	return nil
}

func ParsePortString(arg string) (int, int, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("port specification cannot be empty")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) == 1 {
		p, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port %s: %w", arg, err)
		}
		if p < 1 || p > 65535 {
			return 0, 0, fmt.Errorf("port %d out of valid range (1-65535)", p)
		}
		return p, p, nil
	}
	if len(parts) == 2 {
		lp, err1 := strconv.Atoi(parts[0])
		rp, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("invalid port specification %s", arg)
		}
		if lp < 1 || lp > 65535 || rp < 1 || rp > 65535 {
			return 0, 0, fmt.Errorf("ports in %s out of valid range (1-65535)", arg)
		}
		return lp, rp, nil
	}
	return 0, 0, fmt.Errorf("expected PORT or LOCAL:REMOTE format, got %s", arg)
}
