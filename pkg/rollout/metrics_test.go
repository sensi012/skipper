package rollout

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/config"
)

func newMockPrometheusServer(t *testing.T, errorRate, p95Ms, saturation string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("query")
		val := "0"
		switch {
		case strings.Contains(q, "http_requests_total"):
			val = errorRate
		case strings.Contains(q, "http_request_duration_seconds_bucket"):
			val = p95Ms
		case strings.Contains(q, "container_cpu_usage_seconds_total"):
			val = saturation
		}

		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"app": "checkout"},
						"value":  []interface{}{1700000000, val},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestPrometheusHealthGateHealthy(t *testing.T) {
	srv := newMockPrometheusServer(t, "0.002", "120.5", "0.45")
	defer srv.Close()

	gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
	report, err := gate.Evaluate(context.Background(), "checkout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("expected healthy report, got violations: %v", report.Violations)
	}
	if len(report.Metrics) != 3 {
		t.Fatalf("expected 3 metric samples, got %d", len(report.Metrics))
	}
	if len(report.Violations) != 0 {
		t.Errorf("expected no violations, got %v", report.Violations)
	}
	for _, m := range report.Metrics {
		if !m.Healthy {
			t.Errorf("expected metric %s to be healthy, got %+v", m.Name, m)
		}
	}
}

func TestPrometheusHealthGateViolations(t *testing.T) {
	srv := newMockPrometheusServer(t, "0.05", "820.0", "0.95")
	defer srv.Close()

	gate := NewPrometheusHealthGate(srv.URL, GateThresholds{
		MaxErrorRate:    0.01,
		MaxP95LatencyMs: 500,
		MaxSaturation:   0.85,
	})
	report, err := gate.Evaluate(context.Background(), "checkout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Healthy {
		t.Fatalf("expected unhealthy report")
	}
	if len(report.Violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %v", len(report.Violations), report.Violations)
	}
	joined := strings.Join(report.Violations, " | ")
	for _, expected := range []string{"error_rate", "p95_latency_ms", "saturation"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected violation mentioning %q, got: %s", expected, joined)
		}
	}
}

func TestPrometheusHealthGateUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := srv.URL
	srv.Close() // immediately close so connection is refused

	gate := NewPrometheusHealthGate(badURL, DefaultGateThresholds())
	_, err := gate.Evaluate(context.Background(), "checkout")
	if err == nil {
		t.Fatal("expected error for unreachable endpoint, got nil")
	}
}

func TestPrometheusHealthGateAPIErrorsAndEdgeCases(t *testing.T) {
	t.Run("http 500 error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		if _, err := gate.Evaluate(context.Background(), "api"); err == nil {
			t.Fatal("expected error on HTTP 500")
		}
	})

	t.Run("prometheus error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
		}))
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		if _, err := gate.QueryScalar(context.Background(), "bad("); err == nil {
			t.Fatal("expected error on prometheus error status")
		}
	})

	t.Run("scalar result and auth header", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"0.25"]}}`))
		}))
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		gate.AuthToken = "secret-token"
		val, err := gate.QueryScalar(context.Background(), "scalar(0.25)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != 0.25 {
			t.Errorf("expected 0.25, got %v", val)
		}
		if gotAuth != "Bearer secret-token" {
			t.Errorf("expected Bearer auth header, got %q", gotAuth)
		}
	})

	t.Run("empty vector and NaN treated as zero", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("query")
			if strings.Contains(q, "nan") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"NaN"]}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		v1, err := gate.QueryScalar(context.Background(), "empty_metric")
		if err != nil || v1 != 0 {
			t.Fatalf("expected (0, nil) for empty vector, got (%v, %v)", v1, err)
		}
		v2, err := gate.QueryScalar(context.Background(), "nan_metric")
		if err != nil || v2 != 0 {
			t.Fatalf("expected (0, nil) for NaN, got (%v, %v)", v2, err)
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		gate := &PrometheusHealthGate{}
		if _, err := gate.QueryScalar(context.Background(), "up"); err == nil {
			t.Error("expected error for empty endpoint")
		}
		if _, err := gate.Evaluate(context.Background(), ""); err == nil {
			t.Error("expected error for empty workload")
		}
	})
}

func TestPrometheusHealthGateWithRolloutController(t *testing.T) {
	t.Run("promotes canary when metrics healthy", func(t *testing.T) {
		srv := newMockPrometheusServer(t, "0.001", "85", "0.30")
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		ctrl := NewController(&mockRuntimeEngine{state: "Running"})
		ctrl.SetHealthCheck(gate.AsHealthCheck())

		var buf bytes.Buffer
		status, err := ctrl.ExecuteCanary(context.Background(), &config.AppSpec{Name: "checkout"}, RolloutConfig{
			Strategy:     StrategyCanary,
			CanarySteps:  []int{25, 100},
			StepInterval: 1 * time.Millisecond,
			AutoRollback: true,
		}, &buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.Phase != "Promoted" || status.CurrentWeight != 100 {
			t.Fatalf("expected Promoted at 100%%, got %+v", status)
		}
	})

	t.Run("triggers auto-rollback when metrics violate threshold", func(t *testing.T) {
		srv := newMockPrometheusServer(t, "0.15", "900", "0.50")
		defer srv.Close()

		gate := NewPrometheusHealthGate(srv.URL, DefaultGateThresholds())
		ctrl := NewController(&mockRuntimeEngine{state: "Running"})
		ctrl.SetHealthCheck(gate.AsHealthCheck())

		var buf bytes.Buffer
		status, err := ctrl.ExecuteCanary(context.Background(), &config.AppSpec{Name: "checkout"}, RolloutConfig{
			Strategy:     StrategyCanary,
			CanarySteps:  []int{10, 50, 100},
			StepInterval: 1 * time.Millisecond,
			AutoRollback: true,
		}, &buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.Phase != "RolledBack" || status.CurrentWeight != 0 {
			t.Fatalf("expected RolledBack at 0%%, got %+v", status)
		}
		if !strings.Contains(status.Message, "SLO violation") {
			t.Errorf("expected SLO violation in message, got: %s", status.Message)
		}
	})

	t.Run("fails rollout when prometheus unreachable and auto-rollback disabled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		badURL := srv.URL
		srv.Close()

		gate := NewPrometheusHealthGate(badURL, DefaultGateThresholds())
		ctrl := NewController(&mockRuntimeEngine{state: "Running"})
		ctrl.SetHealthCheck(gate.AsHealthCheck())

		var buf bytes.Buffer
		status, err := ctrl.ExecuteCanary(context.Background(), &config.AppSpec{Name: "checkout"}, RolloutConfig{
			Strategy:     StrategyCanary,
			CanarySteps:  []int{10, 100},
			StepInterval: 1 * time.Millisecond,
			AutoRollback: false,
		}, &buf)
		if err == nil {
			t.Fatal("expected error when prometheus unreachable and AutoRollback=false")
		}
		if status.Phase != "Failed" {
			t.Fatalf("expected Failed phase, got %+v", status)
		}
	})
}
