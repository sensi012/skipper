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
