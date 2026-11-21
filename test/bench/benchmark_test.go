package bench_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/diagnostics"
	"github.com/sensi012/skipper/pkg/governance"
	"github.com/sensi012/skipper/pkg/opmem"
	"github.com/sensi012/skipper/pkg/synthesizer"
)

func BenchmarkManifestSynthesis(b *testing.B) {
	synth := synthesizer.NewSynthesizer()
	spec := &config.AppSpec{
		Name: "benchmark-service",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "100m", Limit: "500m"},
				Memory: config.ResourceLimit{Request: "128Mi", Limit: "512Mi"},
			},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot:           true,
			ReadOnlyRootFilesystem: true,
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = synth.Synthesize(spec, "production")
	}
}

func BenchmarkPolicyEvaluation(b *testing.B) {
	gov := governance.NewGovernanceEngine()
	spec := &config.AppSpec{
		Name: "benchmark-service",
		Team: "core-platform",
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Limit: "500m"},
				Memory: config.ResourceLimit{Limit: "512Mi"},
			},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot: true,
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = gov.EvaluatePolicies(spec)
	}
}

func BenchmarkDiagnosticMatrix(b *testing.B) {
	dir := b.TempDir()
	ledger, _ := opmem.NewOperationalMemoryLedger(dir)
	matrix := diagnostics.NewDeterministicMatrix(ledger)
	logs := "kernel: [12345.67] Out of memory: Killed process 42 (app) exit code 137"
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = matrix.Diagnose(ctx, "production", "benchmark-service", logs)
	}
}

func BenchmarkOperationalMemoryWrite(b *testing.B) {
	dir := b.TempDir()
	ledger, _ := opmem.NewOperationalMemoryLedger(dir)
	evt := &opmem.CausalEvent{
		Timestamp:   time.Now(),
		Type:        "MUTATION",
		Source:      "bench",
		Description: "Deployed v1.0.0",
		Severity:    "INFO",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ledger.RecordTelemetryEvent(evt)
	}
}

func BenchmarkOperationalMemoryQuery(b *testing.B) {
	dir := b.TempDir()
	ledger, _ := opmem.NewOperationalMemoryLedger(dir)
	// Seed 500 events
	for i := 0; i < 500; i++ {
		_ = ledger.RecordTelemetryEvent(&opmem.CausalEvent{
			Timestamp:   time.Now(),
			Type:        "SYSTEM_SIGNAL",
			Source:      "bench-seeder",
			Description: "Benchmark metric event",
			Severity:    "INFO",
		})
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ledger.QueryTimeline("bench-service", 1*time.Hour)
	}
}

func BenchmarkLanguageDetection(b *testing.B) {
	dir := b.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	builder := build.NewCNBBuilder()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = builder.Detect(ctx, dir)
	}
}
