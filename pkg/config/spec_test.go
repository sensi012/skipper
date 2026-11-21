package config

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAppSpecDefaults(t *testing.T) {
	spec := AppSpec{
		Version: "v1alpha1",
		Name:    "demo-service",
		Type:    "service",
	}

	if spec.Name != "demo-service" {
		t.Fatalf("expected demo-service, got %s", spec.Name)
	}
	if spec.Security.RunAsNonRoot {
		t.Fatalf("expected RunAsNonRoot default false")
	}
	if spec.Runtime.Replicas.Min != 0 {
		t.Fatalf("expected zero value for Replicas.Min")
	}
}

func TestAppSpecFullPopulation(t *testing.T) {
	spec := AppSpec{
		Version:     "v1alpha1",
		Name:        "payment-api",
		Team:        "checkout",
		Environment: "production",
		Type:        "service",
		Image:       "payment-api:1.0.0",
		Build: BuildConfig{
			Context:    ".",
			Builder:    "paketobuildpacks/builder:base",
			Dockerfile: "Dockerfile",
			Args:       map[string]string{"GO_VERSION": "1.22"},
		},
		Runtime: RuntimeConfig{
			Replicas: ReplicaConfig{Min: 2, Max: 10, TargetCPUUtilization: 80},
			Resources: ResourceConfig{
				CPU:    ResourceLimit{Request: "100m", Limit: "500m"},
				Memory: ResourceLimit{Request: "128Mi", Limit: "512Mi"},
			},
		},
		Endpoints: []EndpointConfig{
			{
				Name:      "http",
				Port:      8080,
				Protocol:  "TCP",
				Public:    true,
				Domain:    "pay.internal",
				Path:      "/",
				RateLimit: &RateLimitConfig{RequestsPerSecond: 100},
			},
		},
		Health: HealthConfig{
			Readiness: &ProbeConfig{Path: "/healthz", Port: 8080, PeriodSeconds: 10},
			Liveness:  &ProbeConfig{Path: "/livez", Port: 8080, PeriodSeconds: 15},
		},
		Security: SecurityConfig{
			RunAsNonRoot:             true,
			ReadOnlyRootFilesystem:   true,
			AllowPrivilegeEscalation: false,
			Capabilities: &Capabilities{
				Drop: []string{"ALL"},
			},
		},
		Env: []EnvVar{
			{Name: "DB_HOST", Value: "postgres.default.svc"},
			{Name: "DB_PASS", FromSecret: "db-secret"},
		},
		Dependencies: DependenciesConfig{
			Services: []ServiceDependency{{Name: "auth-service", Namespace: "default", Required: true}},
			Storage:  []StorageDependency{{Name: "data", MountPath: "/data", Size: "10Gi"}},
		},
	}

	if spec.Team != "checkout" {
		t.Fatalf("expected checkout, got %s", spec.Team)
	}
	if spec.Endpoints[0].RateLimit.RequestsPerSecond != 100 {
		t.Fatalf("expected rate limit 100")
	}
}

func TestAppSpecJSONRoundTrip(t *testing.T) {
	spec := AppSpec{
		Version: "v1alpha1",
		Name:    "roundtrip-app",
		Type:    "service",
		Security: SecurityConfig{
			RunAsNonRoot: true,
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}

	var decoded AppSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal json: %v", err)
	}

	if decoded.Name != spec.Name || !decoded.Security.RunAsNonRoot {
		t.Fatalf("mismatch in decoded struct: %+v", decoded)
	}
}

func TestServiceDependencyFieldsRoundTrip(t *testing.T) {
	spec := AppSpec{
		Version: "v1",
		Name:    "orders-api",
		Type:    "service",
		Dependencies: DependenciesConfig{
			Services: []ServiceDependency{
				{
					Name:     "db",
					Required: true,
					Type:     "postgres",
					Port:     5432,
					Env:      map[string]string{"POSTGRES_USER": "orders"},
				},
				{
					Name:  "cache",
					Type:  "custom",
					Image: "valkey/valkey:8-alpine",
					Port:  6379,
				},
				{
					// Backward-compatible minimal entry
					Name:      "auth",
					Namespace: "core",
				},
			},
		},
	}

	rawYAML, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var fromYAML AppSpec
	if err := yaml.Unmarshal(rawYAML, &fromYAML); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if len(fromYAML.Dependencies.Services) != 3 {
		t.Fatalf("expected 3 service deps, got %d", len(fromYAML.Dependencies.Services))
	}
	if fromYAML.Dependencies.Services[0].Type != "postgres" || fromYAML.Dependencies.Services[0].Env["POSTGRES_USER"] != "orders" {
		t.Fatalf("unexpected postgres dep from YAML: %+v", fromYAML.Dependencies.Services[0])
	}
	if fromYAML.Dependencies.Services[1].Image != "valkey/valkey:8-alpine" || fromYAML.Dependencies.Services[1].Port != 6379 {
		t.Fatalf("unexpected custom dep from YAML: %+v", fromYAML.Dependencies.Services[1])
	}
	if fromYAML.Dependencies.Services[2].Namespace != "core" || fromYAML.Dependencies.Services[2].Type != "" {
		t.Fatalf("unexpected minimal dep from YAML: %+v", fromYAML.Dependencies.Services[2])
	}

	rawJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var fromJSON AppSpec
	if err := json.Unmarshal(rawJSON, &fromJSON); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if fromJSON.Dependencies.Services[0].Env["POSTGRES_USER"] != "orders" || fromJSON.Dependencies.Services[1].Image != "valkey/valkey:8-alpine" {
		t.Fatalf("unexpected deps from JSON: %+v", fromJSON.Dependencies.Services)
	}
}
