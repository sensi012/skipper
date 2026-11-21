package synthesizer

import (
	"strings"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
)

func TestSynthesizeBasicDeployment(t *testing.T) {
	synth := NewSynthesizer()
	spec := &config.AppSpec{
		Name: "worker-svc",
		Runtime: config.RuntimeConfig{
			Replicas: config.ReplicaConfig{Min: 3},
		},
	}

	bundle, err := synth.Synthesize(spec, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(bundle.Deployment, "kind: Deployment") {
		t.Errorf("expected Deployment in bundle")
	}
	if !strings.Contains(bundle.Deployment, "namespace: staging") {
		t.Errorf("expected namespace staging")
	}
	if !strings.Contains(bundle.Deployment, "replicas: 3") {
		t.Errorf("expected replicas: 3")
	}
	if !strings.Contains(bundle.Deployment, "runAsNonRoot: true") {
		t.Errorf("expected hardened security context")
	}
	if bundle.Service != "" {
		t.Errorf("expected empty service when no endpoints defined")
	}
	if bundle.NetworkPolicy != "" {
		t.Errorf("expected empty network policy when no endpoints defined")
	}
	if !strings.Contains(bundle.PodDisruption, "PodDisruptionBudget") {
		t.Errorf("expected PodDisruptionBudget")
	}
}

func TestSynthesizeWithEndpoints(t *testing.T) {
	synth := NewSynthesizer()
	spec := &config.AppSpec{
		Name: "api-gateway",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
	}

	bundle, err := synth.Synthesize(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(bundle.Service, "kind: Service") {
		t.Errorf("expected Service manifest")
	}
	if !strings.Contains(bundle.NetworkPolicy, "kind: NetworkPolicy") {
		t.Errorf("expected NetworkPolicy manifest")
	}
	if !strings.Contains(bundle.Deployment, "readinessProbe:") {
		t.Errorf("expected readiness probe in deployment")
	}
}

func TestManifestBundleFullYaml(t *testing.T) {
	bundle := &ManifestBundle{
		Deployment: "deployment-yaml",
		Service:    "service-yaml",
	}

	full := bundle.FullYaml()
	if !strings.Contains(full, "deployment-yaml\n---\nservice-yaml") {
		t.Errorf("unexpected FullYaml format: %s", full)
	}
}
