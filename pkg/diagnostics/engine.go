package diagnostics

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/sensi012/skipper/pkg/opmem"
)

func validateDiagArg(label, val string) error {
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

// IncidentSeverity ranks diagnostic findings
type IncidentSeverity string

const (
	SeverityInfo     IncidentSeverity = "INFO"
	SeverityWarning  IncidentSeverity = "WARNING"
	SeverityCritical IncidentSeverity = "CRITICAL"
)

// DiagnosticFinding represents a deterministic or AI-grounded root-cause determination
type DiagnosticFinding struct {
	DetectorName   string           `json:"detectorName"`
	Severity       IncidentSeverity `json:"severity"`
	WorkloadName   string           `json:"workloadName"`
	RootCause      string           `json:"rootCause"`
	Evidence       []string         `json:"evidence"`
	SuggestedFix   string           `json:"suggestedFix"`
	RemediationCmd string           `json:"remediationCmd,omitempty"`
	Confidence     float64          `json:"confidence"` // 0.0 - 1.0
	Timestamp      time.Time        `json:"timestamp"`
}

// Detector interface implemented by all deterministic rule evaluators
type Detector interface {
	Name() string
	Evaluate(ctx context.Context, namespace, workload string, tailLogs string) (*DiagnosticFinding, error)
}

// DeterministicMatrix manages the rule-based diagnostic suite
type DeterministicMatrix struct {
	detectors []Detector
	ledger    *opmem.OperationalMemoryLedger
}

// NewDeterministicMatrix constructs the matrix with 8 critical Kubernetes detectors
func NewDeterministicMatrix(ledger *opmem.OperationalMemoryLedger) *DeterministicMatrix {
	return &DeterministicMatrix{
		ledger: ledger,
		detectors: []Detector{
			&OOMKilledDetector{},
			&CrashLoopDetector{},
			&ImagePullDetector{},
			&ReadinessProbeDetector{},
			&PendingPodDetector{},
			&NetworkDropDetector{},
			&IngressRoutingDetector{},
			&DNSResolutionDetector{},
		},
	}
}

// Diagnose executes all detectors and returns active incident findings
func (m *DeterministicMatrix) Diagnose(ctx context.Context, namespace, workload string, tailLogs string) ([]DiagnosticFinding, error) {
	var findings []DiagnosticFinding

	for _, d := range m.detectors {
		finding, err := d.Evaluate(ctx, namespace, workload, tailLogs)
		if err == nil && finding != nil {
			findings = append(findings, *finding)

			// Record symptom into operational memory
			if m.ledger != nil {
				_ = m.ledger.RecordTelemetryEvent(&opmem.CausalEvent{
					Timestamp:   time.Now(),
					Type:        "SYSTEM_SIGNAL",
					Source:      d.Name(),
					Description: fmt.Sprintf("[%s] %s on %s: %s", finding.Severity, finding.DetectorName, workload, finding.RootCause),
					Severity:    string(finding.Severity),
				})
			}
		}
	}

	return findings, nil
}

// 1. OOMKilled Detector (Exit code 137 or cgroup memory pressure)
type OOMKilledDetector struct{}

func (d *OOMKilledDetector) Name() string { return "OOMKilledDetector" }
func (d *OOMKilledDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "exit code 137") || strings.Contains(logs, "Out of memory") || strings.Contains(logs, "OOMKilled") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityCritical,
			WorkloadName:   workload,
			RootCause:      "Container terminated by Linux Kernel OOM (Out Of Memory) Killer. Memory limit breached.",
			Evidence:       []string{"Process exited with code 137", "Kernel OOM event detected in container cgroup"},
			SuggestedFix:   "Increase memory limits in skipper.yaml: 'runtime.resources.memory.limit: 1Gi'",
			RemediationCmd: "skipper rightsize",
			Confidence:     1.0,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 2. CrashLoopBackOff Detector (Exit code 1, 2, 139 Segmentation Fault)
type CrashLoopDetector struct{}

func (d *CrashLoopDetector) Name() string { return "CrashLoopDetector" }
func (d *CrashLoopDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "CrashLoopBackOff") || strings.Contains(logs, "panic:") || strings.Contains(logs, "SIGSEGV") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityCritical,
			WorkloadName:   workload,
			RootCause:      "Application process panicking or aborting immediately upon startup.",
			Evidence:       []string{"CrashLoopBackOff condition active", "Fatal runtime exception or unhandled panic detected in stderr"},
			SuggestedFix:   "Inspect application startup environment variables or run 'skipper debug' to inspect missing config files",
			RemediationCmd: fmt.Sprintf("skipper debug %s", workload),
			Confidence:     0.95,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 3. ImagePullBackOff / ErrImagePull Detector
type ImagePullDetector struct{}

func (d *ImagePullDetector) Name() string { return "ImagePullDetector" }
func (d *ImagePullDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "ImagePullBackOff") || strings.Contains(logs, "ErrImagePull") || strings.Contains(logs, "manifest unknown") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityCritical,
			WorkloadName:   workload,
			RootCause:      "Container registry returned 404/Not Found or image tag does not exist.",
			Evidence:       []string{"ErrImagePull reported by kubelet", "Registry authentication or image tag mismatch"},
			SuggestedFix:   "Run 'skipper build' to generate image locally or verify registry pull credentials",
			RemediationCmd: "skipper build",
			Confidence:     1.0,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 4. ReadinessProbe Failure Detector
type ReadinessProbeDetector struct{}

func (d *ReadinessProbeDetector) Name() string { return "ReadinessProbeDetector" }
func (d *ReadinessProbeDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "Readiness probe failed") || strings.Contains(logs, "connection refused") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityWarning,
			WorkloadName:   workload,
			RootCause:      "HTTP readiness probe endpoint failing or port unreachable.",
			Evidence:       []string{"Kubelet reported readiness probe HTTP 500 or connection refused"},
			SuggestedFix:   "Verify health.readiness.path and port configuration in skipper.yaml matches HTTP server listener",
			RemediationCmd: fmt.Sprintf("skipper logs %s", workload),
			Confidence:     0.90,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 5. Pending Pod Detector (Resource Starvation or Node Taints)
type PendingPodDetector struct{}

func (d *PendingPodDetector) Name() string { return "PendingPodDetector" }
func (d *PendingPodDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "Insufficient cpu") || strings.Contains(logs, "Insufficient memory") || strings.Contains(logs, "0/1 nodes are available") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityCritical,
			WorkloadName:   workload,
			RootCause:      "Pod cannot be scheduled due to cluster resource starvation (CPU/Memory limits exceed cluster capacity).",
			Evidence:       []string{"Kubernetes scheduler event: Insufficient compute resources"},
			SuggestedFix:   "Scale cluster node pool or lower requested resource allocations in skipper.yaml",
			RemediationCmd: "skipper rightsize",
			Confidence:     1.0,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 6. Network Drop Detector (NetworkPolicy ingress/egress blocks)
type NetworkDropDetector struct{}

func (d *NetworkDropDetector) Name() string { return "NetworkDropDetector" }
func (d *NetworkDropDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "NetworkPolicy dropped") || strings.Contains(logs, "Connection timed out") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityWarning,
			WorkloadName:   workload,
			RootCause:      "Network egress/ingress packets dropped by active NetworkPolicy isolation rules.",
			Evidence:       []string{"Connection timeout across inter-service call"},
			SuggestedFix:   "Declare required dependent service in skipper.yaml 'dependencies.services' to synthesize allow rules",
			RemediationCmd: "skipper manifest",
			Confidence:     0.85,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 7. Ingress Routing Failure Detector
type IngressRoutingDetector struct{}

func (d *IngressRoutingDetector) Name() string { return "IngressRoutingDetector" }
func (d *IngressRoutingDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "503 Service Temporarily Unavailable") || strings.Contains(logs, "no endpoints available") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityWarning,
			WorkloadName:   workload,
			RootCause:      "Ingress controller has no healthy backend Service endpoints available to route traffic.",
			Evidence:       []string{"HTTP 503 from ingress controller", "0 endpoints registered for backend Service"},
			SuggestedFix:   "Ensure pod readiness probe passes so pod is added to Service endpoints slice",
			RemediationCmd: fmt.Sprintf("skipper status %s", workload),
			Confidence:     0.90,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// 8. DNS Resolution Failure Detector
type DNSResolutionDetector struct{}

func (d *DNSResolutionDetector) Name() string { return "DNSResolutionDetector" }
func (d *DNSResolutionDetector) Evaluate(ctx context.Context, ns, workload, logs string) (*DiagnosticFinding, error) {
	if strings.Contains(logs, "dial tcp: lookup") || strings.Contains(logs, "server misbehaving") {
		return &DiagnosticFinding{
			DetectorName:   d.Name(),
			Severity:       SeverityWarning,
			WorkloadName:   workload,
			RootCause:      "Cluster CoreDNS resolution failed for hostname.",
			Evidence:       []string{"Lookup failure inside application socket layer"},
			SuggestedFix:   "Verify CoreDNS health and verify service names use standard '<service>.<namespace>.svc.cluster.local' conventions",
			RemediationCmd: "skipper local status",
			Confidence:     0.90,
			Timestamp:      time.Now(),
		}, nil
	}
	return nil, nil
}

// ContextGroundedEngine combines deterministic findings with operational memory RAG for AI-grade diagnosis
type ContextGroundedEngine struct {
	Matrix      *DeterministicMatrix
	Ledger      *opmem.OperationalMemoryLedger
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewContextGroundedEngine constructs the unified diagnostic reasoning engine
func NewContextGroundedEngine(ledger *opmem.OperationalMemoryLedger) *ContextGroundedEngine {
	return &ContextGroundedEngine{
		Matrix:      NewDeterministicMatrix(ledger),
		Ledger:      ledger,
		execCommand: exec.CommandContext,
	}
}

func (e *ContextGroundedEngine) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if e.execCommand != nil {
		return e.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

// RunDiagnostics executes the full deterministic + contextual analysis
func (e *ContextGroundedEngine) RunDiagnostics(ctx context.Context, ns, workload string) ([]DiagnosticFinding, error) {
	if err := validateDiagArg("workload name", workload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ns) == "" {
		ns = "default"
	}
	if err := validateDiagArg("namespace", ns); err != nil {
		return nil, err
	}

	// 1. Gather recent logs from target
	tailLogs := ""
	cmd := e.command(ctx, "kubectl", "logs", fmt.Sprintf("deployment/%s", workload), "-n", ns, "--tail=100")
	if out, err := cmd.CombinedOutput(); err == nil {
		tailLogs = string(out)
	}

	// 2. Query recent operational changes
	var timeline []opmem.CausalEvent
	if e.Ledger != nil {
		timeline, _ = e.Ledger.QueryTimeline(workload, 1*time.Hour)
	}

	// 3. Evaluate deterministic matrix
	matrix := e.Matrix
	if matrix == nil {
		matrix = NewDeterministicMatrix(e.Ledger)
	}
	findings, err := matrix.Diagnose(ctx, ns, workload, tailLogs)
	if err != nil {
		return nil, err
	}

	// 4. Ground findings with timeline causal context
	if len(timeline) > 0 && len(findings) > 0 {
		for i := range findings {
			lastEvent := timeline[len(timeline)-1]
			findings[i].Evidence = append(findings[i].Evidence,
				fmt.Sprintf("Preceded by mutation at %s: %s", lastEvent.Timestamp.Format("15:04:05"), lastEvent.Description))
		}
	}

	return findings, nil
}
