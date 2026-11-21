package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/diagnostics"
	"github.com/sensi012/skipper/pkg/governance"
	"github.com/sensi012/skipper/pkg/opmem"
	"github.com/sensi012/skipper/pkg/synthesizer"
)

func TestEndToEndPlatformPipeline(t *testing.T) {
	ctx := context.Background()

	// 1. Spec Parsing & Intent Validation
	spec := &config.AppSpec{
		Version:     "v1alpha1",
		Name:        "e2e-service",
		Team:        "infrastructure-core",
		Environment: "production",
		Type:        "service",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080, Public: true},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot:           true,
			ReadOnlyRootFilesystem: true,
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "100m", Limit: "500m"},
				Memory: config.ResourceLimit{Request: "128Mi", Limit: "512Mi"},
			},
		},
	}

	// 2. Policy-as-Code Compliance Verification
	gov := governance.NewGovernanceEngine()
	violations, err := gov.EvaluatePolicies(spec)
	if err != nil {
		t.Fatalf("Policy evaluation error: %v", err)
	}
	for _, v := range violations {
		if v.Severity == "BLOCK" {
			t.Fatalf("Unexpected blocking policy violation: %s - %s", v.RuleName, v.Message)
		}
	}

	// 3. Production Manifest Synthesis Verification
	synth := synthesizer.NewSynthesizer()
	bundle, err := synth.Synthesize(spec, "production")
	if err != nil {
		t.Fatalf("Manifest synthesis failed: %v", err)
	}

	if !strings.Contains(bundle.Deployment, "runAsNonRoot: true") {
		t.Errorf("Synthesized deployment missing hardened securityContext")
	}
	if !strings.Contains(bundle.NetworkPolicy, "NetworkPolicy") {
		t.Errorf("Synthesized bundle missing zero-trust NetworkPolicy")
	}
	if !strings.Contains(bundle.PodDisruption, "PodDisruptionBudget") {
		t.Errorf("Synthesized bundle missing PodDisruptionBudget")
	}

	// 4. Operational Memory & Timeline Verification
	tempDir := t.TempDir()
	ledger, err := opmem.NewOperationalMemoryLedger(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize operational memory: %v", err)
	}

	err = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
		Timestamp:   time.Now(),
		Type:        "MUTATION",
		Source:      "e2e-test",
		Description: "Promoted e2e-service to v1.2",
		Severity:    "INFO",
	})
	if err != nil {
		t.Fatalf("Failed to record telemetry event: %v", err)
	}

	events, err := ledger.QueryTimeline("e2e-service", 10*time.Minute)
	if err != nil || len(events) == 0 {
		t.Fatalf("Failed to retrieve timeline from operational memory: %v", err)
	}

	// 5. Deterministic Diagnostics Simulation (Chaos Injection)
	matrix := diagnostics.NewDeterministicMatrix(ledger)

	// Simulate OOMKilled condition
	oomLogs := "kernel: [12345.67] Out of memory: Killed process 42 (app) exit code 137"
	findings, err := matrix.Diagnose(ctx, "production", "e2e-service", oomLogs)
	if err != nil {
		t.Fatalf("Diagnostic run failed: %v", err)
	}

	if len(findings) == 0 || findings[0].DetectorName != "OOMKilledDetector" {
		t.Fatalf("Expected OOMKilledDetector finding, got: %v", findings)
	}

	// 6. FinOps Rightsizing Analysis Verification
	rightsizing, err := gov.AnalyzeRightsizing(ctx, spec)
	if err != nil {
		t.Fatalf("FinOps analysis failed: %v", err)
	}

	if !rightsizing.Overprovisioned || rightsizing.MonthlySavings <= 0 {
		t.Errorf("Expected overprovisioning savings to be detected, got: %+v", rightsizing)
	}
}
