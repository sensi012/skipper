package mcp

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/unified-platform/container/pkg/bootstrapper"
	"github.com/unified-platform/container/pkg/config"
	"github.com/unified-platform/container/pkg/diagnostics"
	"github.com/unified-platform/container/pkg/doctor"
	"github.com/unified-platform/container/pkg/governance"
	"github.com/unified-platform/container/pkg/synthesizer"
	"gopkg.in/yaml.v3"
)

type MCPServer struct {
	srv *server.MCPServer
}

func NewMCPServer() *MCPServer {
	s := server.NewMCPServer("skipper-mcp", "1.0.0")

	// 1. Tool: skipper_doctor
	doctorTool := mcp.NewTool("skipper_doctor",
		mcp.WithDescription("Audits developer environment prerequisites: Docker daemon status, kubectl availability, Kind CLI, and port conflicts"),
	)
	s.AddTool(doctorTool, handleDoctor)

	// 2. Tool: skipper_init
	initTool := mcp.NewTool("skipper_init",
		mcp.WithDescription("Inspects the current project and generates a production-hardened skipper.yaml"),
		mcp.WithString("name", mcp.Description("Optional workload name")),
		mcp.WithNumber("port", mcp.Description("Optional primary container port")),
	)
	s.AddTool(initTool, handleInit)

	// 3. Tool: skipper_policy_check
	policyTool := mcp.NewTool("skipper_policy_check",
		mcp.WithDescription("Validates skipper.yaml against organizational compliance and security policies"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml (defaults to skipper.yaml)")),
	)
	s.AddTool(policyTool, handlePolicyCheck)

	// 4. Tool: skipper_manifest
	manifestTool := mcp.NewTool("skipper_manifest",
		mcp.WithDescription("Compiles skipper.yaml into complete hardened Kubernetes manifests (Deployments, Services, NetworkPolicies, PDBs)"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml")),
		mcp.WithString("namespace", mcp.Description("Target Kubernetes namespace (default: default)")),
	)
	s.AddTool(manifestTool, handleManifest)

	// 5. Tool: skipper_diagnose
	diagnoseTool := mcp.NewTool("skipper_diagnose",
		mcp.WithDescription("Runs the 8-detector deterministic matrix on workload logs to identify root cause (OOM, CrashLoop, probe fail, etc.)"),
		mcp.WithString("workload", mcp.Required(), mcp.Description("Workload name to diagnose")),
		mcp.WithString("logs", mcp.Required(), mcp.Description("Recent container logs or events")),
	)
	s.AddTool(diagnoseTool, handleDiagnose)

	// 6. Tool: skipper_rightsize
	rightsizeTool := mcp.NewTool("skipper_rightsize",
		mcp.WithDescription("Analyzes workload resource allocations vs actual P95 utilization to calculate monthly cloud cost savings"),
		mcp.WithString("configPath", mcp.Description("Path to skipper.yaml")),
	)
	s.AddTool(rightsizeTool, handleRightsize)

	return &MCPServer{srv: s}
}

func (m *MCPServer) Start(ctx context.Context) error {
	return server.ServeStdio(m.srv)
}

func handleDoctor(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	doc := doctor.NewDoctorEngine()
	report := doc.RunDiagnostics(ctx)
	data, _ := yaml.Marshal(report)
	return mcp.NewToolResultText(string(data)), nil
}

func handleInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := request.GetString("name", "")
	port := request.GetInt("port", 0)

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
	cfgPath := request.GetString("configPath", "skipper.yaml")

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
	cfgPath := request.GetString("configPath", "skipper.yaml")
	ns := request.GetString("namespace", "default")

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
	workload, _ := request.RequireString("workload")
	logs, _ := request.RequireString("logs")

	matrix := diagnostics.NewDeterministicMatrix(nil)
	findings, err := matrix.Diagnose(ctx, "default", workload, logs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := yaml.Marshal(findings)
	return mcp.NewToolResultText(string(data)), nil
}

func handleRightsize(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfgPath := request.GetString("configPath", "skipper.yaml")

	spec, err := readSpec(cfgPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	gov := governance.NewGovernanceEngine()
	res, err := gov.AnalyzeRightsizing(ctx, spec)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, _ := yaml.Marshal(res)
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
	return &spec, nil
}
