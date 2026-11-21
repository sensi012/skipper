package runtime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/sensi012/skipper/pkg/opmem"
)

const DefaultDebugImage = "nicolaka/netshoot:latest"

// EphemeralSandboxOptions configures an ephemeral forensic debug sandbox session.
type EphemeralSandboxOptions struct {
	WorkloadName    string   `json:"workloadName"`
	Namespace       string   `json:"namespace"`
	Runtime         string   `json:"runtime"` // "kubernetes" | "docker"
	DebugImage      string   `json:"debugImage"`
	TargetPod       string   `json:"targetPod,omitempty"`
	TargetContainer string   `json:"targetContainer,omitempty"`
	CopyPod         bool     `json:"copyPod,omitempty"`
	CopyPodName     string   `json:"copyPodName,omitempty"`
	ShareProcesses  bool     `json:"shareProcesses,omitempty"`
	Command         []string `json:"command,omitempty"`
	DryRun          bool     `json:"dryRun,omitempty"`
	Operator        string   `json:"operator,omitempty"`
}

// EphemeralSandboxResult summarizes the planned or executed ephemeral debug session.
type EphemeralSandboxResult struct {
	SessionID  string   `json:"sessionId"`
	Runtime    string   `json:"runtime"`
	Target     string   `json:"target"`
	DebugImage string   `json:"debugImage"`
	Command    []string `json:"command"`
	DryRun     bool     `json:"dryRun"`
	ExitCode   int      `json:"exitCode"`
	Recorded   bool     `json:"recorded"`
}

// EphemeralSandboxLauncher launches ephemeral debug containers on Kubernetes or Docker
// and optionally records sessions into the operational memory ledger.
type EphemeralSandboxLauncher struct {
	Ledger      *opmem.OperationalMemoryLedger
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewEphemeralSandboxLauncher constructs an ephemeral sandbox launcher.
func NewEphemeralSandboxLauncher(ledger *opmem.OperationalMemoryLedger) *EphemeralSandboxLauncher {
	return &EphemeralSandboxLauncher{
		Ledger:      ledger,
		execCommand: exec.CommandContext,
	}
}

func (l *EphemeralSandboxLauncher) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if l.execCommand != nil {
		return l.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

// BuildCommand constructs the exact CLI binary, arguments, and resolved target for the sandbox.
func (l *EphemeralSandboxLauncher) BuildCommand(ctx context.Context, opts EphemeralSandboxOptions) (string, []string, string, error) {
	rt := strings.ToLower(strings.TrimSpace(opts.Runtime))
	if rt == "" {
		rt = "kubernetes"
	}

	debugImage := strings.TrimSpace(opts.DebugImage)
	if debugImage == "" {
		debugImage = DefaultDebugImage
	}

	cmdArgs := opts.Command
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"/bin/sh"}
	}

	switch rt {
	case "docker":
		workload := strings.TrimSpace(opts.WorkloadName)
		if workload == "" {
			return "", nil, "", fmt.Errorf("workload name is required for docker ephemeral sandbox")
		}
		args := []string{
			"run", "--rm", "-it",
			fmt.Sprintf("--pid=container:%s", workload),
			fmt.Sprintf("--network=container:%s", workload),
			debugImage,
		}
		args = append(args, cmdArgs...)
		return "docker", args, workload, nil

	case "kubernetes", "k8s":
		ns := strings.TrimSpace(opts.Namespace)
		if ns == "" {
			ns = "default"
		}
		workload := strings.TrimSpace(opts.WorkloadName)
		targetPod := strings.TrimSpace(opts.TargetPod)
		if targetPod == "" && workload == "" {
			return "", nil, "", fmt.Errorf("workload name or target pod is required for kubernetes ephemeral sandbox")
		}
		if targetPod == "" {
			if opts.DryRun {
				targetPod = fmt.Sprintf("pod/%s", workload)
			} else {
				podCmd := l.command(ctx, "kubectl", "get", "pods",
					"-l", fmt.Sprintf("app=%s", workload),
					"-n", ns,
					"-o", "jsonpath={.items[0].metadata.name}")
				out, err := podCmd.CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) == "" {
					return "", nil, "", fmt.Errorf("no pods found for workload %q in namespace %q: %s", workload, ns, strings.TrimSpace(string(out)))
				}
				targetPod = strings.TrimSpace(string(out))
			}
		}

		args := []string{"debug", "-it", targetPod, "-n", ns, fmt.Sprintf("--image=%s", debugImage)}
		if strings.TrimSpace(opts.TargetContainer) != "" {
			args = append(args, fmt.Sprintf("--target=%s", strings.TrimSpace(opts.TargetContainer)))
		}
		if opts.CopyPod {
			copyName := strings.TrimSpace(opts.CopyPodName)
			if copyName == "" {
				base := workload
				if base == "" {
					base = strings.TrimPrefix(targetPod, "pod/")
				}
				copyName = fmt.Sprintf("%s-debug", base)
			}
			args = append(args, fmt.Sprintf("--copy-to=%s", copyName))
		}
		if opts.ShareProcesses {
			args = append(args, "--share-processes")
		}
		args = append(args, "--")
		args = append(args, cmdArgs...)
		return "kubectl", args, targetPod, nil

	default:
		return "", nil, "", fmt.Errorf("unsupported ephemeral sandbox runtime %q (expected kubernetes or docker)", opts.Runtime)
	}
}

// Launch builds and executes (or dry-runs) an ephemeral debug sandbox, recording the session
// into the operational memory ledger when configured.
func (l *EphemeralSandboxLauncher) Launch(ctx context.Context, opts EphemeralSandboxOptions, stdin io.Reader, stdout, stderr io.Writer) (*EphemeralSandboxResult, error) {
	bin, args, target, err := l.BuildCommand(ctx, opts)
	if err != nil {
		return nil, err
	}

	rt := strings.ToLower(strings.TrimSpace(opts.Runtime))
	if rt == "" || rt == "k8s" {
		rt = "kubernetes"
	}
	ns := strings.TrimSpace(opts.Namespace)
	if ns == "" && rt == "kubernetes" {
		ns = "default"
	}
	debugImage := strings.TrimSpace(opts.DebugImage)
	if debugImage == "" {
		debugImage = DefaultDebugImage
	}

	fullCmd := append([]string{bin}, args...)
	startedAt := time.Now().UTC()
	sessionID := fmt.Sprintf("dbg-%d", startedAt.UnixNano())
	exitCode := 0

	var runErr error
	if !opts.DryRun {
		cmd := l.command(ctx, bin, args...)
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if err != nil {
			runErr = err
			exitCode = 1
		}
	}

	endedAt := time.Now().UTC()
	recorded := false
	if l.Ledger != nil {
		rec := &opmem.DebugSessionRecord{
			SessionID:    sessionID,
			StartedAt:    startedAt,
			EndedAt:      endedAt,
			Operator:     opts.Operator,
			TargetPod:    target,
			WorkloadName: opts.WorkloadName,
			Runtime:      rt,
			Namespace:    ns,
			DebugImage:   debugImage,
			DryRun:       opts.DryRun,
			CommandsExecuted: []opmem.ExecutedCommand{
				{
					Command:    strings.Join(fullCmd, " "),
					ExecutedAt: startedAt,
					ExitCode:   exitCode,
				},
			},
			ExitCode: exitCode,
		}
		if recErr := l.Ledger.RecordDebugSessionWithEvent(rec); recErr == nil {
			recorded = true
		}
	}

	res := &EphemeralSandboxResult{
		SessionID:  sessionID,
		Runtime:    rt,
		Target:     target,
		DebugImage: debugImage,
		Command:    fullCmd,
		DryRun:     opts.DryRun,
		ExitCode:   exitCode,
		Recorded:   recorded,
	}
	return res, runErr
}
