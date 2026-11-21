package synthesizer

import (
	"strings"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

func TestSynthesizeOTelConfigDefaults(t *testing.T) {
	spec := &config.AppSpec{
		Name:        "payments-api",
		Environment: "staging",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
	}

	out, err := SynthesizeOTelConfig(spec, OTelOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	docs := strings.Split(out, "\n---\n")
	if len(docs) != 2 {
		t.Fatalf("expected 2 YAML documents, got %d", len(docs))
	}

	// Verify both documents parse as valid YAML
	for i, doc := range docs {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			t.Fatalf("document %d is invalid YAML: %v\n%s", i, err, doc)
		}
	}

	requiredSnippets := []string{
		"kind: Deployment",
		"namespace: default",
		`prometheus.io/scrape: "true"`,
		`prometheus.io/port: "8080"`,
		`prometheus.io/path: "/metrics"`,
		"name: OTEL_SERVICE_NAME",
		`value: "payments-api"`,
		"name: OTEL_EXPORTER_OTLP_ENDPOINT",
		`value: "http://otel-collector.monitoring.svc.cluster.local:4317"`,
		"apiVersion: monitoring.coreos.com/v1",
		"kind: ServiceMonitor",
		"name: payments-api-monitor",
		"port: http",
		"path: /metrics",
		"interval: 15s",
	}
	for _, s := range requiredSnippets {
		if !strings.Contains(out, s) {
			t.Errorf("expected output to contain %q, got:\n%s", s, out)
		}
	}
}

func TestSynthesizeOTelConfigCustomOptions(t *testing.T) {
	spec := &config.AppSpec{
		Name: "orders-svc",
	}

	opts := OTelOptions{
		Namespace:       "prod",
		OTLPEndpoint:    "https://otel.internal:4318",
		ServiceName:     "orders-prod",
		MetricsPort:     9100,
		MetricsPortName: "prom-metrics",
		MetricsPath:     "/custom/metrics",
		ScrapeInterval:  "30s",
		ScrapeTimeout:   "15s",
		Protocol:        "http/protobuf",
	}

	out, err := SynthesizeOTelConfig(spec, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"namespace: prod",
		`prometheus.io/port: "9100"`,
		`prometheus.io/path: "/custom/metrics"`,
		`value: "https://otel.internal:4318"`,
		`value: "orders-prod"`,
		`value: "http/protobuf"`,
		"port: prom-metrics",
		"path: /custom/metrics",
		"interval: 30s",
		"scrapeTimeout: 15s",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in output:\n%s", c, out)
		}
	}
}

func TestSynthesizeOTelConfigFallbackPortNoEndpoints(t *testing.T) {
	spec := &config.AppSpec{Name: "batch-worker"}
	out, err := SynthesizeOTelConfig(spec, OTelOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `prometheus.io/port: "9090"`) {
		t.Errorf("expected fallback metrics port 9090, got:\n%s", out)
	}
	if !strings.Contains(out, "port: metrics") {
		t.Errorf("expected fallback metrics port name 'metrics', got:\n%s", out)
	}
}

func TestSynthesizeOTelConfigValidationErrors(t *testing.T) {
	if _, err := SynthesizeOTelConfig(nil, OTelOptions{}); err == nil {
		t.Error("expected error for nil spec")
	}
	if _, err := SynthesizeOTelConfig(&config.AppSpec{}, OTelOptions{}); err == nil {
		t.Error("expected error for empty spec name")
	}
}

func TestOTelHelpersNilSpec(t *testing.T) {
	if env := OTelEnvVars(nil, OTelOptions{}); env != nil {
		t.Errorf("expected nil env vars for nil spec, got %v", env)
	}
	if ann := PrometheusAnnotations(nil, OTelOptions{}); ann != nil {
		t.Errorf("expected nil annotations for nil spec, got %v", ann)
	}

	spec := &config.AppSpec{Name: "demo"}
	ann := PrometheusAnnotations(spec, OTelOptions{MetricsPort: 8081, MetricsPath: "/stats"})
	if ann["prometheus.io/scrape"] != "true" || ann["prometheus.io/port"] != "8081" || ann["prometheus.io/path"] != "/stats" {
		t.Errorf("unexpected annotations: %+v", ann)
	}
}
