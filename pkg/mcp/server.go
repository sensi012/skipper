package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sensi012/skipper/pkg/bootstrapper"
	"github.com/sensi012/skipper/pkg/cluster"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/diagnostics"
	"github.com/sensi012/skipper/pkg/doctor"
	"github.com/sensi012/skipper/pkg/governance"
	"github.com/sensi012/skipper/pkg/opmem"
	"github.com/sensi012/skipper/pkg/rollout"
	"github.com/sensi012/skipper/pkg/security"
	"github.com/sensi012/skipper/pkg/synthesizer"
	"gopkg.in/yaml.v3"
)

const (
	// MaxToolOutputBytes caps individual MCP tool response payloads to prevent context bloat.
	MaxToolOutputBytes = 256 * 1024
	// maxInputLogBytes caps diagnostic log inputs scanned by regex detectors.
	maxInputLogBytes = 256 * 1024
)

var mcpLedgerMu sync.Mutex

type MCPServer struct {
	srv *server.MCPServer
}

func NewMCPServer() *MCPServer {
	s := server.NewMCPServer("skipper-mcp", "1.0.0")

	// 1. Tool: skipper_doctor
	doctorTool := mcp.NewTool("skipper_doctor",
		mcp.WithDescription("Audits developer environment prerequisites: Docker daemon status, kubectl availability, Kind CLI, and port conflicts"),
	)
	s.AddTool(doctorTool, wrapToolHandler("skipper_doctor", handleDoctor))

	// 2. Tool: skipper_init
	initTool := mcp.NewTool("skipper_init",
		mcp.WithDescription("Inspects the current project and generates a production-hardened skipper.yaml"),
		mcp.WithString("name", mcp.Description("Optional workload name")),
		mcp.WithNumber("port", mcp.Description("Optional primary container port")),
	)
	s.AddTool(initTool, wrapToolHandler("skipper_init", handleInit))

	// 3. Tool: skipper_policy_check
	policyTool := mcp.NewTool("skipper_policy_check",
		mcp.WithDescription("Validates skipper.yaml against organizational compliance and security policies"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml (defaults to skipper.yaml)")),
	)
	s.AddTool(policyTool, wrapToolHandler("skipper_policy_check", handlePolicyCheck))

	// 4. Tool: skipper_manifest
	manifestTool := mcp.NewTool("skipper_manifest",
		mcp.WithDescription("Compiles skipper.yaml into complete hardened Kubernetes manifests (Deployments, Services, NetworkPolicies, PDBs)"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
	)
	s.AddTool(manifestTool, wrapToolHandler("skipper_manifest", handleManifest))

	// 5. Tool: skipper_diagnose
	diagnoseTool := mcp.NewTool("skipper_diagnose",
		mcp.WithDescription("Runs the 8-detector deterministic matrix on workload logs to identify root cause (OOM, CrashLoop, probe fail, etc.)"),
		mcp.WithString("workload", mcp.Required(), mcp.Description("Workload name to diagnose")),
		mcp.WithString("logs", mcp.Required(), mcp.Description("Recent container logs or events")),
	)
	s.AddTool(diagnoseTool, wrapToolHandler("skipper_diagnose", handleDiagnose))

	// 6. Tool: skipper_rightsize
	rightsizeTool := mcp.NewTool("skipper_rightsize",
		mcp.WithDescription("Analyzes workload resource allocations vs actual P95 utilization (Prometheus, kubectl top, or baseline) to calculate monthly cloud cost savings"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
		mcp.WithString("prometheusUrl", mcp.Description("Optional Prometheus HTTP endpoint for live P95 metrics")),
		mcp.WithBoolean("useKubectlTop", mcp.Description("Query live pod metrics via kubectl top")),
	)
	s.AddTool(rightsizeTool, wrapToolHandler("skipper_rightsize", handleRightsize))

	// 7. Tool: skipper_rollback
	rollbackTool := mcp.NewTool("skipper_rollback",
		mcp.WithDescription("Executes or dry-runs a Kubernetes deployment rollback (kubectl rollout undo) and records the mutation in operational memory"),
		mcp.WithString("workload", mcp.Required(), mcp.Description("Workload name to roll back")),
		mcp.WithString("revision", mcp.Description("Target revision number or 'previous' (default: previous)")),
		mcp.WithString("reason", mcp.Description("Reason for triggering rollback")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
		mcp.WithString("context", mcp.Description("Optional kubeconfig context name")),
		mcp.WithBoolean("dryRun", mcp.Description("Preview kubectl rollout undo command without executing (default: true for safety)")),
	)
	s.AddTool(rollbackTool, wrapToolHandler("skipper_rollback", handleRollback))

	// 8. Tool: skipper_investigate_incident
	investigateTool := mcp.NewTool("skipper_investigate_incident",
		mcp.WithDescription("Correlates operational memory timeline events with 8-detector diagnostics to produce a unified incident report"),
		mcp.WithString("workload", mcp.Required(), mcp.Description("Workload name to investigate")),
		mcp.WithString("logs", mcp.Required(), mcp.Description("Recent container logs or events")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
		mcp.WithNumber("windowMinutes", mcp.Description("Lookback window in minutes for operational memory timeline (default: 60)")),
	)
	s.AddTool(investigateTool, wrapToolHandler("skipper_investigate_incident", handleInvestigateIncident))

	// 9. Tool: skipper_sbom
	sbomTool := mcp.NewTool("skipper_sbom",
		mcp.WithDescription("Generates a CycloneDX 1.5 or SPDX 2.3 Software Bill of Materials (SBOM) for a directory or OCI image"),
		mcp.WithString("target", mcp.Description("Target project directory or OCI image reference (default: .)")),
		mcp.WithString("format", mcp.Description("SBOM format: cyclonedx or spdx (default: cyclonedx)")),
		mcp.WithString("outputPath", mcp.Description("Optional file path to write the generated SBOM JSON")),
	)
	s.AddTool(sbomTool, wrapToolHandler("skipper_sbom", handleSBOM))

	// 10. Tool: skipper_drift_check
	driftTool := mcp.NewTool("skipper_drift_check",
		mcp.WithDescription("Detects configuration drift between desired and live Kubernetes manifests or against a target cluster context"),
		mcp.WithString("desiredYaml", mcp.Description("Desired Kubernetes manifest YAML (for direct manifest comparison)")),
		mcp.WithString("liveYaml", mcp.Description("Live Kubernetes manifest YAML (for direct manifest comparison)")),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml (used for live cluster drift check when desiredYaml is omitted)")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
		mcp.WithString("context", mcp.Description("Optional kubeconfig context name")),
	)
	s.AddTool(driftTool, wrapToolHandler("skipper_drift_check", handleDriftCheck))

	return &MCPServer{srv: s}
}

func wrapToolHandler(toolName string, fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("%s aborted: %v", toolName, ctxErr)), nil
			}
		} else {
			ctx = context.Background()
		}

		defer func() {
			if r := recover(); r != nil {
				res = mcp.NewToolResultError(fmt.Sprintf("internal error in %s: %v", toolName, r))
				err = nil
			}
		}()

		res, err = fn(ctx, request)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("%s error: %v", toolName, err)), nil
		}
		if res == nil {
			return mcp.NewToolResultError(fmt.Sprintf("%s returned empty result", toolName)), nil
		}
		return boundToolResult(res), nil
	}
}

func boundToolResult(res *mcp.CallToolResult) *mcp.CallToolResult {
	if res == nil {
		return nil
	}
	for i, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && len(tc.Text) > MaxToolOutputBytes {
			suffix := fmt.Sprintf("\n... [truncated: output exceeded %d bytes limit]", MaxToolOutputBytes)
			tc.Text = tc.Text[:MaxToolOutputBytes-len(suffix)] + suffix
			res.Content[i] = tc
		}
	}
	return res
}

func boundInputLogs(logs string) string {
	if len(logs) <= maxInputLogBytes {
		return logs
	}
	return logs[len(logs)-maxInputLogBytes:]
}

// Start runs the MCP JSON-RPC 2.0 server over standard I/O, honoring ctx cancellation
// and routing any transport error logs strictly to os.Stderr.
func (m *MCPServer) Start(ctx context.Context) error {
	return m.ServeIO(ctx, os.Stdin, os.Stdout, os.Stderr)
}

// ServeIO runs the MCP JSON-RPC 2.0 loop over custom reader/writer streams.
func (m *MCPServer) ServeIO(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if stderr == nil {
		stderr = io.Discard
	}
	stdioSrv := server.NewStdioServer(m.srv)
	stdioSrv.SetErrorLogger(log.New(stderr, "", 0))
	return stdioSrv.Listen(ctx, stdin, stdout)
}

func handleDoctor(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("doctor cancelled: %v", err)), nil
	}
	doc := doctor.NewDoctorEngine()
	report := doc.RunDiagnostics(ctx)
	data, _ := yaml.Marshal(report)
	return mcp.NewToolResultText(string(data)), nil
}

func handleInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("init cancelled: %v", err)), nil
	}
	name := strings.TrimSpace(request.GetString("name", ""))
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/\\ \t\r\n") {
		return mcp.NewToolResultError(fmt.Sprintf("invalid workload name %q", name)), nil
	}
	port := request.GetInt("port", 0)
	if port < 0 || port > 65535 {
		return mcp.NewToolResultError(fmt.Sprintf("invalid port %d: must be between 0 and 65535", port)), nil
	}

	initr := bootstrapper.NewProjectInitializer()
	spec, err := initr.InspectAndGenerate(ctx, bootstrapper.InitOptions{
		Name: name,
		Port: port,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("init failed: %v", err)), nil
	}

	data, _ := yaml.Marshal(spec)
	return mcp.NewToolResultText(string(data)), nil
}

func handlePolicyCheck(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("policy check cancelled: %v", err)), nil
	}
	cfgPath := strings.TrimSpace(request.GetString("configPath", "skipper.yaml"))
	if cfgPath == "" {
		cfgPath = "skipper.yaml"
	}

	spec, err := readSpec(cfgPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	gov := governance.NewGovernanceEngine()
	violations, err := gov.EvaluatePolicies(spec)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := yaml.Marshal(violations)
	return mcp.NewToolResultText(string(data)), nil
}

func handleManifest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("manifest synthesis cancelled: %v", err)), nil
	}
	cfgPath := strings.TrimSpace(request.GetString("configPath", "skipper.yaml"))
	if cfgPath == "" {
		cfgPath = "skipper.yaml"
	}
	ns := strings.TrimSpace(request.GetString("namespace", "default"))
	if ns == "" {
		ns = "default"
	}

	spec, err := readSpec(cfgPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	synth := synthesizer.NewSynthesizer()
	bundle, err := synth.Synthesize(spec, ns)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(bundle.FullYaml()), nil
}

func handleDiagnose(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("diagnose cancelled: %v", err)), nil
	}
	workload, err := request.RequireString("workload")
	if err != nil || strings.TrimSpace(workload) == "" {
		return mcp.NewToolResultError(fmt.Sprintf("invalid workload parameter: %v", err)), nil
	}
	logs, err := request.RequireString("logs")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid logs parameter: %v", err)), nil
	}

	matrix := diagnostics.NewDeterministicMatrix(nil)
	findings, err := matrix.Diagnose(ctx, "default", strings.TrimSpace(workload), boundInputLogs(logs))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := yaml.Marshal(findings)
	return mcp.NewToolResultText(string(data)), nil
}

func handleRightsize(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rightsize cancelled: %v", err)), nil
	}
	cfgPath := strings.TrimSpace(request.GetString("configPath", "skipper.yaml"))
	if cfgPath == "" {
		cfgPath = "skipper.yaml"
	}
	ns := strings.TrimSpace(request.GetString("namespace", "default"))
	if ns == "" {
		ns = "default"
	}
	promURL := strings.TrimSpace(request.GetString("prometheusUrl", ""))
	useTop := request.GetBool("useKubectlTop", false)

	spec, err := readSpec(cfgPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	gov := governance.NewGovernanceEngine()
	res, err := gov.AnalyzeRightsizingWithOptions(ctx, spec, governance.RightsizingOptions{
		Namespace:     ns,
		PrometheusURL: promURL,
		UseKubectlTop: useTop,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := yaml.Marshal(res)
	return mcp.NewToolResultText(string(data)), nil
}

// RollbackResult aliases rollout.RollbackResult for structured JSON MCP responses.
type RollbackResult = rollout.RollbackResult

// IncidentReport represents the correlated output of skipper_investigate_incident.
type IncidentReport struct {
	Workload         string                          `json:"workload"`
	Namespace        string                          `json:"namespace"`
	Severity         string                          `json:"severity"`
	RootCause        string                          `json:"rootCause"`
	Findings         []diagnostics.DiagnosticFinding `json:"findings"`
	Timeline         []opmem.CausalEvent             `json:"timeline"`
	DebugSessions    []opmem.DebugSessionRecord      `json:"debugSessions,omitempty"`
	RemediationSteps []string                        `json:"remediationSteps"`
	GeneratedAt      time.Time                       `json:"generatedAt"`
}

func handleRollback(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rollback cancelled: %v", err)), nil
	}
	workload, err := request.RequireString("workload")
	if err != nil || strings.TrimSpace(workload) == "" {
		return mcp.NewToolResultError(fmt.Sprintf("invalid workload parameter: %v", err)), nil
	}
	revision := strings.TrimSpace(request.GetString("revision", "previous"))
	if revision == "" {
		revision = "previous"
	}
	reason := strings.TrimSpace(request.GetString("reason", "rollback triggered via MCP"))
	if reason == "" {
		reason = "rollback triggered via MCP"
	}
	ns := strings.TrimSpace(request.GetString("namespace", "default"))
	if ns == "" {
		ns = "default"
	}
	ctxName := strings.TrimSpace(request.GetString("context", ""))
	dryRun := request.GetBool("dryRun", true)

	mcpLedgerMu.Lock()
	ledger, _ := opmem.NewOperationalMemoryLedger("")
	mgr := rollout.NewRollbackManager(ledger)
	res, err := mgr.Execute(ctx, rollout.RollbackOptions{
		WorkloadName: workload,
		Namespace:    ns,
		Revision:     revision,
		Reason:       reason,
		ContextName:  ctxName,
		DryRun:       dryRun,
	})
	mcpLedgerMu.Unlock()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal rollback result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func severityRank(s diagnostics.IncidentSeverity) int {
	switch s {
	case diagnostics.SeverityCritical:
		return 3
	case diagnostics.SeverityWarning:
		return 2
	default:
		return 1
	}
}

func handleInvestigateIncident(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("incident investigation cancelled: %v", err)), nil
	}
	workload, err := request.RequireString("workload")
	if err != nil || strings.TrimSpace(workload) == "" {
		return mcp.NewToolResultError(fmt.Sprintf("invalid workload parameter: %v", err)), nil
	}
	logs, err := request.RequireString("logs")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid logs parameter: %v", err)), nil
	}
	ns := strings.TrimSpace(request.GetString("namespace", "default"))
	if ns == "" {
		ns = "default"
	}
	windowMin := request.GetInt("windowMinutes", 60)
	if windowMin <= 0 {
		windowMin = 60
	}

	mcpLedgerMu.Lock()
	ledger, _ := opmem.NewOperationalMemoryLedger("")
	matrix := diagnostics.NewDeterministicMatrix(ledger)
	findings, err := matrix.Diagnose(ctx, ns, strings.TrimSpace(workload), boundInputLogs(logs))
	var timeline []opmem.CausalEvent
	var sessions []opmem.DebugSessionRecord
	if err == nil && ledger != nil {
		if evts, qErr := ledger.QueryTimeline(workload, time.Duration(windowMin)*time.Minute); qErr == nil {
			timeline = evts
		}
		if sess, sErr := ledger.ListDebugSessions(workload); sErr == nil {
			sessions = sess
		}
	}
	mcpLedgerMu.Unlock()

	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if findings == nil {
		findings = []diagnostics.DiagnosticFinding{}
	}
	if timeline == nil {
		timeline = []opmem.CausalEvent{}
	}

	highestSev := diagnostics.SeverityInfo
	rootCause := "No deterministic anomalies detected in provided logs"
	var remediationSteps []string
	seenSteps := make(map[string]bool)

	for _, f := range findings {
		if severityRank(f.Severity) > severityRank(highestSev) || rootCause == "No deterministic anomalies detected in provided logs" {
			highestSev = f.Severity
			rootCause = f.RootCause
		}
		if f.SuggestedFix != "" && !seenSteps[f.SuggestedFix] {
			seenSteps[f.SuggestedFix] = true
			remediationSteps = append(remediationSteps, f.SuggestedFix)
		}
		if f.RemediationCmd != "" {
			step := fmt.Sprintf("Run: %s", f.RemediationCmd)
			if !seenSteps[step] {
				seenSteps[step] = true
				remediationSteps = append(remediationSteps, step)
			}
		}
	}
	if len(remediationSteps) == 0 {
		remediationSteps = []string{"Continue monitoring workload health and telemetry."}
	}

	report := IncidentReport{
		Workload:         workload,
		Namespace:        ns,
		Severity:         string(highestSev),
		RootCause:        rootCause,
		Findings:         findings,
		Timeline:         timeline,
		DebugSessions:    sessions,
		RemediationSteps: remediationSteps,
		GeneratedAt:      time.Now().UTC(),
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal incident report: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// SBOMToolResult wraps security.SBOMReport for structured JSON MCP responses.
type SBOMToolResult struct {
	Format         security.SBOMFormat `json:"format"`
	Target         string              `json:"target"`
	Tool           string              `json:"tool"`
	ComponentCount int                 `json:"componentCount"`
	OutputPath     string              `json:"outputPath,omitempty"`
	Document       json.RawMessage     `json:"document"`
}

func handleSBOM(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("SBOM generation cancelled: %v", err)), nil
	}
	target := strings.TrimSpace(request.GetString("target", "."))
	if target == "" {
		target = "."
	}
	formatRaw := request.GetString("format", "cyclonedx")
	format, err := security.ParseSBOMFormat(formatRaw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid SBOM format: %v", err)), nil
	}
	outputPath := strings.TrimSpace(request.GetString("outputPath", ""))

	gen := security.NewSBOMGenerator()
	report, err := gen.Generate(ctx, security.SBOMOptions{
		Target:     target,
		Format:     format,
		OutputPath: outputPath,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("SBOM generation failed: %v", err)), nil
	}

	out := SBOMToolResult{
		Format:         report.Format,
		Target:         report.Target,
		Tool:           report.Tool,
		ComponentCount: report.ComponentCount,
		OutputPath:     report.OutputPath,
		Document:       json.RawMessage(report.Document),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal SBOM result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func handleDriftCheck(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("drift check cancelled: %v", err)), nil
	}
	desiredYAML := request.GetString("desiredYaml", "")
	liveYAML := request.GetString("liveYaml", "")
	detector := cluster.NewDriftDetector()

	var report *cluster.DriftReport
	var err error

	if strings.TrimSpace(desiredYAML) != "" || strings.TrimSpace(liveYAML) != "" {
		report, err = detector.DetectDrift(desiredYAML, liveYAML)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("drift check failed: %v", err)), nil
		}
	} else {
		cfgPath := strings.TrimSpace(request.GetString("configPath", "skipper.yaml"))
		if cfgPath == "" {
			cfgPath = "skipper.yaml"
		}
		ns := strings.TrimSpace(request.GetString("namespace", "default"))
		if ns == "" {
			ns = "default"
		}
		ctxName := strings.TrimSpace(request.GetString("context", ""))

		spec, err := readSpec(cfgPath)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		report, err = detector.DetectClusterDrift(ctx, spec, ns, ctxName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("cluster drift check failed: %v", err)), nil
		}
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal drift report: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func readSpec(path string) (*config.AppSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", path, err)
	}
	var spec config.AppSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("invalid YAML format in %s: %w", path, err)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("invalid spec in %s: workload name must not be empty", path)
	}
	return &spec, nil
}
