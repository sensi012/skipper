package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sensi012/skipper/pkg/config"
)

// GateThresholds defines acceptable SLO thresholds for Prometheus/OTel canary health gates.
type GateThresholds struct {
	MaxErrorRate    float64 `json:"maxErrorRate"`    // Error ratio (e.g. 0.01 = 1%)
	MaxP95LatencyMs float64 `json:"maxP95LatencyMs"` // P95 latency in milliseconds (e.g. 500)
	MaxSaturation   float64 `json:"maxSaturation"`   // Resource saturation ratio (e.g. 0.85 = 85%)
}

// DefaultGateThresholds returns sensible production SLO defaults.
func DefaultGateThresholds() GateThresholds {
	return GateThresholds{
		MaxErrorRate:    0.01,
		MaxP95LatencyMs: 500.0,
		MaxSaturation:   0.85,
	}
}

// MetricSample holds the evaluated result of a single Prometheus query.
type MetricSample struct {
	Name      string  `json:"name"`
	Query     string  `json:"query"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"`
	Healthy   bool    `json:"healthy"`
}

// ObservationReport is the structured health evaluation returned by PrometheusHealthGate.
type ObservationReport struct {
	Workload   string         `json:"workload"`
	Endpoint   string         `json:"endpoint"`
	Timestamp  time.Time      `json:"timestamp"`
	Metrics    []MetricSample `json:"metrics"`
	Healthy    bool           `json:"healthy"`
	Violations []string       `json:"violations,omitempty"`
}

// PrometheusHealthGate queries a Prometheus/OTel HTTP API (/api/v1/query) to gate canary rollouts.
type PrometheusHealthGate struct {
	Endpoint   string
	Thresholds GateThresholds
	HTTPClient *http.Client
	AuthToken  string
}

// NewPrometheusHealthGate creates a health gate backed by a Prometheus HTTP API endpoint.
func NewPrometheusHealthGate(endpoint string, thresholds GateThresholds) *PrometheusHealthGate {
	defaults := DefaultGateThresholds()
	if thresholds.MaxErrorRate <= 0 {
		thresholds.MaxErrorRate = defaults.MaxErrorRate
	}
	if thresholds.MaxP95LatencyMs <= 0 {
		thresholds.MaxP95LatencyMs = defaults.MaxP95LatencyMs
	}
	if thresholds.MaxSaturation <= 0 {
		thresholds.MaxSaturation = defaults.MaxSaturation
	}
	return &PrometheusHealthGate{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		Thresholds: thresholds,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

type promAPIResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type promVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}

// QueryScalar executes an instant PromQL query against /api/v1/query and extracts a single float64 value.
func (g *PrometheusHealthGate) QueryScalar(ctx context.Context, promQL string) (float64, error) {
	if g == nil {
		return 0, fmt.Errorf("prometheus health gate must not be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(g.Endpoint) == "" {
		return 0, fmt.Errorf("prometheus endpoint must not be empty")
	}
	client := g.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && client.Timeout == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	reqURL := fmt.Sprintf("%s/api/v1/query?query=%s", strings.TrimRight(g.Endpoint, "/"), url.QueryEscape(promQL))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to build prometheus request: %w", err)
	}
	if g.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+g.AuthToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("prometheus query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return 0, fmt.Errorf("failed to read prometheus response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed promAPIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("failed to decode prometheus response: %w", err)
	}
	if parsed.Status != "success" {
		return 0, fmt.Errorf("prometheus query error (%s): %s", parsed.ErrorType, parsed.Error)
	}

	switch parsed.Data.ResultType {
	case "vector":
		var samples []promVectorSample
		if err := json.Unmarshal(parsed.Data.Result, &samples); err != nil {
			return 0, fmt.Errorf("failed to parse vector result: %w", err)
		}
		if len(samples) == 0 {
			return 0, nil
		}
		return parsePromValue(samples[0].Value)
	case "scalar":
		var pair []interface{}
		if err := json.Unmarshal(parsed.Data.Result, &pair); err != nil {
			return 0, fmt.Errorf("failed to parse scalar result: %w", err)
		}
		return parsePromValue(pair)
	default:
		return 0, fmt.Errorf("unsupported prometheus resultType %q", parsed.Data.ResultType)
	}
}

func parsePromValue(pair []interface{}) (float64, error) {
	if len(pair) < 2 {
		return 0, fmt.Errorf("malformed prometheus sample value")
	}
	var raw string
	switch v := pair[1].(type) {
	case string:
		raw = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, nil
		}
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected prometheus value type %T", pair[1])
	}

	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float value %q: %w", raw, err)
	}
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, nil
	}
	return val, nil
}

// Evaluate queries error rate, p95 latency (ms), and saturation for the given workload
// and checks each against GateThresholds.
func (g *PrometheusHealthGate) Evaluate(ctx context.Context, workload string) (*ObservationReport, error) {
	if strings.TrimSpace(workload) == "" {
		return nil, fmt.Errorf("workload name must not be empty")
	}

	thresholds := g.Thresholds
	defaults := DefaultGateThresholds()
	if thresholds.MaxErrorRate <= 0 {
		thresholds.MaxErrorRate = defaults.MaxErrorRate
	}
	if thresholds.MaxP95LatencyMs <= 0 {
		thresholds.MaxP95LatencyMs = defaults.MaxP95LatencyMs
	}
	if thresholds.MaxSaturation <= 0 {
		thresholds.MaxSaturation = defaults.MaxSaturation
	}

	checks := []struct {
		name      string
		query     string
		threshold float64
		unit      string
	}{
		{
			name:      "error_rate",
			query:     fmt.Sprintf(`sum(rate(http_requests_total{app=%q,status=~"5.."}[5m])) / clamp_min(sum(rate(http_requests_total{app=%q}[5m])), 1)`, workload, workload),
			threshold: thresholds.MaxErrorRate,
			unit:      "ratio",
		},
		{
			name:      "p95_latency_ms",
			query:     fmt.Sprintf(`histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{app=%q}[5m])) by (le)) * 1000`, workload),
			threshold: thresholds.MaxP95LatencyMs,
			unit:      "ms",
		},
		{
			name:      "saturation",
			query:     fmt.Sprintf(`avg(container_cpu_usage_seconds_total{app=%q})`, workload),
			threshold: thresholds.MaxSaturation,
			unit:      "ratio",
		},
	}

	report := &ObservationReport{
		Workload:  workload,
		Endpoint:  g.Endpoint,
		Timestamp: time.Now().UTC(),
		Metrics:   make([]MetricSample, 0, len(checks)),
		Healthy:   true,
	}

	for _, chk := range checks {
		val, err := g.QueryScalar(ctx, chk.query)
		if err != nil {
			return nil, fmt.Errorf("metric %s: %w", chk.name, err)
		}

		healthy := val <= chk.threshold
		sample := MetricSample{
			Name:      chk.name,
			Query:     chk.query,
			Value:     val,
			Threshold: chk.threshold,
			Unit:      chk.unit,
			Healthy:   healthy,
		}
		report.Metrics = append(report.Metrics, sample)
		if !healthy {
			report.Healthy = false
			report.Violations = append(report.Violations,
				fmt.Sprintf("%s=%.4f%s exceeds threshold %.4f%s", chk.name, val, chk.unit, chk.threshold, chk.unit))
		}
	}

	return report, nil
}

// AsHealthCheck returns a HealthCheckFunc compatible with Controller.HealthCheck
// so progressive canary rollouts can gate on live Prometheus/OTel metrics.
func (g *PrometheusHealthGate) AsHealthCheck() HealthCheckFunc {
	return func(ctx context.Context, spec *config.AppSpec, cfg RolloutConfig) error {
		if spec == nil {
			return fmt.Errorf("app spec must not be nil")
		}
		report, err := g.Evaluate(ctx, spec.Name)
		if err != nil {
			return fmt.Errorf("prometheus health gate unreachable: %w", err)
		}
		if !report.Healthy {
			return fmt.Errorf("SLO violation: %s", strings.Join(report.Violations, "; "))
		}
		return nil
	}
}
