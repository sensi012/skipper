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

func TestSynthesizeEdgeCasesAndValidation(t *testing.T) {
	synth := NewSynthesizer()

	if _, err := synth.Synthesize(nil, "default"); err == nil {
		t.Error("expected error for nil spec")
	}
	if _, err := synth.Synthesize(&config.AppSpec{}, "default"); err == nil {
		t.Error("expected error for empty spec name")
	}
	if _, err := synth.Synthesize(&config.AppSpec{Name: "-bad"}, "default"); err == nil {
		t.Error("expected error for leading dash spec name")
	}
	if _, err := synth.Synthesize(&config.AppSpec{Name: "app"}, "bad\nns"); err == nil {
		t.Error("expected error for newline in namespace")
	}

	// Nil receiver and zero-value Synthesizer fallback defaults
	var nilSynth *Synthesizer
	spec := &config.AppSpec{
		Name:  "custom-svc",
		Image: "ghcr.io/acme/custom:v1",
		Env: []config.EnvVar{
			{Name: "LOG_LEVEL", Value: "info"},
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "250m", Limit: "1000m"},
				Memory: config.ResourceLimit{Request: "256Mi", Limit: "1Gi"},
			},
		},
	}
	bundle, err := nilSynth.Synthesize(spec, "")
	if err != nil {
		t.Fatalf("unexpected error on nil synthesizer receiver: %v", err)
	}
	if !strings.Contains(bundle.Deployment, `name: LOG_LEVEL`) || !strings.Contains(bundle.Deployment, "cpu: 250m") {
		t.Errorf("expected custom env and CPU request in deployment:\n%s", bundle.Deployment)
	}

	zeroSynth := &Synthesizer{}
	b2, err := zeroSynth.Synthesize(&config.AppSpec{Name: "zero-app"}, "default")
	if err != nil || !strings.Contains(b2.Deployment, "cpu: 100m") {
		t.Errorf("expected fallback default CPU 100m on zero-value Synthesizer, err=%v", err)
	}
}

func TestSynthesizeNormalizesDockerMemoryAndCPUUnits(t *testing.T) {
	synth := NewSynthesizer()
	spec := &config.AppSpec{
		Name: "normalized-app",
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "0.25", Limit: "1.5"},
				Memory: config.ResourceLimit{Request: "256m", Limit: "1gb"},
			},
		},
	}

	bundle, err := synth.Synthesize(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"cpu: 250m",
		"memory: 256Mi",
		"cpu: 1500m",
		"memory: 1Gi",
	} {
		if !strings.Contains(bundle.Deployment, want) {
			t.Errorf("expected %q in synthesized deployment:\n%s", want, bundle.Deployment)
		}
	}
}

func TestSynthesizeTmpVolumeAndHealthProbes(t *testing.T) {
	synth := NewSynthesizer()

	t.Run("mounts emptyDir /tmp and falls back to tcpSocket probes when Health is unset", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "nginx-web",
			Endpoints: []config.EndpointConfig{
				{Name: "http", Port: 8080, Protocol: "TCP"},
			},
		}
		bundle, err := synth.Synthesize(spec, "default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"readOnlyRootFilesystem: true",
			"mountPath: /tmp",
			"emptyDir: {}",
			"tcpSocket:",
			"port: 8080",
		} {
			if !strings.Contains(bundle.Deployment, want) {
				t.Errorf("expected %q in deployment:\n%s", want, bundle.Deployment)
			}
		}
		if strings.Contains(bundle.Deployment, "/healthz") || strings.Contains(bundle.Deployment, "/livez") {
			t.Errorf("did not expect hardcoded /healthz or /livez without spec.Health:\n%s", bundle.Deployment)
		}
	})

	t.Run("honors custom startup, readiness, and liveness HTTP probes", func(t *testing.T) {
		spec := &config.AppSpec{
			Name: "custom-api",
			Endpoints: []config.EndpointConfig{
				{Name: "http", Port: 8080, Protocol: "TCP"},
			},
			Health: config.HealthConfig{
				Startup: &config.ProbeConfig{
					Path:                "/boot",
					Port:                9000,
					InitialDelaySeconds: 2,
					PeriodSeconds:       5,
					FailureThreshold:    12,
				},
				Readiness: &config.ProbeConfig{
					Path:                "/ready",
					InitialDelaySeconds: 3,
					PeriodSeconds:       8,
					TimeoutSeconds:      2,
				},
				Liveness: &config.ProbeConfig{
					Path:                "/health",
					InitialDelaySeconds: 15,
					PeriodSeconds:       20,
				},
			},
		}
		bundle, err := synth.Synthesize(spec, "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{
			"startupProbe:",
			"path: /boot",
			"port: 9000",
			"failureThreshold: 12",
			"path: /ready",
			"timeoutSeconds: 2",
			"path: /health",
			"port: 8080",
		} {
			if !strings.Contains(bundle.Deployment, want) {
				t.Errorf("expected %q in custom-probe deployment:\n%s", want, bundle.Deployment)
			}
		}
	})
}
