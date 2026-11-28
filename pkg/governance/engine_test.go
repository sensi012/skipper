package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
)

// TestGovernanceHelperProcess is invoked as a subprocess to mock kubectl top commands.
func TestGovernanceHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GOV_HELPER_PROCESS") != "1" {
		return
	}
	if out := os.Getenv("GOV_MOCK_STDOUT"); out != "" {
		_, _ = fmt.Fprint(os.Stdout, out)
	}
	if errOut := os.Getenv("GOV_MOCK_STDERR"); errOut != "" {
		_, _ = fmt.Fprint(os.Stderr, errOut)
	}
	if os.Getenv("GOV_MOCK_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func mockGovExecCommand(stdout, stderr string, fail bool, captured *[]string) func(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if captured != nil {
			*captured = append([]string{name}, arg...)
		}
		cs := []string{"-test.run=TestGovernanceHelperProcess", "--", name}
		cs = append(cs, arg...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		env := append(os.Environ(),
			"GO_WANT_GOV_HELPER_PROCESS=1",
			"GOV_MOCK_STDOUT="+stdout,
			"GOV_MOCK_STDERR="+stderr,
		)
		if fail {
			env = append(env, "GOV_MOCK_EXIT=1")
		}
		cmd.Env = env
		return cmd
	}
}

func TestEvaluatePolicies(t *testing.T) {
	gov := NewGovernanceEngine()

	t.Run("NilSpecReturnsError", func(t *testing.T) {
		if _, err := gov.EvaluatePolicies(nil); err == nil {
			t.Fatal("expected error for nil spec")
		}
	})

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
	if analysis.DataSource != DataSourceBaseline {
		t.Fatalf("expected DataSource %q, got %q", DataSourceBaseline, analysis.DataSource)
	}
	if analysis.Namespace != "default" {
		t.Fatalf("expected default namespace, got %q", analysis.Namespace)
	}
}

func TestAnalyzeRightsizingWithOptions_NilSpec(t *testing.T) {
	gov := NewGovernanceEngine()
	if _, err := gov.AnalyzeRightsizingWithOptions(context.Background(), nil, RightsizingOptions{}); err == nil {
		t.Fatalf("expected error for nil spec")
	}
}

func TestAnalyzeRightsizingWithOptions_Prometheus(t *testing.T) {
	var seenQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("query")
		seenQueries = append(seenQueries, q)

		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "container_cpu_usage_seconds_total") {
			// 0.20 cores = 200m -> recommended = 250m
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result": []map[string]interface{}{
						{"metric": map[string]string{}, "value": []interface{}{1700000000.0, "0.2"}},
					},
				},
			})
			return
		}
		if strings.Contains(q, "container_memory_working_set_bytes") {
			// 200 MiB -> recommended = 250Mi
			bytes := 200 * 1024 * 1024
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result": []map[string]interface{}{
						{"metric": map[string]string{}, "value": []interface{}{1700000000.0, fmt.Sprintf("%d", bytes)}},
					},
				},
			})
			return
		}
		http.Error(w, "unexpected query", http.StatusBadRequest)
	}))
	defer srv.Close()

	gov := NewGovernanceEngine()
	spec := &config.AppSpec{
		Name: "payments-api",
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Limit: "1000m"},
				Memory: config.ResourceLimit{Limit: "1Gi"},
			},
		},
	}

	res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), spec, RightsizingOptions{
		Namespace:     "prod",
		PrometheusURL: srv.URL,
		HTTPClient:    srv.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataSource != DataSourcePrometheus {
		t.Fatalf("expected DataSource %q, got %q", DataSourcePrometheus, res.DataSource)
	}
	if res.Namespace != "prod" {
		t.Fatalf("expected namespace prod, got %q", res.Namespace)
	}
	if res.ActualP95CPU != "200m" || res.RecommendedCPU != "250m" {
		t.Fatalf("unexpected CPU analysis: actual=%s rec=%s", res.ActualP95CPU, res.RecommendedCPU)
	}
	if res.ActualP95Memory != "200Mi" || res.RecommendedMem != "250Mi" {
		t.Fatalf("unexpected Memory analysis: actual=%s rec=%s", res.ActualP95Memory, res.RecommendedMem)
	}
	if !res.Overprovisioned || res.MonthlySavings <= 0 {
		t.Fatalf("expected overprovisioned with positive savings, got %+v", res)
	}
	if len(seenQueries) != 2 {
		t.Fatalf("expected 2 Prometheus queries, got %d", len(seenQueries))
	}
}

func TestAnalyzeRightsizingWithOptions_PrometheusFloorsAndScalar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "container_cpu_usage_seconds_total") {
			// 0.01 cores = 10m -> floor 50m
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "scalar",
					"result":     []interface{}{1700000000.0, "0.01"},
				},
			})
			return
		}
		// 10 MiB -> floor 64Mi
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "scalar",
				"result":     []interface{}{1700000000.0, float64(10 * 1024 * 1024)},
			},
		})
	}))
	defer srv.Close()

	gov := NewGovernanceEngine()
	spec := &config.AppSpec{Name: "tiny-svc"}

	res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), spec, RightsizingOptions{
		PrometheusURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataSource != DataSourcePrometheus {
		t.Fatalf("expected prometheus source, got %q", res.DataSource)
	}
	if res.RecommendedCPU != "50m" {
		t.Fatalf("expected CPU floor 50m, got %s", res.RecommendedCPU)
	}
	if res.RecommendedMem != "64Mi" {
		t.Fatalf("expected memory floor 64Mi, got %s", res.RecommendedMem)
	}
}

func TestAnalyzeRightsizingWithOptions_PrometheusFallback(t *testing.T) {
	t.Run("HTTP500FallsBackToBaseline", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		gov := NewGovernanceEngine()
		res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), &config.AppSpec{Name: "api"}, RightsizingOptions{
			PrometheusURL: srv.URL,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DataSource != DataSourceBaseline {
			t.Fatalf("expected baseline fallback, got %q", res.DataSource)
		}
	})

	t.Run("EmptyVectorFallsBackToKubectlTop", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer srv.Close()

		gov := NewGovernanceEngine()
		gov.execCommand = mockGovExecCommand("api-abc 100m 128Mi\n", "", false, nil)

		res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), &config.AppSpec{Name: "api"}, RightsizingOptions{
			Namespace:     "staging",
			PrometheusURL: srv.URL,
			UseKubectlTop: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DataSource != DataSourceKubectlTop {
			t.Fatalf("expected kubectl-top fallback, got %q", res.DataSource)
		}
		if res.ActualP95CPU != "100m" || res.RecommendedCPU != "125m" {
			t.Fatalf("unexpected CPU: %+v", res)
		}
		if res.ActualP95Memory != "128Mi" || res.RecommendedMem != "160Mi" {
			t.Fatalf("unexpected Memory: %+v", res)
		}
	})
}

func TestAnalyzeRightsizingWithOptions_KubectlTop(t *testing.T) {
	t.Run("MultiPodSum", func(t *testing.T) {
		var captured []string
		gov := NewGovernanceEngine()
		gov.execCommand = mockGovExecCommand(
			"web-7f8b-1   120m   256Mi\nweb-7f8b-2   80m    144Mi\n",
			"",
			false,
			&captured,
		)

		spec := &config.AppSpec{
			Name: "web",
			Runtime: config.RuntimeConfig{
				Resources: config.ResourceConfig{
					CPU:    config.ResourceLimit{Limit: "1"},
					Memory: config.ResourceLimit{Limit: "1Gi"},
				},
			},
		}

		res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), spec, RightsizingOptions{
			Namespace:     "prod",
			UseKubectlTop: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantCmd := "kubectl top pod -l app=web -n prod --no-headers"
		if gotCmd := strings.Join(captured, " "); gotCmd != wantCmd {
			t.Fatalf("expected command %q, got %q", wantCmd, gotCmd)
		}
		if res.DataSource != DataSourceKubectlTop {
			t.Fatalf("expected DataSource %q, got %q", DataSourceKubectlTop, res.DataSource)
		}
		if res.ActualP95CPU != "200m" || res.RecommendedCPU != "250m" {
			t.Fatalf("unexpected CPU: actual=%s rec=%s", res.ActualP95CPU, res.RecommendedCPU)
		}
		if res.ActualP95Memory != "400Mi" || res.RecommendedMem != "500Mi" {
			t.Fatalf("unexpected Memory: actual=%s rec=%s", res.ActualP95Memory, res.RecommendedMem)
		}
		if !res.Overprovisioned || res.MonthlySavings <= 0 {
			t.Fatalf("expected overprovisioned with savings, got %+v", res)
		}
	})

	t.Run("KubectlErrorFallsBackToBaseline", func(t *testing.T) {
		gov := NewGovernanceEngine()
		gov.execCommand = mockGovExecCommand("", "metrics API not available", true, nil)

		res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), &config.AppSpec{Name: "web"}, RightsizingOptions{
			UseKubectlTop: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DataSource != DataSourceBaseline {
			t.Fatalf("expected baseline fallback, got %q", res.DataSource)
		}
	})

	t.Run("EmptyOutputFallsBackToBaseline", func(t *testing.T) {
		gov := NewGovernanceEngine()
		gov.execCommand = mockGovExecCommand("   \n", "", false, nil)

		res, err := gov.AnalyzeRightsizingWithOptions(context.Background(), &config.AppSpec{Name: "web"}, RightsizingOptions{
			UseKubectlTop: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DataSource != DataSourceBaseline {
			t.Fatalf("expected baseline fallback, got %q", res.DataSource)
		}
	})
}

func TestResourceParsers(t *testing.T) {
	cpuCases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"500m", 500, false},
		{"1", 1000, false},
		{"0.25", 250, false},
		{"1000000n", 1, false},
		{"", 0, true},
		{"bad", 0, true},
	}
	for _, tc := range cpuCases {
		got, err := parseCPUMillicores(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseCPUMillicores(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseCPUMillicores(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	memCases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"512Mi", 512, false},
		{"512m", 512, false},
		{"512mb", 512, false},
		{"1g", 1024, false},
		{"1GiB", 1024, false},
		{"2Gi", 2048, false},
		{"1024Ki", 1, false},
		{"1048576", 1, false},
		{"", 0, true},
		{"badMi", 0, true},
	}
	for _, tc := range memCases {
		got, err := parseMemoryMiB(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseMemoryMiB(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseMemoryMiB(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
