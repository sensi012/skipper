package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
)

func TestDockerNetworkName(t *testing.T) {
	if got := DockerNetworkName("orders-api"); got != "skipper-orders-api-net" {
		t.Fatalf("expected skipper-orders-api-net, got %q", got)
	}
}

func TestResolveDependencyPreset(t *testing.T) {
	t.Run("postgres preset", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("orders", config.ServiceDependency{
			Name: "db",
			Type: "postgres",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "postgres:16-alpine" || port != 5432 {
			t.Fatalf("expected postgres:16-alpine:5432, got %s:%d", img, port)
		}
		wantEnv := map[string]string{
			"POSTGRES_PASSWORD": "skipper",
			"POSTGRES_DB":       "orders",
		}
		if !reflect.DeepEqual(env, wantEnv) {
			t.Fatalf("env mismatch: got %v, want %v", env, wantEnv)
		}
	})

	t.Run("redis preset", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("orders", config.ServiceDependency{
			Name: "cache",
			Type: "redis",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "redis:7-alpine" || port != 6379 {
			t.Fatalf("expected redis:7-alpine:6379, got %s:%d", img, port)
		}
		if len(env) != 0 {
			t.Fatalf("expected empty env for redis, got %v", env)
		}
	})

	t.Run("mysql preset", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("billing", config.ServiceDependency{
			Name: "mysql",
			Type: "MySQL",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "mysql:8.4" || port != 3306 {
			t.Fatalf("expected mysql:8.4:3306, got %s:%d", img, port)
		}
		wantEnv := map[string]string{
			"MYSQL_ROOT_PASSWORD": "skipper",
			"MYSQL_DATABASE":      "billing",
		}
		if !reflect.DeepEqual(env, wantEnv) {
			t.Fatalf("env mismatch: got %v, want %v", env, wantEnv)
		}
	})

	t.Run("mongo preset", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("catalog", config.ServiceDependency{
			Name: "mongo",
			Type: "mongo",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "mongo:7" || port != 27017 {
			t.Fatalf("expected mongo:7:27017, got %s:%d", img, port)
		}
		if len(env) != 0 {
			t.Fatalf("expected empty env for mongo, got %v", env)
		}
	})

	t.Run("empty appName defaults db name to skipper", func(t *testing.T) {
		_, _, env, err := ResolveDependencyPreset("", config.ServiceDependency{
			Name: "db",
			Type: "postgres",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if env["POSTGRES_DB"] != "skipper" {
			t.Fatalf("expected default POSTGRES_DB=skipper, got %q", env["POSTGRES_DB"])
		}
	})

	t.Run("preset with image, port, and env overrides", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("orders", config.ServiceDependency{
			Name:  "db",
			Type:  "postgres",
			Image: "postgres:15-alpine",
			Port:  5433,
			Env: map[string]string{
				"POSTGRES_PASSWORD": "secret",
				"POSTGRES_USER":     "admin",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "postgres:15-alpine" || port != 5433 {
			t.Fatalf("expected postgres:15-alpine:5433, got %s:%d", img, port)
		}
		wantEnv := map[string]string{
			"POSTGRES_PASSWORD": "secret",
			"POSTGRES_DB":       "orders",
			"POSTGRES_USER":     "admin",
		}
		if !reflect.DeepEqual(env, wantEnv) {
			t.Fatalf("env mismatch: got %v, want %v", env, wantEnv)
		}
	})

	t.Run("custom image with type custom and empty type", func(t *testing.T) {
		img, port, env, err := ResolveDependencyPreset("orders", config.ServiceDependency{
			Name:  "queue",
			Type:  "custom",
			Image: "nats:2.10-alpine",
			Port:  4222,
			Env:   map[string]string{"NATS_DEBUG": "1"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img != "nats:2.10-alpine" || port != 4222 || env["NATS_DEBUG"] != "1" {
			t.Fatalf("unexpected custom resolution: %s:%d %v", img, port, env)
		}

		img2, port2, _, err := ResolveDependencyPreset("orders", config.ServiceDependency{
			Name:  "broker",
			Image: "rabbitmq:3-alpine",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img2 != "rabbitmq:3-alpine" || port2 != 0 {
			t.Fatalf("unexpected implicit custom resolution: %s:%d", img2, port2)
		}
	})

	t.Run("error cases", func(t *testing.T) {
		if _, _, _, err := ResolveDependencyPreset("orders", config.ServiceDependency{Name: "db"}); err == nil {
			t.Fatalf("expected error when both type and image are empty")
		}
		if _, _, _, err := ResolveDependencyPreset("orders", config.ServiceDependency{Name: "db", Type: "custom"}); err == nil {
			t.Fatalf("expected error when type is custom and image is empty")
		}
		if _, _, _, err := ResolveDependencyPreset("orders", config.ServiceDependency{Name: "stream", Type: "kafka"}); err == nil {
			t.Fatalf("expected error for unsupported type")
		}
	})
}

func TestDockerDeployWithDependencies(t *testing.T) {
	t.Run("starts network, deps, and injects env without overriding spec.Env", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				// docker network create
				{exitCode: 0},
				// docker rm -f orders-db
				{exitCode: 0},
				// docker run db
				{stdout: "db-container\n", exitCode: 0},
				// docker rm -f orders-cache-1
				{exitCode: 0},
				// docker run cache-1
				{stdout: "cache-container\n", exitCode: 0},
				// docker rm -f orders
				{exitCode: 0},
				// docker run orders
				{stdout: "app-container\n", exitCode: 0},
				// docker inspect
				{stdout: "running|true|2026-11-21T06:00:00Z", exitCode: 0},
			}, &calls),
		}

		spec := &config.AppSpec{
			Name:  "orders",
			Image: "orders:v1",
			Env: []config.EnvVar{
				{Name: "DB_HOST", Value: "custom-override-host"},
			},
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{
					{Name: "db", Type: "postgres"},
					{Name: "cache-1", Type: "redis"},
				},
			},
		}

		st, err := d.Deploy(context.Background(), spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.State != "Running" {
			t.Fatalf("expected Running, got %+v", st)
		}

		if len(calls) != 8 {
			t.Fatalf("expected 8 docker calls, got %d: %v", len(calls), calls)
		}
		if strings.Join(calls[0], " ") != "docker network create skipper-orders-net" {
			t.Fatalf("unexpected network create call: %v", calls[0])
		}
		if strings.Join(calls[1], " ") != "docker rm -f orders-db" {
			t.Fatalf("unexpected dep rm call: %v", calls[1])
		}
		wantDBRun := "docker run -d --name orders-db --network skipper-orders-net --network-alias db --label skipper.parent=orders -e POSTGRES_DB=orders -e POSTGRES_PASSWORD=skipper postgres:16-alpine"
		if strings.Join(calls[2], " ") != wantDBRun {
			t.Fatalf("unexpected db run call:\ngot:  %s\nwant: %s", strings.Join(calls[2], " "), wantDBRun)
		}
		wantCacheRun := "docker run -d --name orders-cache-1 --network skipper-orders-net --network-alias cache-1 --label skipper.parent=orders redis:7-alpine"
		if strings.Join(calls[4], " ") != wantCacheRun {
			t.Fatalf("unexpected cache run call:\ngot:  %s\nwant: %s", strings.Join(calls[4], " "), wantCacheRun)
		}

		appRun := strings.Join(calls[6], " ")
		for _, want := range []string{
			"--network skipper-orders-net",
			"-e DB_HOST=custom-override-host",
			"-e DB_PORT=5432",
			"-e CACHE_1_HOST=cache-1",
			"-e CACHE_1_PORT=6379",
			"orders:v1",
		} {
			if !strings.Contains(appRun, want) {
				t.Errorf("expected main app run to contain %q, got: %s", want, appRun)
			}
		}
		if strings.Contains(appRun, "-e DB_HOST=db") {
			t.Errorf("DB_HOST from spec.Env should not be overridden: %s", appRun)
		}
	})

	t.Run("ignores already exists network error", func(t *testing.T) {
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{stderr: "Error response from daemon: network with name skipper-api-net already exists", exitCode: 1},
				{exitCode: 0}, // rm dep
				{exitCode: 0}, // run dep
				{exitCode: 0}, // rm app
				{exitCode: 0}, // run app
				{stdout: "running|true|2026-11-21T06:00:00Z", exitCode: 0}, // inspect
			}, nil),
		}

		_, err := d.Deploy(context.Background(), &config.AppSpec{
			Name:  "api",
			Image: "api:v1",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{
					{Name: "cache", Type: "redis"},
				},
			},
		})
		if err != nil {
			t.Fatalf("expected already-exists network error to be ignored, got: %v", err)
		}
	})

	t.Run("custom dependency without port omits PORT env", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0}, // network create
				{exitCode: 0}, // rm dep
				{exitCode: 0}, // run dep
				{exitCode: 0}, // rm app
				{exitCode: 0}, // run app
				{stdout: "running|true|2026-11-21T06:00:00Z", exitCode: 0}, // inspect
			}, &calls),
		}

		_, err := d.Deploy(context.Background(), &config.AppSpec{
			Name:  "app",
			Image: "app:v1",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{
					{Name: "sidecar", Type: "custom", Image: "busybox:latest"},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		appRun := strings.Join(calls[4], " ")
		if !strings.Contains(appRun, "-e SIDECAR_HOST=sidecar") {
			t.Errorf("expected SIDECAR_HOST=sidecar in %s", appRun)
		}
		if strings.Contains(appRun, "SIDECAR_PORT") {
			t.Errorf("did not expect SIDECAR_PORT when port is 0: %s", appRun)
		}
	})

	t.Run("validation and failure paths", func(t *testing.T) {
		d := &DockerAdapter{
			execCommand: makeFakeExec(nil, nil),
		}
		if _, err := d.Deploy(context.Background(), &config.AppSpec{
			Name: "api",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{{Name: ""}},
			},
		}); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("expected empty dependency name error, got: %v", err)
		}

		if _, err := d.Deploy(context.Background(), &config.AppSpec{
			Name: "api",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{{Name: "bad", Type: "unknown"}},
			},
		}); err == nil || !strings.Contains(err.Error(), "unsupported dependency type") {
			t.Fatalf("expected unsupported type error, got: %v", err)
		}

		dNetFail := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{stderr: "permission denied", exitCode: 1},
			}, nil),
		}
		if _, err := dNetFail.Deploy(context.Background(), &config.AppSpec{
			Name: "api",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{{Name: "cache", Type: "redis"}},
			},
		}); err == nil || !strings.Contains(err.Error(), "failed to create docker network") {
			t.Fatalf("expected network create error, got: %v", err)
		}

		dDepFail := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0}, // network create
				{exitCode: 0}, // rm dep
				{stderr: "pull access denied", exitCode: 125}, // run dep
			}, nil),
		}
		if _, err := dDepFail.Deploy(context.Background(), &config.AppSpec{
			Name: "api",
			Dependencies: config.DependenciesConfig{
				Services: []config.ServiceDependency{{Name: "cache", Type: "redis"}},
			},
		}); err == nil || !strings.Contains(err.Error(), "failed to start dependency cache") {
			t.Fatalf("expected dependency start error, got: %v", err)
		}
	})
}

func TestDockerStopCleansUpDependencies(t *testing.T) {
	t.Run("removes labeled dependency containers and network", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0},                           // docker stop orders
				{stdout: "dep-1\ndep-2\n", exitCode: 0}, // docker ps -aq --filter label=skipper.parent=orders
				{exitCode: 0},                           // docker rm -f dep-1 dep-2
				{exitCode: 0},                           // docker network rm skipper-orders-net
			}, &calls),
		}

		if err := d.Stop(context.Background(), "orders"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(calls) != 4 {
			t.Fatalf("expected 4 docker calls, got %d: %v", len(calls), calls)
		}
		if strings.Join(calls[0], " ") != "docker stop orders" {
			t.Fatalf("unexpected stop call: %v", calls[0])
		}
		if strings.Join(calls[1], " ") != "docker ps -aq --filter label=skipper.parent=orders" {
			t.Fatalf("unexpected ps filter call: %v", calls[1])
		}
		if strings.Join(calls[2], " ") != "docker rm -f dep-1 dep-2" {
			t.Fatalf("unexpected rm call: %v", calls[2])
		}
		if strings.Join(calls[3], " ") != "docker network rm skipper-orders-net" {
			t.Fatalf("unexpected network rm call: %v", calls[3])
		}
	})

	t.Run("returns error when stopping main container fails", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{stderr: "no such container", exitCode: 1},
			}, &calls),
		}
		if err := d.Stop(context.Background(), "missing"); err == nil {
			t.Fatalf("expected stop error")
		}
		if len(calls) != 1 {
			t.Fatalf("expected stop to abort after first failure, got %d calls", len(calls))
		}
	})
}
