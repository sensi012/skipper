package governance

import (
	"context"
	"testing"

	"github.com/unified-platform/container/pkg/config"
)

func TestEvaluatePolicies(t *testing.T) {
	gov := NewGovernanceEngine()

	t.Run("AllPassing", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "order-service",
			Team: "orders",
			Security: config.SecurityConfig{
				RunAsNonRoot: true,
			},
			Runtime: config.RuntimeConfig{
				Resources: config.ResourceConfig{
					CPU:    config.ResourceLimit{Limit: "500m"},
					Memory: config.ResourceLimit{Limit: "512Mi"},
				},
			},
		}
		violations, err := gov.EvaluatePolicies(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(violations) != 0 {
			t.Fatalf("expected 0 violations, got %d", len(violations))
		}
	})

	t.Run("MissingTeam", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "order-service",
			Security: config.SecurityConfig{
				RunAsNonRoot: true,
			},
			Runtime: config.RuntimeConfig{
				Resources: config.ResourceConfig{
					CPU:    config.ResourceLimit{Limit: "500m"},
					Memory: config.ResourceLimit{Limit: "512Mi"},
				},
			},
		}
		violations, err := gov.EvaluatePolicies(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(violations) != 1 || violations[0].RuleName != "require-team-ownership" {
			t.Fatalf("expected require-team-ownership violation")
		}
	})

	t.Run("DisallowRootContainers", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "order-service",
			Team: "orders",
			Security: config.SecurityConfig{
				RunAsNonRoot: false,
			},
			Runtime: config.RuntimeConfig{
				Resources: config.ResourceConfig{
					CPU:    config.ResourceLimit{Limit: "500m"},
					Memory: config.ResourceLimit{Limit: "512Mi"},
				},
			},
		}
		violations, err := gov.EvaluatePolicies(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(violations) != 1 || violations[0].RuleName != "disallow-root-containers" {
			t.Fatalf("expected disallow-root-containers violation")
		}
	})

	t.Run("MissingLimitsWarn", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "order-service",
			Team: "orders",
			Security: config.SecurityConfig{
				RunAsNonRoot: true,
			},
		}
		violations, err := gov.EvaluatePolicies(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(violations) != 1 || violations[0].Severity != "WARN" {
			t.Fatalf("expected WARN severity for missing limits")
		}
	})
}

func TestAnalyzeRightsizing(t *testing.T) {
	gov := NewGovernanceEngine()
	spec := &config.AppSpec{
		Name: "heavy-app",
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Limit: "1000m"},
				Memory: config.ResourceLimit{Limit: "2Gi"},
			},
		},
	}

	analysis, err := gov.AnalyzeRightsizing(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !analysis.Overprovisioned {
		t.Fatalf("expected Overprovisioned true")
	}
	if analysis.MonthlySavings <= 0 {
		t.Fatalf("expected positive monthly savings")
	}
}
