package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/diagnostics"
	"github.com/sensi012/skipper/pkg/doctor"
	"github.com/sensi012/skipper/pkg/governance"
	"github.com/sensi012/skipper/pkg/opmem"
	"github.com/sensi012/skipper/pkg/runtime"
)

func resetGlobals(t *testing.T) {
	t.Helper()
	origRuntime := targetRuntime
	origNamespace := namespace
	origConfig := appConfigFile
	origFormat := outputFormat
	targetRuntime = "docker"
	namespace = "default"
	appConfigFile = ""
	outputFormat = "text"
	t.Cleanup(func() {
		targetRuntime = origRuntime
		namespace = origNamespace
		appConfigFile = origConfig
		outputFormat = origFormat
	})
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", tmpDir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})
	return tmpDir
}

func executeCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

const sampleCompliantSpec = `version: v1alpha1
name: demo-service
team: platform
environment: production
type: service
image: ghcr.io/acme/demo-service:1.2.3
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
      limit: 256Mi
endpoints:
  - name: http
    port: 8080
    protocol: TCP
    public: true
health:
  readiness:
    path: /ready
    port: 8080
  liveness:
    path: /healthz
    port: 8080
security:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
`

const sampleNonCompliantSpec = `version: v1alpha1
name: risky-app
environment: production
type: service
image: nginx:latest
endpoints:
  - name: http
    port: 80
`

func TestRootCmdFlagsAndSilence(t *testing.T) {
	resetGlobals(t)

	root := newRootCmd()
	if !root.SilenceUsage {
		t.Error("expected rootCmd.SilenceUsage to be true")
	}
	if !root.SilenceErrors {
		t.Error("expected rootCmd.SilenceErrors to be true")
	}

	// --version
	out, _, err := executeCLI(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if !strings.Contains(out, version) {
		t.Errorf("expected version output to contain %q, got %q", version, out)
	}

	// --help
	out, _, err = executeCLI(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	for _, want := range []string{"--runtime", "--namespace", "--config", "--format", "doctor", "explain", "manifest", "policy-check", "secret", "import"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected --help output to contain %q", want)
		}
	}

	// Persistent flag parsing
	resetGlobals(t)
	_, _, err = executeCLI(t, "--runtime", "kubernetes", "--namespace", "staging", "--config", "custom.yaml", "--format", "json", "explain")
	if err != nil {
		t.Fatalf("persistent flags with explain failed: %v", err)
	}
	if targetRuntime != "kubernetes" {
		t.Errorf("expected targetRuntime=kubernetes, got %q", targetRuntime)
	}
	if namespace != "staging" {
		t.Errorf("expected namespace=staging, got %q", namespace)
	}
	if appConfigFile != "custom.yaml" {
		t.Errorf("expected appConfigFile=custom.yaml, got %q", appConfigFile)
	}
	if outputFormat != "json" {
		t.Errorf("expected outputFormat=json, got %q", outputFormat)
	}

	// Runtime error does not dump usage or duplicate Error: via cobra
	resetGlobals(t)
	chdirTemp(t)
	stdout, stderr, err := executeCLI(t, "manifest")
	if err == nil {
		t.Fatal("expected error when skipper.yaml is missing")
	}
	if strings.Contains(stdout, "Usage:") || strings.Contains(stderr, "Usage:") {
		t.Errorf("expected SilenceUsage to suppress usage on runtime error, got stdout=%q stderr=%q", stdout, stderr)
	}
	if strings.Contains(stderr, "Error:") {
		t.Errorf("expected SilenceErrors to suppress Cobra Error: output, got stderr=%q", stderr)
	}
}

func TestShortFlagShadowingFixes(t *testing.T) {
	resetGlobals(t)

	root := newRootCmd()

	// Verify port-forward does NOT define a local -n / --namespace flag
	pfCmd, _, err := root.Find([]string{"port-forward"})
	if err != nil {
		t.Fatalf("failed to find port-forward command: %v", err)
	}
	if pfCmd.LocalFlags().Lookup("namespace") != nil {
		t.Error("expected port-forward not to declare a duplicate local --namespace flag")
	}
	if pfCmd.LocalFlags().ShorthandLookup("n") != nil {
		t.Error("expected port-forward not to declare a duplicate local -n shorthand")
	}

	// Verify init --name has no -n shorthand collision with global -n/--namespace
	initCmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatalf("failed to find init command: %v", err)
	}
	nameFlag := initCmd.LocalFlags().Lookup("name")
	if nameFlag == nil {
		t.Fatal("expected init command to have --name flag")
	}
	if nameFlag.Shorthand != "" {
		t.Errorf("expected init --name flag to have no shorthand, got %q", nameFlag.Shorthand)
	}
	if initCmd.LocalFlags().ShorthandLookup("n") != nil {
		t.Error("expected init command not to shadow global -n shorthand")
	}

	// Both help outputs should render cleanly
	out, _, err := executeCLI(t, "init", "--help")
	if err != nil {
		t.Fatalf("init --help failed: %v", err)
	}
	if strings.Contains(out, "-n, --name ") {
		t.Errorf("init --help still shows -n, --name collision: %s", out)
	}
	if !strings.Contains(out, "--name string") {
		t.Errorf("expected init --help to show --name string flag: %s", out)
	}

	out, _, err = executeCLI(t, "port-forward", "--help")
	if err != nil {
		t.Fatalf("port-forward --help failed: %v", err)
	}
	if !strings.Contains(out, "-n, --namespace") {
		t.Errorf("expected port-forward --help to inherit global -n, --namespace flag")
	}

	out, _, err = executeCLI(t, "logs", "--help")
	if err != nil {
		t.Fatalf("logs --help failed (must not panic on -f shorthand collision): %v", err)
	}
	if !strings.Contains(out, "--follow") {
		t.Errorf("expected logs --help to show --follow flag")
	}
}

func TestLoadSpec(t *testing.T) {
	t.Run("explicit config path", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		customPath := filepath.Join(dir, "custom-spec.yaml")
		if err := os.WriteFile(customPath, []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}
		appConfigFile = customPath

		spec, err := loadSpec()
		if err != nil {
			t.Fatalf("loadSpec failed: %v", err)
		}
		if spec.Name != "demo-service" {
			t.Errorf("expected spec.Name=demo-service, got %q", spec.Name)
		}
	})

	t.Run("auto-discovers skipper.yaml", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		spec, err := loadSpec()
		if err != nil {
			t.Fatalf("loadSpec failed: %v", err)
		}
		if spec.Name != "demo-service" {
			t.Errorf("expected spec.Name=demo-service, got %q", spec.Name)
		}
	})

	t.Run("falls back to app.yaml", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(sampleNonCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		spec, err := loadSpec()
		if err != nil {
			t.Fatalf("loadSpec failed: %v", err)
		}
		if spec.Name != "risky-app" {
			t.Errorf("expected spec.Name=risky-app, got %q", spec.Name)
		}
	})

	t.Run("prefers skipper.yaml over app.yaml", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(sampleNonCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		spec, err := loadSpec()
		if err != nil {
			t.Fatalf("loadSpec failed: %v", err)
		}
		if spec.Name != "demo-service" {
			t.Errorf("expected skipper.yaml to win (demo-service), got %q", spec.Name)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)

		_, err := loadSpec()
		if err == nil {
			t.Fatal("expected error when no config file exists")
		}
		if !strings.Contains(err.Error(), "unable to read") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("invalid YAML returns error", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte("name: [unclosed\n\tinvalid: :"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := loadSpec()
		if err == nil {
			t.Fatal("expected error for invalid YAML")
		}
		if !strings.Contains(err.Error(), "invalid") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestGetRuntimeAdapter(t *testing.T) {
	resetGlobals(t)

	targetRuntime = "docker"
	adapter := getRuntimeAdapter()
	if adapter.Name() != "docker" {
		t.Errorf("expected docker adapter, got %q", adapter.Name())
	}

	targetRuntime = "kubernetes"
	namespace = "prod"
	adapter = getRuntimeAdapter()
	if adapter.Name() != "kubernetes" {
		t.Errorf("expected kubernetes adapter, got %q", adapter.Name())
	}

	targetRuntime = ""
	adapter = getRuntimeAdapter()
	if adapter.Name() != "docker" {
		t.Errorf("expected default docker adapter, got %q", adapter.Name())
	}
}

func TestRenderDoctorReport(t *testing.T) {
	passingReport := &doctor.DoctorReport{
		Timestamp: time.Now(),
		Passed:    true,
		Checks: []doctor.DiagnosticCheck{
			{Name: "Docker Daemon", Status: doctor.StatusPass, Message: "Running"},
			{Name: "Kind CLI", Status: doctor.StatusWarn, Message: "Optional", Remediation: "Install kind"},
		},
	}

	failingReport := &doctor.DoctorReport{
		Timestamp: time.Now(),
		Passed:    false,
		Checks: []doctor.DiagnosticCheck{
			{Name: "Docker Daemon", Status: doctor.StatusFail, Message: "Not running", Remediation: "Start Docker Desktop"},
			{Name: "Port 8080", Status: doctor.StatusWarn, Message: "In use"},
		},
	}

	t.Run("text mode passed", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderDoctorReport(&buf, passingReport, "text")
		if err != nil {
			t.Fatalf("expected nil error for passing report, got %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "[✓]") || !strings.Contains(out, "[!]") || !strings.Contains(out, "All critical prerequisites satisfied!") {
			t.Errorf("unexpected text output: %s", out)
		}
	})

	t.Run("text mode failed", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderDoctorReport(&buf, failingReport, "text")
		if err == nil {
			t.Fatal("expected non-nil error for failing report in text mode")
		}
		out := buf.String()
		if !strings.Contains(out, "[✗]") || !strings.Contains(out, "-> Fix: Start Docker Desktop") {
			t.Errorf("unexpected text output: %s", out)
		}
	})

	t.Run("json mode passed", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderDoctorReport(&buf, passingReport, "json")
		if err != nil {
			t.Fatalf("expected nil error for passing report in json mode, got %v", err)
		}
		var decoded doctor.DoctorReport
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, buf.String())
		}
		if !decoded.Passed || len(decoded.Checks) != 2 {
			t.Errorf("unexpected decoded report: %+v", decoded)
		}
	})

	t.Run("json mode failed returns error without corrupting JSON stdout", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderDoctorReport(&buf, failingReport, "json")
		if err == nil {
			t.Fatal("expected non-nil error for failing report in json mode")
		}
		var decoded doctor.DoctorReport
		if jsonErr := json.Unmarshal(buf.Bytes(), &decoded); jsonErr != nil {
			t.Fatalf("JSON stdout was corrupted on failure: %v\noutput: %s", jsonErr, buf.String())
		}
		if decoded.Passed {
			t.Error("expected decoded report.Passed to be false")
		}
	})
}

func TestDoctorCmdExecution(t *testing.T) {
	resetGlobals(t)
	origRunDoctor := runDoctor
	t.Cleanup(func() { runDoctor = origRunDoctor })

	runDoctor = func(ctx context.Context) *doctor.DoctorReport {
		return &doctor.DoctorReport{
			Timestamp: time.Now(),
			Passed:    true,
			Checks: []doctor.DiagnosticCheck{
				{Name: "Docker Engine Daemon", Status: doctor.StatusPass, Message: "Running"},
			},
		}
	}

	out, _, err := executeCLI(t, "doctor", "--format", "json")
	if err != nil {
		t.Fatalf("doctor --format json returned unexpected error: %v", err)
	}
	var decoded doctor.DoctorReport
	if jsonErr := json.Unmarshal([]byte(out), &decoded); jsonErr != nil {
		t.Fatalf("doctor --format json produced invalid JSON: %v\noutput: %s", jsonErr, out)
	}
	if !decoded.Passed {
		t.Error("expected decoded.Passed == true")
	}

	resetGlobals(t)
	textOut, _, textErr := executeCLI(t, "doctor")
	if textErr != nil {
		t.Fatalf("doctor text mode returned unexpected error: %v", textErr)
	}
	if !strings.Contains(textOut, "Running Skipper environment health audit") {
		t.Errorf("unexpected doctor text output: %s", textOut)
	}
}

func TestExplainCmd(t *testing.T) {
	expectedCommands := []string{
		"skipper sbom",
		"skipper sign",
		"skipper debug <workload> --ephemeral",
		"skipper observe",
		"skipper gitops export",
		"skipper context",
		"skipper secret sync",
	}
	expectedMCPTools := []string{
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

	t.Run("default markdown output", func(t *testing.T) {
		resetGlobals(t)
		out, _, err := executeCLI(t, "explain")
		if err != nil {
			t.Fatalf("explain failed: %v", err)
		}
		if !strings.Contains(out, "# Skipper AI Agent Operational Specification") {
			t.Errorf("expected markdown header, got: %s", out)
		}
		for _, want := range expectedCommands {
			if !strings.Contains(out, want) {
				t.Errorf("markdown explain missing command %q", want)
			}
		}
		for _, tool := range expectedMCPTools {
			if !strings.Contains(out, tool) {
				t.Errorf("markdown explain missing MCP tool %q", tool)
			}
		}
	})

	t.Run("json output is valid structured JSON", func(t *testing.T) {
		resetGlobals(t)
		out, _, err := executeCLI(t, "explain", "--format", "json")
		if err != nil {
			t.Fatalf("explain --format json failed: %v", err)
		}
		var parsed explainSpec
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("explain --format json returned invalid JSON: %v\noutput: %s", err, out)
		}
		if parsed.Title == "" || len(parsed.Playbook) < 14 || len(parsed.RemediationLoop) == 0 {
			t.Errorf("expected populated explainSpec, got: %+v", parsed)
		}
		if len(parsed.MCPTools) != 10 {
			t.Errorf("expected 10 MCP tools in explainSpec, got %d: %v", len(parsed.MCPTools), parsed.MCPTools)
		}
		for _, want := range expectedCommands {
			if !strings.Contains(out, want) {
				t.Errorf("JSON explain missing command %q", want)
			}
		}
		for _, tool := range expectedMCPTools {
			if !strings.Contains(out, tool) {
				t.Errorf("JSON explain missing MCP tool %q", tool)
			}
		}
	})

	t.Run("schema works from temp directory via go:embed", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)

		out, _, err := executeCLI(t, "explain", "--schema")
		if err != nil {
			t.Fatalf("explain --schema failed outside repo root: %v", err)
		}
		var schemaMap map[string]interface{}
		if err := json.Unmarshal([]byte(out), &schemaMap); err != nil {
			t.Fatalf("explain --schema returned invalid JSON: %v", err)
		}
		if schemaMap["title"] != "SkipperIntentSpecification" {
			t.Errorf("unexpected schema title: %v", schemaMap["title"])
		}
	})
}

func TestSecretCmd(t *testing.T) {
	resetGlobals(t)
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	// Invalid format
	_, _, err := executeCLI(t, "secret", "set", "INVALID_NO_EQUALS")
	if err == nil || !strings.Contains(err.Error(), "expected format KEY=VALUE") {
		t.Fatalf("expected KEY=VALUE format error, got: %v", err)
	}

	// Set secret
	out, _, err := executeCLI(t, "secret", "set", "DB_PASS=s3cr3t=val")
	if err != nil {
		t.Fatalf("secret set failed: %v", err)
	}
	if !strings.Contains(out, "DB_PASS") {
		t.Errorf("unexpected secret set output: %s", out)
	}

	// Get secret
	out, _, err = executeCLI(t, "secret", "get", "DB_PASS")
	if err != nil {
		t.Fatalf("secret get failed: %v", err)
	}
	if strings.TrimSpace(out) != "DB_PASS=s3cr3t=val" {
		t.Errorf("expected DB_PASS=s3cr3t=val, got %q", out)
	}

	// Get missing secret
	_, _, err = executeCLI(t, "secret", "get", "DOES_NOT_EXIST")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}

	// Export Kubernetes Secret manifest with custom namespace
	out, _, err = executeCLI(t, "secret", "export", "-n", "my-ns")
	if err != nil {
		t.Fatalf("secret export failed: %v", err)
	}
	if !strings.Contains(out, "kind: Secret") || !strings.Contains(out, "namespace: my-ns") {
		t.Errorf("unexpected secret export manifest:\n%s", out)
	}
}

func TestManifestCmd(t *testing.T) {
	resetGlobals(t)
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
		t.Fatal(err)
	}

	// Stdout mode
	out, _, err := executeCLI(t, "manifest")
	if err != nil {
		t.Fatalf("manifest failed: %v", err)
	}
	if !strings.Contains(out, "kind: Deployment") || !strings.Contains(out, "name: demo-service") {
		t.Errorf("unexpected manifest output:\n%s", out)
	}

	// Output to file
	outFile := filepath.Join(dir, "out.yaml")
	out, _, err = executeCLI(t, "manifest", "-o", outFile)
	if err != nil {
		t.Fatalf("manifest -o failed: %v", err)
	}
	if !strings.Contains(out, outFile) {
		t.Errorf("expected confirmation mentioning %s, got %s", outFile, out)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read generated manifest file: %v", err)
	}
	if !strings.Contains(string(written), "kind: Deployment") {
		t.Errorf("written manifest missing Deployment:\n%s", string(written))
	}
}

func TestPolicyCheckCmd(t *testing.T) {
	t.Run("compliant text and json", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "policy-check")
		if err != nil {
			t.Fatalf("policy-check failed: %v", err)
		}
		if !strings.Contains(out, "POLICY AUDIT PASSED") {
			t.Errorf("expected POLICY AUDIT PASSED, got:\n%s", out)
		}

		resetGlobals(t)
		out, _, err = executeCLI(t, "policy-check", "--format", "json")
		if err != nil {
			t.Fatalf("policy-check --format json failed: %v", err)
		}
		var violations []governance.PolicyViolation
		if err := json.Unmarshal([]byte(out), &violations); err != nil {
			t.Fatalf("invalid JSON output: %v\noutput: %s", err, out)
		}
		if len(violations) != 0 {
			t.Errorf("expected 0 violations, got %d", len(violations))
		}
	})

	t.Run("non-compliant text and json", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleNonCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "policy-check")
		if err != nil {
			t.Fatalf("policy-check failed: %v", err)
		}
		if !strings.Contains(out, "POLICY AUDIT FINDINGS") {
			t.Errorf("expected POLICY AUDIT FINDINGS, got:\n%s", out)
		}

		resetGlobals(t)
		out, _, err = executeCLI(t, "policy-check", "--format", "json")
		if err != nil {
			t.Fatalf("policy-check --format json failed: %v", err)
		}
		var violations []governance.PolicyViolation
		if err := json.Unmarshal([]byte(out), &violations); err != nil {
			t.Fatalf("invalid JSON output: %v\noutput: %s", err, out)
		}
		if len(violations) == 0 {
			t.Error("expected at least 1 policy violation for non-compliant spec")
		}
	})
}

func TestImportComposeCmd(t *testing.T) {
	t.Run("rejects more than 1 arg", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)
		_, _, err := executeCLI(t, "import", "compose", "a.yaml", "b.yaml")
		if err == nil {
			t.Fatal("expected error when passing 2 args to import compose")
		}
	})

	t.Run("errors when compose file missing", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)
		_, _, err := executeCLI(t, "import", "compose")
		if err == nil || !strings.Contains(err.Error(), "unable to read compose file") {
			t.Fatalf("expected unable to read compose file error, got: %v", err)
		}
	})

	t.Run("transpiles explicit compose file", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		composeContent := `services:
  web:
    image: nginx:1.25
    ports:
      - "8080:80"
`
		composePath := filepath.Join(dir, "my-compose.yaml")
		if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "import", "compose", composePath)
		if err != nil {
			t.Fatalf("import compose failed: %v", err)
		}
		if !strings.Contains(out, "Successfully transpiled") {
			t.Errorf("unexpected output: %s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, "skipper.yaml")); err != nil {
			t.Fatalf("expected skipper.yaml to be generated: %v", err)
		}
	})

	t.Run("falls back to compose.yaml", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		composeContent := `services:
  api:
    image: golang:1.22
    ports:
      - "3000:3000"
`
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(composeContent), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "import", "compose")
		if err != nil {
			t.Fatalf("import compose fallback failed: %v", err)
		}
		if !strings.Contains(out, "compose.yaml") {
			t.Errorf("expected output to mention compose.yaml, got: %s", out)
		}
	})

	t.Run("finds docker-compose.yml, supports --service, and prints multi-service note", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		composeContent := `services:
  db:
    image: postgres:16
  backend:
    build: ./backend
    ports:
      - "8080:8080"
  frontend:
    build: ./frontend
    ports:
      - "3000:80"
`
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "import", "compose")
		if err != nil {
			t.Fatalf("import compose failed: %v", err)
		}
		if !strings.Contains(out, "docker-compose.yml") || !strings.Contains(out, "Service: backend") {
			t.Errorf("expected docker-compose.yml and deterministic buildable service backend, got:\n%s", out)
		}
		if !strings.Contains(out, "Compose file has 3 services") || !strings.Contains(out, "--service") {
			t.Errorf("expected multi-service note in output, got:\n%s", out)
		}

		// Explicit --service picks frontend
		out, _, err = executeCLI(t, "import", "compose", "--service", "frontend")
		if err != nil {
			t.Fatalf("import compose --service frontend failed: %v", err)
		}
		if !strings.Contains(out, "Service: frontend") {
			t.Errorf("expected Service: frontend, got:\n%s", out)
		}
		if strings.Contains(out, "Compose file has") {
			t.Errorf("did not expect multi-service note when --service is explicitly set, got:\n%s", out)
		}
	})
}

func TestSBOMCmd(t *testing.T) {
	resetGlobals(t)
	dir := chdirTemp(t)
	goMod := "module example.com/demo\n\ngo 1.25.0\n\nrequire gopkg.in/yaml.v3 v3.0.1\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	// Default CycloneDX to stdout
	out, _, err := executeCLI(t, "sbom", "--target", dir, "--sbom-format", "cyclonedx")
	if err != nil {
		t.Fatalf("sbom cyclonedx failed: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("sbom stdout is not valid JSON: %v\n%s", err, out)
	}

	// SPDX + --format json + --output
	resetGlobals(t)
	outFile := filepath.Join(dir, "spdx.json")
	out, _, err = executeCLI(t, "sbom", "--target", dir, "--sbom-format", "spdx", "--output", outFile, "--format", "json")
	if err != nil {
		t.Fatalf("sbom spdx json failed: %v", err)
	}
	if !strings.Contains(out, `"spdx-json"`) {
		t.Errorf("expected spdx-json in output: %s", out)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("expected SBOM written to %s: %v", outFile, err)
	}

	// Invalid format
	resetGlobals(t)
	_, _, err = executeCLI(t, "sbom", "--sbom-format", "unknown")
	if err == nil {
		t.Fatal("expected error for unsupported sbom-format")
	}
}

func TestSignCmd(t *testing.T) {
	resetGlobals(t)
	chdirTemp(t)

	// Missing image returns error
	_, _, err := executeCLI(t, "sign", "--dry-run")
	if err == nil {
		t.Fatal("expected error when no image is provided")
	}

	// Keyless dry-run sign
	out, _, err := executeCLI(t, "sign", "--image", "ghcr.io/acme/app:v1", "--dry-run")
	if err != nil {
		t.Fatalf("sign --dry-run failed: %v", err)
	}
	if !strings.Contains(out, "cosign sign") || !strings.Contains(out, "ghcr.io/acme/app:v1") {
		t.Errorf("unexpected sign output: %s", out)
	}

	// Keyed verify dry-run in JSON mode
	resetGlobals(t)
	out, _, err = executeCLI(t, "sign", "--image", "ghcr.io/acme/app:v1", "--key", "cosign.pub", "--verify", "--dry-run", "--format", "json")
	if err != nil {
		t.Fatalf("sign --verify --dry-run failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("sign --format json invalid JSON: %v\n%s", err, out)
	}
	if parsed["image"] != "ghcr.io/acme/app:v1" {
		t.Errorf("unexpected parsed sign result: %+v", parsed)
	}
}

func TestDebugEphemeralCmd(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	// Docker ephemeral dry-run
	resetGlobals(t)
	out, _, err := executeCLI(t, "debug", "api-svc", "--ephemeral", "--dry-run", "--debug-image", "busybox:latest")
	if err != nil {
		t.Fatalf("debug --ephemeral --dry-run failed: %v", err)
	}
	if !strings.Contains(out, "DRY-RUN") || !strings.Contains(out, "busybox:latest") {
		t.Errorf("unexpected ephemeral debug output:\n%s", out)
	}

	// Kubernetes ephemeral dry-run in JSON mode
	resetGlobals(t)
	out, _, err = executeCLI(t, "--runtime", "kubernetes", "-n", "prod", "--format", "json", "debug", "api-svc", "--ephemeral", "--copy-pod", "--share-processes", "--dry-run")
	if err != nil {
		t.Fatalf("kubernetes debug --ephemeral --dry-run failed: %v", err)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("debug --ephemeral --format json invalid JSON: %v\n%s", err, out)
	}
	if res["runtime"] != "kubernetes" || res["dryRun"] != true {
		t.Errorf("unexpected ephemeral result: %+v", res)
	}
}

func TestObserveCmd(t *testing.T) {
	t.Run("otel-manifest text and json", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		out, _, err := executeCLI(t, "observe", "--otel-manifest")
		if err != nil {
			t.Fatalf("observe --otel-manifest failed: %v", err)
		}
		if !strings.Contains(out, "OTEL_EXPORTER_OTLP_ENDPOINT") || !strings.Contains(out, "kind: ServiceMonitor") {
			t.Errorf("unexpected otel-manifest output:\n%s", out)
		}

		resetGlobals(t)
		out, _, err = executeCLI(t, "observe", "--otel-manifest", "--format", "json")
		if err != nil {
			t.Fatalf("observe --otel-manifest --format json failed: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
	})

	t.Run("prometheus health gate healthy and degraded", func(t *testing.T) {
		healthyVal := "0.001"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1700000000,"` + healthyVal + `"]}]}}`))
		}))
		defer srv.Close()

		resetGlobals(t)
		out, _, err := executeCLI(t, "observe", "demo-service", "--prometheus-url", srv.URL, "--format", "json")
		if err != nil {
			t.Fatalf("observe healthy failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, `"healthy": true`) {
			t.Errorf("expected healthy: true, got:\n%s", out)
		}

		// Trigger violation (value 0.50 exceeds max-error-rate 0.01)
		healthyVal = "0.50"
		resetGlobals(t)
		out, _, err = executeCLI(t, "observe", "demo-service", "--prometheus-url", srv.URL, "--max-error-rate", "0.01", "--format", "json")
		if err == nil {
			t.Fatal("expected error when health gate is violated")
		}
		var parsed map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
			t.Fatalf("expected valid JSON even on health gate failure: %v\n%s", jsonErr, out)
		}
	})
}

func TestGitOpsExportCmd(t *testing.T) {
	resetGlobals(t)
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
		t.Fatal(err)
	}

	// ArgoCD export to temp dir
	argoDir := filepath.Join(dir, "gitops-argo")
	out, _, err := executeCLI(t, "gitops", "export", "--engine", "argocd", "--repo", "https://github.com/acme/infra.git", "--branch", "main", "--env", "prod", "--output", argoDir)
	if err != nil {
		t.Fatalf("gitops export argocd failed: %v", err)
	}
	if !strings.Contains(out, "argocd") {
		t.Errorf("unexpected argocd export output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(argoDir, "argocd", "application.yaml")); err != nil {
		t.Fatalf("expected argocd/application.yaml to exist: %v", err)
	}

	// Flux export in JSON mode
	resetGlobals(t)
	fluxDir := filepath.Join(dir, "gitops-flux")
	out, _, err = executeCLI(t, "gitops", "export", "--engine", "flux", "--env", "staging", "--output", fluxDir, "--format", "json")
	if err != nil {
		t.Fatalf("gitops export flux failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("gitops export --format json invalid JSON: %v\n%s", err, out)
	}
	if parsed["engine"] != "flux" {
		t.Errorf("expected engine=flux, got %+v", parsed["engine"])
	}
}

func TestContextCmd(t *testing.T) {
	resetGlobals(t)
	dir := chdirTemp(t)

	// Help renders all subcommands
	out, _, err := executeCLI(t, "context", "--help")
	if err != nil {
		t.Fatalf("context --help failed: %v", err)
	}
	for _, sub := range []string{"list", "current", "use", "drift"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected context --help to list %q", sub)
		}
	}

	// `context use` requires 1 arg
	_, _, err = executeCLI(t, "context", "use")
	if err == nil {
		t.Fatal("expected error when context use has no arg")
	}

	// File-based drift detection (text and json)
	desired := filepath.Join(dir, "desired.yaml")
	live := filepath.Join(dir, "live.yaml")
	_ = os.WriteFile(desired, []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 3\n  template:\n    spec:\n      containers:\n        - name: api\n          image: nginx:1.25\n"), 0644)
	_ = os.WriteFile(live, []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 1\n  template:\n    spec:\n      containers:\n        - name: api\n          image: nginx:1.24\n"), 0644)

	out, _, err = executeCLI(t, "context", "drift", "--desired", desired, "--live", live)
	if err != nil {
		t.Fatalf("context drift failed: %v", err)
	}
	if !strings.Contains(out, "DRIFT DETECTED") {
		t.Errorf("expected DRIFT DETECTED, got:\n%s", out)
	}

	resetGlobals(t)
	out, _, err = executeCLI(t, "context", "drift", "--desired", desired, "--live", live, "--format", "json")
	if err != nil {
		t.Fatalf("context drift --format json failed: %v", err)
	}
	if !strings.Contains(out, `"hasDrift": true`) {
		t.Errorf("expected hasDrift: true in JSON:\n%s", out)
	}

	// Missing --live when --desired is specified
	resetGlobals(t)
	_, _, err = executeCLI(t, "context", "drift", "--desired", desired)
	if err == nil {
		t.Fatal("expected error when --live is missing")
	}
}

func TestSecretSyncCmd(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	// ExternalSecret manifest generation (Vault)
	resetGlobals(t)
	out, _, err := executeCLI(t, "secret", "sync", "--provider", "vault", "--vault-addr", "https://vault.example.com", "--vault-path", "apps/demo", "--external-secret")
	if err != nil {
		t.Fatalf("secret sync --external-secret failed: %v", err)
	}
	if !strings.Contains(out, "kind: SecretStore") || !strings.Contains(out, "kind: ExternalSecret") {
		t.Errorf("unexpected ExternalSecret manifest:\n%s", out)
	}

	// ExternalSecret JSON mode (AWS)
	resetGlobals(t)
	out, _, err = executeCLI(t, "secret", "sync", "--provider", "aws", "--aws-region", "us-west-2", "--external-secret", "--format", "json")
	if err != nil {
		t.Fatalf("secret sync aws json failed: %v", err)
	}
	if !strings.Contains(out, `"provider": "aws"`) {
		t.Errorf("unexpected AWS JSON output:\n%s", out)
	}

	// Push and Pull against mock Vault KV v2 HTTP server
	vaultData := map[string]interface{}{}
	vaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body struct {
				Data map[string]interface{} `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			vaultData = body.Data
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"version":1}}`))
			return
		}
		if r.Method == http.MethodGet {
			resp := map[string]interface{}{"data": map[string]interface{}{"data": vaultData}}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer vaultSrv.Close()

	// Seed local secret and push
	resetGlobals(t)
	_, _, err = executeCLI(t, "secret", "set", "API_KEY=top-secret-123")
	if err != nil {
		t.Fatalf("secret set failed: %v", err)
	}
	out, _, err = executeCLI(t, "secret", "sync", "--direction", "push", "--vault-addr", vaultSrv.URL, "--vault-token", "test-tok", "--vault-path", "demo/app")
	if err != nil {
		t.Fatalf("secret sync push failed: %v", err)
	}
	if !strings.Contains(out, "Pushed 1 secret") {
		t.Errorf("unexpected push output: %s", out)
	}

	// Pull back into fresh home dir
	freshHome := t.TempDir()
	t.Setenv("USERPROFILE", freshHome)
	t.Setenv("HOME", freshHome)
	resetGlobals(t)
	out, _, err = executeCLI(t, "secret", "sync", "--direction", "pull", "--vault-addr", vaultSrv.URL, "--vault-token", "test-tok", "--vault-path", "demo/app", "--format", "json")
	if err != nil {
		t.Fatalf("secret sync pull failed: %v", err)
	}
	if !strings.Contains(out, `"status": "SYNCED"`) {
		t.Errorf("unexpected pull JSON output: %s", out)
	}
	got, _, err := executeCLI(t, "secret", "get", "API_KEY")
	if err != nil || strings.TrimSpace(got) != "API_KEY=top-secret-123" {
		t.Fatalf("expected pulled secret API_KEY=top-secret-123, got %q (err=%v)", got, err)
	}

	// Invalid direction
	resetGlobals(t)
	_, _, err = executeCLI(t, "secret", "sync", "--direction", "bogus")
	if err == nil {
		t.Fatal("expected error for invalid sync direction")
	}
}

func TestRollbackCmd(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	// Missing workload without skipper.yaml
	resetGlobals(t)
	chdirTemp(t)
	_, _, err := executeCLI(t, "rollback", "--dry-run")
	if err == nil {
		t.Fatal("expected error when workload name is missing")
	}

	// Dry-run text mode
	resetGlobals(t)
	out, _, err := executeCLI(t, "rollback", "payments-api", "-n", "prod", "--revision", "5", "--context", "prod-east", "--reason", "canary error spike", "--dry-run")
	if err != nil {
		t.Fatalf("rollback --dry-run failed: %v", err)
	}
	if !strings.Contains(out, "kubectl --context prod-east rollout undo deployment/payments-api -n prod --to-revision=5") {
		t.Errorf("unexpected rollback output:\n%s", out)
	}

	// Dry-run JSON mode
	resetGlobals(t)
	out, _, err = executeCLI(t, "rollback", "payments-api", "--dry-run", "--format", "json")
	if err != nil {
		t.Fatalf("rollback --dry-run --format json failed: %v", err)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON from rollback: %v\n%s", err, out)
	}
	if res["status"] != "dry-run" || res["workload"] != "payments-api" {
		t.Errorf("unexpected rollback JSON: %+v", res)
	}
}

func TestRightsizeCmd(t *testing.T) {
	resetGlobals(t)
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
		t.Fatal(err)
	}

	// Baseline text mode
	out, _, err := executeCLI(t, "rightsize")
	if err != nil {
		t.Fatalf("rightsize failed: %v", err)
	}
	if !strings.Contains(out, "Source: baseline") || !strings.Contains(out, "demo-service") {
		t.Errorf("unexpected baseline rightsize output:\n%s", out)
	}

	// Live Prometheus JSON mode
	promSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		if strings.Contains(q, "cpu") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1700000000,"0.08"]}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1700000000,"67108864"]}]}}`))
		}
	}))
	defer promSrv.Close()

	resetGlobals(t)
	out, _, err = executeCLI(t, "rightsize", "--prometheus-url", promSrv.URL, "--format", "json")
	if err != nil {
		t.Fatalf("rightsize --prometheus-url failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON from rightsize: %v\n%s", err, out)
	}
	if parsed["dataSource"] != "prometheus" {
		t.Errorf("expected dataSource=prometheus, got %+v", parsed)
	}
}

func withFakeBuilder(t *testing.T, fn func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error) {
	t.Helper()
	orig := newCNBBuilder
	newCNBBuilder = func() *build.CNBBuilder {
		return build.NewCNBBuilderWithRunner(fn)
	}
	t.Cleanup(func() {
		newCNBBuilder = orig
	})
}

func setupThreeTierDir(t *testing.T) string {
	t.Helper()
	dir := chdirTemp(t)
	_ = os.MkdirAll(filepath.Join(dir, "frontend"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "backend"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "frontend", "Dockerfile"), []byte("FROM nginx:alpine\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "backend", "go.mod"), []byte("module example.com/backend\n\ngo 1.25\n"), 0644)

	composeYAML := `services:
  db:
    image: postgres:16-alpine
  backend:
    build: ./backend
    depends_on:
      - db
  frontend:
    build: ./frontend
    depends_on:
      - backend
`
	_ = os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeYAML), 0644)
	return dir
}

func TestBuildCmdMultiServiceModes(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	t.Run("compose auto mode builds buildable services and skips prebuilt image", func(t *testing.T) {
		resetGlobals(t)
		setupThreeTierDir(t)

		var builtTags []string
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-t" {
					builtTags = append(builtTags, args[i+1])
				}
			}
			return nil
		})

		out, _, err := executeCLI(t, "build")
		if err != nil {
			t.Fatalf("build in compose auto mode failed: %v\n%s", err, out)
		}
		if len(builtTags) != 2 {
			t.Fatalf("expected 2 docker builds (backend, frontend), got %v", builtTags)
		}
		if !strings.Contains(out, "db") || !strings.Contains(out, "skipped") {
			t.Errorf("expected summary table with skipped db service, got:\n%s", out)
		}
		if !strings.Contains(out, "backend") || !strings.Contains(out, "frontend") {
			t.Errorf("expected backend and frontend in summary, got:\n%s", out)
		}
	})

	t.Run("--service filter and unknown service error", func(t *testing.T) {
		resetGlobals(t)
		setupThreeTierDir(t)

		var builtTags []string
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-t" {
					builtTags = append(builtTags, args[i+1])
				}
			}
			return nil
		})

		out, _, err := executeCLI(t, "build", "--service", "backend", "--tag", "custom-api:v2")
		if err != nil {
			t.Fatalf("build --service backend --tag failed: %v\n%s", err, out)
		}
		if len(builtTags) != 1 || builtTags[0] != "custom-api:v2" {
			t.Fatalf("expected single build tagged custom-api:v2, got %v", builtTags)
		}

		// Unknown service lists available services
		resetGlobals(t)
		_, _, err = executeCLI(t, "build", "--service", "nonexistent")
		if err == nil || !strings.Contains(err.Error(), "unknown service") || !strings.Contains(err.Error(), "backend") {
			t.Fatalf("expected unknown service error listing available services, got: %v", err)
		}
	})

	t.Run("discover mode without compose or skipper.yaml", func(t *testing.T) {
		resetGlobals(t)
		dir := setupThreeTierDir(t)
		_ = os.Remove(filepath.Join(dir, "docker-compose.yml"))

		var builtTags []string
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-t" {
					builtTags = append(builtTags, args[i+1])
				}
			}
			return nil
		})

		out, _, err := executeCLI(t, "build")
		if err != nil {
			t.Fatalf("discover mode build failed: %v\n%s", err, out)
		}
		if len(builtTags) != 2 {
			t.Fatalf("expected 2 discovered builds, got %v", builtTags)
		}
		if !strings.Contains(out, "Discovered 2 service(s)") {
			t.Errorf("expected discovery header in output, got:\n%s", out)
		}

		// Empty dir returns clear guidance error
		resetGlobals(t)
		chdirTemp(t)
		_, _, err = executeCLI(t, "build")
		if err == nil || !strings.Contains(err.Error(), "nothing to build") {
			t.Fatalf("expected nothing to build error, got: %v", err)
		}
	})

	t.Run("--tag with multiple targets and --save in compose mode return errors", func(t *testing.T) {
		resetGlobals(t)
		setupThreeTierDir(t)
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			return nil
		})

		_, _, err := executeCLI(t, "build", "--tag", "one-tag:latest")
		if err == nil || !strings.Contains(err.Error(), "--tag can only be used when building a single service") {
			t.Fatalf("expected --tag multi-target error, got: %v", err)
		}

		resetGlobals(t)
		_, _, err = executeCLI(t, "build", "--save")
		if err == nil || !strings.Contains(err.Error(), "--save is not supported") {
			t.Fatalf("expected --save unsupported error, got: %v", err)
		}
	})

	t.Run("json output parses cleanly", func(t *testing.T) {
		resetGlobals(t)
		setupThreeTierDir(t)
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			return nil
		})

		out, _, err := executeCLI(t, "build", "--format", "json")
		if err != nil {
			t.Fatalf("build --format json failed: %v\n%s", err, out)
		}
		var payload struct {
			Project string                     `json:"project"`
			Mode    string                     `json:"mode"`
			Results []build.ServiceBuildResult `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("build --format json returned invalid JSON: %v\noutput:\n%s", err, out)
		}
		if payload.Mode != "compose" || len(payload.Results) != 3 {
			t.Errorf("unexpected JSON payload: %+v", payload)
		}
	})

	t.Run("failing service returns non-zero error while building others", func(t *testing.T) {
		resetGlobals(t)
		setupThreeTierDir(t)
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			if strings.Contains(strings.Join(args, " "), "backend") {
				return errors.New("compile failed")
			}
			return nil
		})

		out, _, err := executeCLI(t, "build")
		if err == nil || !strings.Contains(err.Error(), "1 of 3 services failed to build") {
			t.Fatalf("expected '1 of 3 services failed to build' error, got: %v", err)
		}
		if !strings.Contains(out, "frontend") || !strings.Contains(out, "built") {
			t.Errorf("expected frontend to still build after backend failure, got:\n%s", out)
		}
	})

	t.Run("single-service skipper.yaml behavior unchanged and rejects --service", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0644)
		_ = os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644)

		var builtTag string
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-t" {
					builtTag = args[i+1]
				}
			}
			return nil
		})

		out, _, err := executeCLI(t, "build")
		if err != nil {
			t.Fatalf("single-service build failed: %v\n%s", err, out)
		}
		if builtTag != "ghcr.io/acme/demo-service:1.2.3" {
			t.Errorf("expected single-service tag ghcr.io/acme/demo-service:1.2.3, got %q", builtTag)
		}

		resetGlobals(t)
		_, _, err = executeCLI(t, "build", "--service", "web")
		if err == nil || !strings.Contains(err.Error(), "--service requires compose or discover mode") {
			t.Fatalf("expected --service requires compose or discover mode error, got: %v", err)
		}
	})
}

type stubRuntimeEngine struct {
	name      string
	initErr   error
	deployErr error
	statusErr error
	stopErr   error
	logsErr   error
	debugCode int
	debugErr  error
	logText   string
}

func (s *stubRuntimeEngine) Name() string {
	if s.name == "" {
		return "stub-docker"
	}
	return s.name
}

func (s *stubRuntimeEngine) Initialize(ctx context.Context) error {
	return s.initErr
}

func (s *stubRuntimeEngine) Deploy(ctx context.Context, spec *config.AppSpec) (*runtime.WorkloadStatus, error) {
	if s.deployErr != nil {
		return nil, s.deployErr
	}
	return &runtime.WorkloadStatus{
		Name:      spec.Name,
		Target:    s.Name(),
		State:     "Running",
		Replicas:  1,
		Available: 1,
		CreatedAt: time.Now(),
	}, nil
}

func (s *stubRuntimeEngine) Status(ctx context.Context, workloadName string) (*runtime.WorkloadStatus, error) {
	if s.statusErr != nil {
		return nil, s.statusErr
	}
	return &runtime.WorkloadStatus{
		Name:      workloadName,
		Target:    s.Name(),
		State:     "Running",
		Replicas:  2,
		Available: 2,
		CreatedAt: time.Now(),
	}, nil
}

func (s *stubRuntimeEngine) Stop(ctx context.Context, workloadName string) error {
	return s.stopErr
}

func (s *stubRuntimeEngine) Logs(ctx context.Context, workloadName string, opts runtime.LogOptions, out io.Writer) error {
	if s.logsErr != nil {
		return s.logsErr
	}
	_, _ = io.WriteString(out, s.logText)
	return nil
}

func (s *stubRuntimeEngine) Debug(ctx context.Context, opts runtime.DebugOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	_, _ = io.WriteString(stdout, "debug-attached\n")
	return s.debugCode, s.debugErr
}

func withStubRuntime(t *testing.T, eng runtime.RuntimeEngine) {
	t.Helper()
	orig := runtimeAdapterFactory
	runtimeAdapterFactory = func() runtime.RuntimeEngine { return eng }
	t.Cleanup(func() { runtimeAdapterFactory = orig })
}

func TestRuntimeLifecycleCmds(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	stub := &stubRuntimeEngine{name: "docker", logText: "line1\nline2\n"}
	withStubRuntime(t, stub)

	t.Run("missing workload errors for status, logs, down, debug", func(t *testing.T) {
		for _, sub := range []string{"status", "logs", "down", "debug"} {
			resetGlobals(t)
			chdirTemp(t)
			_, _, err := executeCLI(t, sub)
			if err == nil || !strings.Contains(err.Error(), "workload name required") {
				t.Fatalf("expected workload name required for %q, got: %v", sub, err)
			}
		}
	})

	t.Run("up, status, logs, down, debug text and strict JSON", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		// up text + json
		out, _, err := executeCLI(t, "up", "-i", "demo-service:v2")
		if err != nil || !strings.Contains(out, "deployed successfully") {
			t.Fatalf("up text failed: err=%v out=%s", err, out)
		}
		resetGlobals(t)
		out, _, err = executeCLI(t, "up", "--format", "json")
		if err != nil {
			t.Fatalf("up --format json failed: %v", err)
		}
		var upStatus runtime.WorkloadStatus
		if err := json.Unmarshal([]byte(out), &upStatus); err != nil || upStatus.Name != "demo-service" {
			t.Fatalf("up --format json invalid JSON: err=%v out=%s", err, out)
		}

		// status text + json
		resetGlobals(t)
		out, _, err = executeCLI(t, "status")
		if err != nil || !strings.Contains(out, "Workload: demo-service") {
			t.Fatalf("status text failed: err=%v out=%s", err, out)
		}
		resetGlobals(t)
		out, _, err = executeCLI(t, "status", "demo-service", "--format", "json")
		if err != nil {
			t.Fatalf("status --format json failed: %v", err)
		}
		var st runtime.WorkloadStatus
		if err := json.Unmarshal([]byte(out), &st); err != nil || st.Replicas != 2 {
			t.Fatalf("status --format json invalid JSON: err=%v out=%s", err, out)
		}

		// logs
		resetGlobals(t)
		out, _, err = executeCLI(t, "logs", "--tail", "10")
		if err != nil || !strings.Contains(out, "line1") {
			t.Fatalf("logs failed: err=%v out=%s", err, out)
		}

		// down text + json
		resetGlobals(t)
		out, _, err = executeCLI(t, "down")
		if err != nil || !strings.Contains(out, "stopped on target") {
			t.Fatalf("down text failed: err=%v out=%s", err, out)
		}
		resetGlobals(t)
		out, _, err = executeCLI(t, "down", "demo-service", "--format", "json")
		if err != nil {
			t.Fatalf("down --format json failed: %v", err)
		}
		var downRes map[string]string
		if err := json.Unmarshal([]byte(out), &downRes); err != nil || downRes["status"] != "STOPPED" {
			t.Fatalf("down --format json invalid JSON: err=%v out=%s", err, out)
		}

		// debug (non-ephemeral)
		resetGlobals(t)
		out, _, err = executeCLI(t, "debug", "demo-service")
		if err != nil || !strings.Contains(out, "Debug session preserved") {
			t.Fatalf("debug failed: err=%v out=%s", err, out)
		}
	})

	t.Run("deploy policy block and compliant JSON", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleNonCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCLI(t, "deploy")
		if err == nil || !strings.Contains(err.Error(), "halted by platform governance policy") {
			t.Fatalf("expected governance block on deploy, got: %v", err)
		}
	})
}

func TestScanInitDiagnoseTimeTravelCmds(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	t.Run("init text, json, and already-exists error", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/initdemo\n\ngo 1.25\n"), 0644)

		out, _, err := executeCLI(t, "init", "--name", "init-svc", "--team", "core", "--port", "9000", "--format", "json")
		if err != nil {
			t.Fatalf("init --format json failed: %v", err)
		}
		var initPayload map[string]interface{}
		if err := json.Unmarshal([]byte(out), &initPayload); err != nil {
			t.Fatalf("init --format json invalid JSON: %v\n%s", err, out)
		}

		// Second run fails because skipper.yaml already exists
		resetGlobals(t)
		_, _, err = executeCLI(t, "init")
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected already exists error on duplicate init, got: %v", err)
		}
	})

	t.Run("scan missing image error and text/json output", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)
		_, _, err := executeCLI(t, "scan")
		if err == nil || !strings.Contains(err.Error(), "image name required") {
			t.Fatalf("expected image name required error, got: %v", err)
		}

		resetGlobals(t)
		out, _, err := executeCLI(t, "scan", "nginx:1.25", "--format", "json")
		if err != nil {
			t.Fatalf("scan --format json failed: %v", err)
		}
		var scanRes map[string]interface{}
		if err := json.Unmarshal([]byte(out), &scanRes); err != nil {
			t.Fatalf("scan --format json invalid JSON: %v\n%s", err, out)
		}
	})

	t.Run("diagnose missing workload and strict JSON hygiene", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)
		_, _, err := executeCLI(t, "diagnose")
		if err == nil || !strings.Contains(err.Error(), "workload name required") {
			t.Fatalf("expected workload name required error, got: %v", err)
		}

		resetGlobals(t)
		out, _, err := executeCLI(t, "diagnose", "demo-app", "--format", "json")
		if err != nil {
			t.Fatalf("diagnose --format json failed: %v", err)
		}
		var findings []diagnostics.DiagnosticFinding
		if err := json.Unmarshal([]byte(out), &findings); err != nil {
			t.Fatalf("diagnose --format json corrupted stdout (must be pure JSON array): %v\noutput:\n%s", err, out)
		}
	})

	t.Run("time-travel text and json with recorded events", func(t *testing.T) {
		resetGlobals(t)
		chdirTemp(t)

		// Seed an event in opmem
		if ledger, err := opmem.NewOperationalMemoryLedger(""); err == nil {
			_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
				Timestamp:   time.Now(),
				Type:        "MUTATION",
				Source:      "test",
				Description: "Deployed demo-app",
				Severity:    "INFO",
			})
		}

		out, _, err := executeCLI(t, "time-travel", "demo-app", "--window", "1h")
		if err != nil || !strings.Contains(out, "Deployed demo-app") {
			t.Fatalf("time-travel text failed: err=%v out=%s", err, out)
		}

		resetGlobals(t)
		out, _, err = executeCLI(t, "time-travel", "demo-app", "--window", "invalid-duration", "--format", "json")
		if err != nil {
			t.Fatalf("time-travel --format json failed: %v", err)
		}
		var events []opmem.CausalEvent
		if err := json.Unmarshal([]byte(out), &events); err != nil || len(events) == 0 {
			t.Fatalf("time-travel --format json invalid JSON: err=%v out=%s", err, out)
		}
	})
}

func TestJSONHygieneAndEnvContracts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("HOME", tmpHome)

	t.Run("manifest, import compose, build --save, and secret JSON hygiene", func(t *testing.T) {
		resetGlobals(t)
		dir := chdirTemp(t)
		if err := os.WriteFile(filepath.Join(dir, "skipper.yaml"), []byte(sampleCompliantSpec), 0644); err != nil {
			t.Fatal(err)
		}

		// manifest --format json
		out, _, err := executeCLI(t, "manifest", "--format", "json")
		if err != nil {
			t.Fatalf("manifest --format json failed: %v", err)
		}
		var mf map[string]interface{}
		if err := json.Unmarshal([]byte(out), &mf); err != nil {
			t.Fatalf("manifest --format json invalid JSON: %v\n%s", err, out)
		}

		// single-service build --save --format json
		_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0644)
		withFakeBuilder(t, func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
			if name == "pack" {
				return errors.New("no pack")
			}
			_, _ = io.WriteString(stdout, "docker build progress line\n")
			return nil
		})
		resetGlobals(t)
		out, _, err = executeCLI(t, "build", "-t", "demo-service:v9.9.9", "--save", "--format", "json")
		if err != nil {
			t.Fatalf("build --save --format json failed: %v", err)
		}
		var buildJSON map[string]interface{}
		if err := json.Unmarshal([]byte(out), &buildJSON); err != nil || buildJSON["image"] != "demo-service:v9.9.9" {
			t.Fatalf("build --save --format json corrupted stdout: err=%v\nout=%s", err, out)
		}

		// secret set/get/export --format json and empty key rejection
		resetGlobals(t)
		_, _, err = executeCLI(t, "secret", "set", "=emptykey")
		if err == nil {
			t.Fatal("expected error for empty secret key '=emptykey'")
		}
		out, _, err = executeCLI(t, "secret", "set", "TOKEN_K=val123", "--format", "json")
		if err != nil || json.Unmarshal([]byte(out), &map[string]interface{}{}) != nil {
			t.Fatalf("secret set --format json failed: err=%v out=%s", err, out)
		}
		resetGlobals(t)
		out, _, err = executeCLI(t, "secret", "get", "TOKEN_K", "--format", "json")
		if err != nil || json.Unmarshal([]byte(out), &map[string]interface{}{}) != nil {
			t.Fatalf("secret get --format json failed: err=%v out=%s", err, out)
		}
		resetGlobals(t)
		out, _, err = executeCLI(t, "secret", "export", "--format", "json")
		if err != nil || json.Unmarshal([]byte(out), &map[string]interface{}{}) != nil {
			t.Fatalf("secret export --format json failed: err=%v out=%s", err, out)
		}

		// import compose --format json
		composePath := filepath.Join(dir, "docker-compose.yaml")
		_ = os.WriteFile(composePath, []byte("services:\n  api:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n"), 0644)
		resetGlobals(t)
		out, _, err = executeCLI(t, "import", "compose", composePath, "--format", "json")
		if err != nil || json.Unmarshal([]byte(out), &map[string]interface{}{}) != nil {
			t.Fatalf("import compose --format json failed: err=%v out=%s", err, out)
		}
	})

	t.Run("VAULT_ADDR and VAULT_TOKEN environment variable fallback in secret sync", func(t *testing.T) {
		var gotToken string
		vaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotToken = r.Header.Get("X-Vault-Token")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"data":{"ENV_SECRET":"from-vault"}}}`))
		}))
		defer vaultSrv.Close()

		t.Setenv("VAULT_ADDR", vaultSrv.URL)
		t.Setenv("VAULT_TOKEN", "env-fallback-token")

		resetGlobals(t)
		out, _, err := executeCLI(t, "secret", "sync", "--direction", "pull", "--vault-path", "apps/demo", "--format", "json")
		if err != nil {
			t.Fatalf("secret sync pull with VAULT_ADDR/VAULT_TOKEN env fallback failed: %v", err)
		}
		if gotToken != "env-fallback-token" {
			t.Errorf("expected X-Vault-Token=env-fallback-token from env, got %q", gotToken)
		}
		if json.Unmarshal([]byte(out), &map[string]interface{}{}) != nil {
			t.Fatalf("invalid JSON from secret sync pull: %s", out)
		}
	})

	t.Run("invalid flags, unknown subcommands, and port-forward arg contracts", func(t *testing.T) {
		resetGlobals(t)
		if _, _, err := executeCLI(t, "--nonexistent-flag"); err == nil {
			t.Fatal("expected error for unknown root flag")
		}
		resetGlobals(t)
		if _, _, err := executeCLI(t, "nonexistent-command"); err == nil {
			t.Fatal("expected error for unknown subcommand")
		}
		resetGlobals(t)
		if _, _, err := executeCLI(t, "port-forward", "only-one-arg"); err == nil {
			t.Fatal("expected error for port-forward with 1 arg")
		}
		resetGlobals(t)
		if _, _, err := executeCLI(t, "port-forward", "svc", "invalid-port"); err == nil {
			t.Fatal("expected error for port-forward with invalid port format")
		}
	})
}
