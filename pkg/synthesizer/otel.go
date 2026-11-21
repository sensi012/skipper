package synthesizer

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/sensi012/skipper/pkg/config"
)

// OTelOptions configures OpenTelemetry instrumentation and Prometheus ServiceMonitor generation.
type OTelOptions struct {
	Namespace       string
	OTLPEndpoint    string
	ServiceName     string
	MetricsPort     int
	MetricsPortName string
	MetricsPath     string
	ScrapeInterval  string
	ScrapeTimeout   string
	Protocol        string
}

func resolveOTelOptions(spec *config.AppSpec, opts OTelOptions) OTelOptions {
	resolved := opts
	if resolved.Namespace == "" {
		resolved.Namespace = "default"
	}
	if resolved.OTLPEndpoint == "" {
		resolved.OTLPEndpoint = "http://otel-collector.monitoring.svc.cluster.local:4317"
	}
	if resolved.ServiceName == "" {
		resolved.ServiceName = spec.Name
	}
	if resolved.MetricsPort <= 0 {
		if len(spec.Endpoints) > 0 && spec.Endpoints[0].Port > 0 {
			resolved.MetricsPort = spec.Endpoints[0].Port
		} else {
			resolved.MetricsPort = 9090
		}
	}
	if resolved.MetricsPortName == "" {
		if len(spec.Endpoints) > 0 && spec.Endpoints[0].Name != "" {
			resolved.MetricsPortName = spec.Endpoints[0].Name
		} else {
			resolved.MetricsPortName = "metrics"
		}
	}
	if resolved.MetricsPath == "" {
		resolved.MetricsPath = "/metrics"
	}
	if resolved.ScrapeInterval == "" {
		resolved.ScrapeInterval = "15s"
	}
	if resolved.ScrapeTimeout == "" {
		resolved.ScrapeTimeout = "10s"
	}
	if resolved.Protocol == "" {
		resolved.Protocol = "grpc"
	}
	return resolved
}

// OTelEnvVars returns the OpenTelemetry environment variables for container instrumentation.
func OTelEnvVars(spec *config.AppSpec, opts OTelOptions) []config.EnvVar {
	if spec == nil {
		return nil
	}
	resolved := resolveOTelOptions(spec, opts)
	env := spec.Environment
	if env == "" {
		env = "production"
	}
	return []config.EnvVar{
		{Name: "OTEL_SERVICE_NAME", Value: resolved.ServiceName},
		{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: resolved.OTLPEndpoint},
		{Name: "OTEL_EXPORTER_OTLP_PROTOCOL", Value: resolved.Protocol},
		{Name: "OTEL_RESOURCE_ATTRIBUTES", Value: fmt.Sprintf("service.name=%s,deployment.environment=%s", resolved.ServiceName, env)},
	}
}

// PrometheusAnnotations returns standard Prometheus pod scrape annotations.
func PrometheusAnnotations(spec *config.AppSpec, opts OTelOptions) map[string]string {
	if spec == nil {
		return nil
	}
	resolved := resolveOTelOptions(spec, opts)
	return map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/port":   strconv.Itoa(resolved.MetricsPort),
		"prometheus.io/path":   resolved.MetricsPath,
	}
}

// SynthesizeOTelConfig generates a multi-document YAML containing:
//  1. A Deployment strategic-merge patch adding OpenTelemetry environment variables
//     and Prometheus scrape annotations.
//  2. A Prometheus ServiceMonitor (monitoring.coreos.com/v1) manifest.
func SynthesizeOTelConfig(spec *config.AppSpec, opts OTelOptions) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("app spec must not be nil")
	}
	if spec.Name == "" {
		return "", fmt.Errorf("app spec name must not be empty")
	}

	resolved := resolveOTelOptions(spec, opts)
	envVars := OTelEnvVars(spec, resolved)

	var buf bytes.Buffer

	// Document 1: Deployment patch with OTel env vars and Prometheus pod annotations
	buf.WriteString("apiVersion: apps/v1\n")
	buf.WriteString("kind: Deployment\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n", spec.Name))
	buf.WriteString(fmt.Sprintf("  namespace: %s\n", resolved.Namespace))
	buf.WriteString("spec:\n")
	buf.WriteString("  template:\n")
	buf.WriteString("    metadata:\n")
	buf.WriteString("      annotations:\n")
	buf.WriteString("        prometheus.io/scrape: \"true\"\n")
	buf.WriteString(fmt.Sprintf("        prometheus.io/port: %q\n", strconv.Itoa(resolved.MetricsPort)))
	buf.WriteString(fmt.Sprintf("        prometheus.io/path: %q\n", resolved.MetricsPath))
	buf.WriteString("    spec:\n")
	buf.WriteString("      containers:\n")
	buf.WriteString(fmt.Sprintf("      - name: %s\n", spec.Name))
	buf.WriteString("        env:\n")
	for _, ev := range envVars {
		buf.WriteString(fmt.Sprintf("        - name: %s\n", ev.Name))
		buf.WriteString(fmt.Sprintf("          value: %q\n", ev.Value))
	}

	buf.WriteString("\n---\n")

	// Document 2: Prometheus Operator ServiceMonitor
	buf.WriteString("apiVersion: monitoring.coreos.com/v1\n")
	buf.WriteString("kind: ServiceMonitor\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-monitor\n", spec.Name))
	buf.WriteString(fmt.Sprintf("  namespace: %s\n", resolved.Namespace))
	buf.WriteString("  labels:\n")
	buf.WriteString(fmt.Sprintf("    app: %s\n", spec.Name))
	buf.WriteString("    release: prometheus\n")
	buf.WriteString("spec:\n")
	buf.WriteString("  selector:\n")
	buf.WriteString("    matchLabels:\n")
	buf.WriteString(fmt.Sprintf("      app: %s\n", spec.Name))
	buf.WriteString("  namespaceSelector:\n")
	buf.WriteString("    matchNames:\n")
	buf.WriteString(fmt.Sprintf("    - %s\n", resolved.Namespace))
	buf.WriteString("  endpoints:\n")
	buf.WriteString(fmt.Sprintf("  - port: %s\n", resolved.MetricsPortName))
	buf.WriteString(fmt.Sprintf("    path: %s\n", resolved.MetricsPath))
	buf.WriteString(fmt.Sprintf("    interval: %s\n", resolved.ScrapeInterval))
	buf.WriteString(fmt.Sprintf("    scrapeTimeout: %s\n", resolved.ScrapeTimeout))

	return buf.String(), nil
}
