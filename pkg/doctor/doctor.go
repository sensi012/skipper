package doctor

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

type CheckStatus string

const (
	StatusPass CheckStatus = "PASS"
	StatusWarn CheckStatus = "WARN"
	StatusFail CheckStatus = "FAIL"
)

type DiagnosticCheck struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Message     string      `json:"message"`
	Remediation string      `json:"remediation,omitempty"`
}

type DoctorReport struct {
	Timestamp time.Time         `json:"timestamp"`
	Checks    []DiagnosticCheck `json:"checks"`
	Passed    bool              `json:"passed"`
}

type DoctorEngine struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

func NewDoctorEngine() *DoctorEngine {
	return &DoctorEngine{
		execCommand: exec.CommandContext,
	}
}

func (d *DoctorEngine) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if d.execCommand != nil {
		return d.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func (d *DoctorEngine) RunDiagnostics(ctx context.Context) *DoctorReport {
	report := &DoctorReport{
		Timestamp: time.Now(),
		Passed:    true,
	}

	report.Checks = append(report.Checks, d.checkDocker(ctx))
	report.Checks = append(report.Checks, d.checkKubectl(ctx))
	report.Checks = append(report.Checks, d.checkKind(ctx))
	report.Checks = append(report.Checks, d.checkPortConflicts([]int{80, 443, 8080})...)

	for _, check := range report.Checks {
		if check.Status == StatusFail {
			report.Passed = false
			break
		}
	}

	return report
}

func (d *DoctorEngine) checkDocker(ctx context.Context) DiagnosticCheck {
	cmd := d.command(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DiagnosticCheck{
			Name:        "Docker Engine Daemon",
			Status:      StatusFail,
			Message:     "Docker daemon is not running or unreachable.",
			Remediation: "Ensure Docker Desktop, Colima, or Rancher Desktop is started.",
		}
	}
	return DiagnosticCheck{
		Name:    "Docker Engine Daemon",
		Status:  StatusPass,
		Message: fmt.Sprintf("Running (Version: %s)", strings.TrimSpace(string(out))),
	}
}

func (d *DoctorEngine) checkKubectl(ctx context.Context) DiagnosticCheck {
	cmd := d.command(ctx, "kubectl", "version", "--client", "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DiagnosticCheck{
			Name:        "Kubernetes CLI (kubectl)",
			Status:      StatusWarn,
			Message:     "kubectl CLI is not installed or not in PATH.",
			Remediation: "Install kubectl to enable cluster deployments ('winget install Kubernetes.kubectl' or 'brew install kubectl').",
		}
	}
	_ = out
	return DiagnosticCheck{
		Name:    "Kubernetes CLI (kubectl)",
		Status:  StatusPass,
		Message: "Client binary available and configured.",
	}
}

func (d *DoctorEngine) checkKind(ctx context.Context) DiagnosticCheck {
	cmd := d.command(ctx, "kind", "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DiagnosticCheck{
			Name:        "Kind Local Cluster Tool",
			Status:      StatusWarn,
			Message:     "Kind CLI is not installed.",
			Remediation: "Install Kind to create zero-friction local clusters ('skipper local init').",
		}
	}
	return DiagnosticCheck{
		Name:    "Kind Local Cluster Tool",
		Status:  StatusPass,
		Message: strings.TrimSpace(string(out)),
	}
}

func (d *DoctorEngine) checkPortConflicts(ports []int) []DiagnosticCheck {
	var checks []DiagnosticCheck
	for _, port := range ports {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			checks = append(checks, DiagnosticCheck{
				Name:        fmt.Sprintf("Port Availability (%d)", port),
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Port %d is already in use by another process.", port),
				Remediation: fmt.Sprintf("Free port %d or specify a custom port in skipper.yaml endpoints.", port),
			})
		} else {
			_ = listener.Close()
			checks = append(checks, DiagnosticCheck{
				Name:    fmt.Sprintf("Port Availability (%d)", port),
				Status:  StatusPass,
				Message: fmt.Sprintf("Port %d is free.", port),
			})
		}
	}
	return checks
}
