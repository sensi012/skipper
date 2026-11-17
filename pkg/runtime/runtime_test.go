package runtime

import (
	"strings"
	"testing"

	"github.com/unified-platform/container/pkg/config"
)

func TestDockerAdapterName(t *testing.T) {
	adapter := NewDockerAdapter()
	if adapter.Name() != "docker" {
		t.Fatalf("expected docker, got %s", adapter.Name())
	}
}

func TestKubernetesAdapterName(t *testing.T) {
	adapter := NewKubernetesAdapter("staging")
	if adapter.Name() != "kubernetes" {
		t.Fatalf("expected kubernetes, got %s", adapter.Name())
	}
	if adapter.Namespace != "staging" {
		t.Fatalf("expected staging, got %s", adapter.Namespace)
	}
}

func TestGenerateStandardManifests(t *testing.T) {
	k8s := NewKubernetesAdapter("default")
	spec := &config.AppSpec{
		Name: "auth-svc",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
	}

	manifest, err := k8s.GenerateStandardManifests(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(manifest, "kind: Deployment") {
		t.Errorf("expected Deployment in standard manifest")
	}
	if !strings.Contains(manifest, "kind: Service") {
		t.Errorf("expected Service in standard manifest")
	}
	if !strings.Contains(manifest, "auth-svc:latest") {
		t.Errorf("expected default image tag auth-svc:latest")
	}
}
