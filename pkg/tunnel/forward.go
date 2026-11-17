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

type PortForwarder struct{}

func NewPortForwarder() *PortForwarder {
	return &PortForwarder{}
}

func (p *PortForwarder) Forward(ctx context.Context, opts PortForwardOptions, out, errOut io.Writer) error {
	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}

	target := fmt.Sprintf("deployment/%s", opts.WorkloadName)
	portMapping := fmt.Sprintf("%d:%d", opts.LocalPort, opts.RemotePort)

	_, _ = fmt.Fprintf(out, "==> Establishing port-forward to %s [Local :%d -> Remote :%d] in namespace '%s'...\n",
		opts.WorkloadName, opts.LocalPort, opts.RemotePort, ns)

	cmd := exec.CommandContext(ctx, "kubectl", "port-forward", target, portMapping, "-n", ns)
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("port-forward failed or interrupted: %w", err)
	}
	return nil
}

func ParsePortString(arg string) (int, int, error) {
	parts := strings.Split(arg, ":")
	if len(parts) == 1 {
		p, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port %s: %w", arg, err)
		}
		return p, p, nil
	}
	if len(parts) == 2 {
		lp, err1 := strconv.Atoi(parts[0])
		rp, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("invalid port specification %s", arg)
		}
		return lp, rp, nil
	}
	return 0, 0, fmt.Errorf("expected PORT or LOCAL:REMOTE format, got %s", arg)
}
