package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/sensi012/skipper/pkg/config"
)

func validateSafeArg(label, val string) error {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.HasPrefix(trimmed, "-") {
		return fmt.Errorf("invalid %s %q: must not start with '-'", label, val)
	}
	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid %s %q: must not contain whitespace or control characters", label, val)
		}
	}
	return nil
}

func validateOptionalSafeArg(label, val string) error {
	if strings.TrimSpace(val) == "" {
		return nil
	}
	return validateSafeArg(label, val)
}

// WorkloadStatus represents current execution state across Docker or Kubernetes
type WorkloadStatus struct {
	Name      string            `json:"name"`
	Target    string            `json:"target"` // local | kubernetes
	State     string            `json:"state"`  // Running, Degraded, Stopped, Error
	Replicas  int               `json:"replicas"`
	Available int               `json:"available"`
	Endpoints []string          `json:"endpoints"`
	CreatedAt time.Time         `json:"createdAt"`
	Labels    map[string]string `json:"labels"`
}

// LogOptions specifies constraints for streaming container or pod logs
type LogOptions struct {
	Follow     bool
	TailLines  int64
	Timestamps bool
	Container  string
}

// DebugOptions defines parameters for attaching an ephemeral forensic container
type DebugOptions struct {
	Namespace       string
	WorkloadName    string
	TargetPod       string
	TargetContainer string
	DebugImage      string
	Command         []string
	AttachStdin     bool
}

// RuntimeEngine is the core abstraction unifying Docker and Kubernetes runtimes.
type RuntimeEngine interface {
	// Name returns the driver identifier ("docker" or "kubernetes")
	Name() string

	// Initialize prepares the runtime target (e.g., verifying socket or cluster context)
	Initialize(ctx context.Context) error

	// Deploy launches or updates the workload according to the AppSpec
	Deploy(ctx context.Context, spec *config.AppSpec) (*WorkloadStatus, error)

	// Status queries the live status of the workload
	Status(ctx context.Context, workloadName string) (*WorkloadStatus, error)

	// Stop terminates the running workload
	Stop(ctx context.Context, workloadName string) error

	// Logs streams logs from the target workload to the provided writer
	Logs(ctx context.Context, workloadName string, opts LogOptions, out io.Writer) error

	// Debug initiates an interactive or ephemeral debug session
	Debug(ctx context.Context, opts DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (exitCode int, err error)
}
