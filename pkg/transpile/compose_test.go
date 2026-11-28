package transpile

import (
	"testing"
)

func TestTranspileCompose(t *testing.T) {
	composeYaml := `
version: "3.8"
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      - ENV=production
      - DEBUG=false
    volumes:
      - ./data:/var/www/html
    deploy:
      replicas: 2
      resources:
        limits:
          memory: 256Mi
`

	transpiler := NewTranspiler()
	spec, err := transpiler.TranspileCompose([]byte(composeYaml), "web")
	if err != nil {
		t.Fatalf("failed to transpile compose: %v", err)
	}

	if spec.Name != "web" {
		t.Fatalf("expected name 'web', got %s", spec.Name)
	}
	if spec.Image != "nginx:alpine" {
		t.Fatalf("expected image 'nginx:alpine', got %s", spec.Image)
	}
	if len(spec.Endpoints) != 1 || spec.Endpoints[0].Port != 80 {
		t.Fatalf("expected endpoint on port 80, got %+v", spec.Endpoints)
	}
	if len(spec.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(spec.Env))
	}
	if len(spec.Dependencies.Storage) != 1 || spec.Dependencies.Storage[0].MountPath != "/var/www/html" {
		t.Fatalf("expected storage volume at /var/www/html")
	}
	if spec.Runtime.Replicas.Min != 2 {
		t.Fatalf("expected 2 replicas, got %d", spec.Runtime.Replicas.Min)
	}
	if spec.Runtime.Resources.Memory.Limit != "256Mi" {
		t.Fatalf("expected 256Mi memory limit, got %s", spec.Runtime.Resources.Memory.Limit)
	}
}

func TestTranspileSecurityAndEdgeCases(t *testing.T) {
	if got := SanitizeName("--privileged"); got != "privileged" {
		t.Fatalf("expected SanitizeName('--privileged') == 'privileged', got %q", got)
	}
	if got := SanitizeName("---"); got != "app" {
		t.Fatalf("expected SanitizeName('---') == 'app', got %q", got)
	}

	tr := NewTranspiler()
	if _, err := tr.TranspileCompose([]byte("services:\n  -evil:\n    image: alpine\n"), "-evil"); err == nil {
		t.Fatalf("expected TranspileCompose to reject service name starting with '-'")
	}
	if _, err := tr.TranspileCompose([]byte("services: {}\n"), ""); err == nil {
		t.Fatalf("expected TranspileCompose to reject empty services map")
	}
	if _, err := tr.TranspileCompose([]byte("services:\n  web:\n    image: nginx\n"), "missing"); err == nil {
		t.Fatalf("expected TranspileCompose to reject missing target service")
	}
}

func TestTranspileComposeNormalizesResources(t *testing.T) {
	composeYaml := `
services:
  api:
    image: node:20-alpine
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 512m
        reservations:
          cpus: "0.25"
          memory: 256mb
  tiny:
    image: alpine
    deploy:
      resources:
        limits:
          cpus: "0.05"
          memory: 64m
`
	tr := NewTranspiler()
	apiSpec, err := tr.TranspileCompose([]byte(composeYaml), "api")
	if err != nil {
		t.Fatalf("TranspileCompose api failed: %v", err)
	}
	if apiSpec.Runtime.Resources.Memory.Limit != "512Mi" || apiSpec.Runtime.Resources.Memory.Request != "256Mi" {
		t.Fatalf("expected normalized memory 256Mi/512Mi, got %+v", apiSpec.Runtime.Resources.Memory)
	}
	if apiSpec.Runtime.Resources.CPU.Limit != "500m" || apiSpec.Runtime.Resources.CPU.Request != "250m" {
		t.Fatalf("expected normalized CPU 250m/500m, got %+v", apiSpec.Runtime.Resources.CPU)
	}

	tinySpec, err := tr.TranspileCompose([]byte(composeYaml), "tiny")
	if err != nil {
		t.Fatalf("TranspileCompose tiny failed: %v", err)
	}
	if tinySpec.Runtime.Resources.Memory.Limit != "64Mi" || tinySpec.Runtime.Resources.Memory.Request != "64Mi" {
		t.Fatalf("expected clamped memory request=limit=64Mi, got %+v", tinySpec.Runtime.Resources.Memory)
	}
	if tinySpec.Runtime.Resources.CPU.Limit != "50m" || tinySpec.Runtime.Resources.CPU.Request != "50m" {
		t.Fatalf("expected clamped CPU request=limit=50m, got %+v", tinySpec.Runtime.Resources.CPU)
	}
}
