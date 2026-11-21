package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

const validSkipperYAML = `name: payments-api
team: checkout
image: ghcr.io/acme/payments:1.0.0
endpoints:
  - name: http
    port: 8080
runtime:
  replicas:
    min: 2
    max: 4
  resources:
    cpu:
      request: 100m
      limit: 500m
    memory:
      request: 128Mi
      limit: 512Mi
security:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
`

func writeTempSpec(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "skipper.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp spec: %v", err)
	}
	return p
}

func extractText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestMCPServerCreationAndToolRegistry(t *testing.T) {
	srv := NewMCPServer()
	if srv == nil || srv.srv == nil {
		t.Fatalf("expected non-nil MCP server")
	}

	expectedTools := []string{
		"skipper_doctor",
		"skipper_init",
		"skipper_policy_check",
		"skipper_manifest",
		"skipper_diagnose",
		"skipper_rightsize",
		"skipper_rollback",
		"skipper_investigate_incident",
		"skipper_sbom",
		"skipper_drift_check",
	}

	tools := srv.srv.ListTools()
	if len(tools) != len(expectedTools) {
		t.Fatalf("expected %d registered tools, got %d", len(expectedTools), len(tools))
	}
	for _, name := range expectedTools {
		if srv.srv.GetTool(name) == nil {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestMCPHandleDoctor(t *testing.T) {
	req := mcp.CallToolRequest{}
	res, err := handleDoctor(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from handleDoctor: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error doctor result")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "checks:") {
		t.Fatalf("expected YAML report with checks, got:\n%s", text)
	}
}

func TestMCPHandleInit(t *testing.T) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "skipper_init",
			Arguments: map[string]interface{}{
				"name": "demo-svc",
				"port": 9090,
			},
		},
	}
	res, err := handleInit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from handleInit: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, res))
	}
	text := extractText(t, res)
	if !strings.Contains(text, "name: demo-svc") || !strings.Contains(text, "port: 9090") {
		t.Fatalf("expected generated spec with name and port, got:\n%s", text)
	}
}

func TestMCPHandlePolicyCheck(t *testing.T) {
	t.Run("valid spec passes policies", func(t *testing.T) {
		cfgPath := writeTempSpec(t, validSkipperYAML)
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_policy_check",
				Arguments: map[string]interface{}{"configPath": cfgPath},
			},
		}
		res, err := handlePolicyCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("expected non-error result, got: %s", extractText(t, res))
		}
		text := strings.TrimSpace(extractText(t, res))
		if text != "[]" {
			t.Fatalf("expected no violations ([]), got: %s", text)
		}
	})

	t.Run("non-compliant spec reports violations", func(t *testing.T) {
		cfgPath := writeTempSpec(t, "name: bad-app\n")
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_policy_check",
				Arguments: map[string]interface{}{"configPath": cfgPath},
			},
		}
		res, err := handlePolicyCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("expected non-error result with violations list")
		}
		text := extractText(t, res)
		if !strings.Contains(text, "rulename:") || !strings.Contains(text, "require-team-ownership") {
			t.Fatalf("expected policy violations in output, got:\n%s", text)
		}
	})

	t.Run("missing file returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_policy_check",
				Arguments: map[string]interface{}{"configPath": filepath.Join(t.TempDir(), "missing.yaml")},
			},
		}
		res, err := handlePolicyCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "unable to read") {
			t.Fatalf("expected unable to read error, got: %+v", res)
		}
	})

	t.Run("invalid YAML returns tool error", func(t *testing.T) {
		cfgPath := writeTempSpec(t, "name: [unclosed\n")
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_policy_check",
				Arguments: map[string]interface{}{"configPath": cfgPath},
			},
		}
		res, err := handlePolicyCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid YAML format") {
			t.Fatalf("expected invalid YAML error, got: %+v", res)
		}
	})
}

func TestMCPHandleManifest(t *testing.T) {
	t.Run("valid spec synthesizes manifests", func(t *testing.T) {
		cfgPath := writeTempSpec(t, validSkipperYAML)
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_manifest",
				Arguments: map[string]interface{}{
					"configPath": cfgPath,
					"namespace":  "prod",
				},
			},
		}
		res, err := handleManifest(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		text := extractText(t, res)
		if !strings.Contains(text, "kind: Deployment") || !strings.Contains(text, "namespace: prod") {
			t.Fatalf("expected Deployment in prod namespace, got:\n%s", text)
		}
	})

	t.Run("missing file returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_manifest",
				Arguments: map[string]interface{}{"configPath": filepath.Join(t.TempDir(), "nope.yaml")},
			},
		}
		res, err := handleManifest(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError {
			t.Fatal("expected tool error for missing config")
		}
	})
}

func TestMCPHandleDiagnose(t *testing.T) {
	t.Run("detects OOMKilled", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_diagnose",
				Arguments: map[string]interface{}{
					"workload": "cart-service",
					"logs":     "Out of memory: Killed process 42 exit code 137",
				},
			},
		}
		res, err := handleDiagnose(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error from handleDiagnose: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		text := extractText(t, res)
		if !strings.Contains(strings.ToLower(text), "oom") {
			t.Fatalf("expected OOM finding, got:\n%s", text)
		}
	})

	t.Run("missing workload returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_diagnose",
				Arguments: map[string]interface{}{"logs": "crash"},
			},
		}
		res, err := handleDiagnose(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid workload parameter") {
			t.Fatalf("expected invalid workload parameter error, got: %+v", res)
		}
	})

	t.Run("missing logs returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_diagnose",
				Arguments: map[string]interface{}{"workload": "cart-service"},
			},
		}
		res, err := handleDiagnose(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid logs parameter") {
			t.Fatalf("expected invalid logs parameter error, got: %+v", res)
		}
	})
}

func TestMCPHandleRightsize(t *testing.T) {
	t.Run("valid spec returns rightsizing recommendation", func(t *testing.T) {
		cfgPath := writeTempSpec(t, validSkipperYAML)
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_rightsize",
				Arguments: map[string]interface{}{"configPath": cfgPath},
			},
		}
		res, err := handleRightsize(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		text := extractText(t, res)
		if !strings.Contains(text, "payments-api") {
			t.Fatalf("expected workload name in rightsize report, got:\n%s", text)
		}
	})

	t.Run("missing file returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_rightsize",
				Arguments: map[string]interface{}{"configPath": filepath.Join(t.TempDir(), "absent.yaml")},
			},
		}
		res, err := handleRightsize(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError {
			t.Fatal("expected tool error for missing config")
		}
	})
}

func TestMCPJSONRPCDispatch(t *testing.T) {
	srv := NewMCPServer()
	ctx := context.Background()

	t.Run("tools/list returns all 6 tools", func(t *testing.T) {
		req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		resp := srv.srv.HandleMessage(ctx, req)
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		for _, toolName := range []string{"skipper_doctor", "skipper_init", "skipper_policy_check", "skipper_manifest", "skipper_diagnose", "skipper_rightsize"} {
			if !strings.Contains(string(raw), toolName) {
				t.Errorf("tools/list missing %q in %s", toolName, string(raw))
			}
		}
	})

	t.Run("tools/call dispatches skipper_diagnose", func(t *testing.T) {
		req := json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"skipper_diagnose","arguments":{"workload":"api","logs":"CrashLoopBackOff Back-off restarting failed container"}}}`)
		resp := srv.srv.HandleMessage(ctx, req)
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		if !strings.Contains(string(raw), "CrashLoop") && !strings.Contains(strings.ToLower(string(raw)), "crash") {
			t.Fatalf("expected crashloop diagnostic in JSON-RPC response, got: %s", string(raw))
		}
	})

	t.Run("tools/call unknown tool returns JSON-RPC error", func(t *testing.T) {
		req := json.RawMessage(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nonexistent_tool","arguments":{}}}`)
		resp := srv.srv.HandleMessage(ctx, req)
		if _, isErr := resp.(mcp.JSONRPCError); !isErr {
			t.Fatalf("expected JSONRPCError for unknown tool, got %T: %+v", resp, resp)
		}
	})

	t.Run("malformed JSON returns parse error", func(t *testing.T) {
		req := json.RawMessage(`{invalid json`)
		resp := srv.srv.HandleMessage(ctx, req)
		rpcErr, isErr := resp.(mcp.JSONRPCError)
		if !isErr || rpcErr.Error.Code != mcp.PARSE_ERROR {
			t.Fatalf("expected PARSE_ERROR, got %T: %+v", resp, resp)
		}
	})
}

func TestMCPHandleRollback(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	t.Run("successful rollback records operational memory and returns JSON", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_rollback",
				Arguments: map[string]interface{}{
					"workload":  "checkout-api",
					"revision":  "rev-41",
					"reason":    "p95 latency spike",
					"namespace": "prod",
				},
			},
		}
		res, err := handleRollback(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		var out RollbackResult
		if err := json.Unmarshal([]byte(extractText(t, res)), &out); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if out.Workload != "checkout-api" || out.TargetRevision != "rev-41" || out.Status != "ROLLED_BACK" || !out.RecordedInMem {
			t.Fatalf("unexpected rollback result: %+v", out)
		}
	})

	t.Run("defaults revision and reason when omitted", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_rollback",
				Arguments: map[string]interface{}{"workload": "orders-api"},
			},
		}
		res, err := handleRollback(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var out RollbackResult
		if err := json.Unmarshal([]byte(extractText(t, res)), &out); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if out.TargetRevision != "previous" || out.Namespace != "default" {
			t.Fatalf("unexpected defaults: %+v", out)
		}
	})

	t.Run("missing workload returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_rollback",
				Arguments: map[string]interface{}{"revision": "rev-1"},
			},
		}
		res, err := handleRollback(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid workload parameter") {
			t.Fatalf("expected workload validation error, got: %+v", res)
		}
	})
}

func TestMCPHandleInvestigateIncident(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	// Seed an operational memory event via rollback so timeline correlation has real data
	_, _ = handleRollback(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "skipper_rollback",
			Arguments: map[string]interface{}{"workload": "billing-svc", "revision": "v1.4.2", "reason": "canary failed"},
		},
	})

	t.Run("correlates OOM logs with operational memory timeline", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_investigate_incident",
				Arguments: map[string]interface{}{
					"workload":      "billing-svc",
					"logs":          "Out of memory: Killed process 99 exit code 137",
					"namespace":     "prod",
					"windowMinutes": 30,
				},
			},
		}
		res, err := handleInvestigateIncident(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		var report IncidentReport
		if err := json.Unmarshal([]byte(extractText(t, res)), &report); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if report.Workload != "billing-svc" || report.Severity != "CRITICAL" {
			t.Fatalf("expected CRITICAL incident for billing-svc, got: %+v", report)
		}
		if len(report.Findings) == 0 || len(report.Timeline) == 0 || len(report.RemediationSteps) == 0 {
			t.Fatalf("expected non-empty findings, timeline, and remediationSteps, got: %+v", report)
		}
	})

	t.Run("healthy logs produce INFO incident report", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_investigate_incident",
				Arguments: map[string]interface{}{
					"workload": "healthy-svc",
					"logs":     "GET /healthz 200 OK",
				},
			},
		}
		res, err := handleInvestigateIncident(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var report IncidentReport
		if err := json.Unmarshal([]byte(extractText(t, res)), &report); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if report.Severity != "INFO" || len(report.Findings) != 0 {
			t.Fatalf("expected INFO with 0 findings, got: %+v", report)
		}
	})

	t.Run("missing parameters return tool errors", func(t *testing.T) {
		res, _ := handleInvestigateIncident(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]interface{}{"logs": "err"}},
		})
		if !res.IsError {
			t.Fatal("expected error for missing workload")
		}
		res, _ = handleInvestigateIncident(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]interface{}{"workload": "svc"}},
		})
		if !res.IsError {
			t.Fatal("expected error for missing logs")
		}
	})
}

func TestMCPHandleSBOM(t *testing.T) {
	t.Run("generates CycloneDX SBOM for temp Go module", func(t *testing.T) {
		dir := t.TempDir()
		goMod := "module example.com/demo\n\ngo 1.25.0\n\nrequire gopkg.in/yaml.v3 v3.0.1\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
			t.Fatal(err)
		}
		outFile := filepath.Join(dir, "sbom.json")

		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_sbom",
				Arguments: map[string]interface{}{
					"target":     dir,
					"format":     "cyclonedx",
					"outputPath": outFile,
				},
			},
		}
		res, err := handleSBOM(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		var out SBOMToolResult
		if err := json.Unmarshal([]byte(extractText(t, res)), &out); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if out.ComponentCount == 0 || len(out.Document) == 0 {
			t.Fatalf("expected non-empty SBOM components and document, got: %+v", out)
		}
		if _, err := os.Stat(outFile); err != nil {
			t.Fatalf("expected SBOM file written to %s: %v", outFile, err)
		}
	})

	t.Run("invalid format returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_sbom",
				Arguments: map[string]interface{}{"format": "xml-invalid"},
			},
		}
		res, err := handleSBOM(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid SBOM format") {
			t.Fatalf("expected invalid format error, got: %+v", res)
		}
	})
}

func TestMCPHandleDriftCheck(t *testing.T) {
	desired := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.25
`
	liveDrifted := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.24
`

	t.Run("detects drift between desired and live YAML", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "skipper_drift_check",
				Arguments: map[string]interface{}{
					"desiredYaml": desired,
					"liveYaml":    liveDrifted,
				},
			},
		}
		res, err := handleDriftCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", extractText(t, res))
		}
		text := extractText(t, res)
		if !strings.Contains(text, `"hasDrift": true`) {
			t.Fatalf("expected hasDrift: true, got:\n%s", text)
		}
	})

	t.Run("missing configPath for live cluster drift returns tool error", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "skipper_drift_check",
				Arguments: map[string]interface{}{"configPath": filepath.Join(t.TempDir(), "missing.yaml")},
			},
		}
		res, err := handleDriftCheck(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}
		if !res.IsError {
			t.Fatal("expected tool error for missing configPath")
		}
	})
}
