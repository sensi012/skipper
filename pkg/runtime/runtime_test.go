package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sensi012/skipper/pkg/config"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("HELPER_STDOUT") != "" {
		fmt.Fprint(os.Stdout, os.Getenv("HELPER_STDOUT"))
	}
	if os.Getenv("HELPER_STDERR") != "" {
		fmt.Fprint(os.Stderr, os.Getenv("HELPER_STDERR"))
	}
	code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT_CODE"))
	os.Exit(code)
}

type fakeStep struct {
	stdout   string
	stderr   string
	exitCode int
}

func makeFakeExec(steps []fakeStep, calls *[][]string) func(context.Context, string, ...string) *exec.Cmd {
	idx := 0
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if calls != nil {
			*calls = append(*calls, append([]string{name}, args...))
		}
		var step fakeStep
		if idx < len(steps) {
			step = steps[idx]
			idx++
		}
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_STDOUT="+step.stdout,
			"HELPER_STDERR="+step.stderr,
			fmt.Sprintf("HELPER_EXIT_CODE=%d", step.exitCode),
		)
		return cmd
	}
}

func TestDockerAdapterName(t *testing.T) {
	adapter := NewDockerAdapter()
	if adapter.Name() != "docker" {
		t.Fatalf("expected docker, got %s", adapter.Name())
	}
}

func TestBuildDockerRunArgs(t *testing.T) {
	spec := &config.AppSpec{
		Name:  "orders-api",
		Image: "ghcr.io/acme/orders:v2",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
			{Name: "metrics", Port: 9090},
		},
		Env: []config.EnvVar{
			{Name: "LOG_LEVEL", Value: "debug"},
			{Name: "EMPTY_VAR", Value: ""},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot: true,
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				Memory: config.ResourceLimit{Limit: "512m"},
			},
		},
	}

	got := buildDockerRunArgs(spec)
	want := []string{
		"run", "-d", "--name", "orders-api",
		"-p", "8080:8080",
		"-p", "9090:9090",
		"-e", "LOG_LEVEL=debug",
		"--security-opt=no-new-privileges:true",
		"-m", "512m",
		"ghcr.io/acme/orders:v2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildDockerRunArgs mismatch:\ngot:  %v\nwant: %v", got, want)
	}

	// Default image tag when spec.Image is empty
	minimal := buildDockerRunArgs(&config.AppSpec{Name: "worker"})
	if minimal[len(minimal)-1] != "worker:latest" {
		t.Fatalf("expected default image worker:latest, got %v", minimal)
	}
}

func TestNormalizeDockerUnits(t *testing.T) {
	memCases := map[string]string{
		"512Mi":    "512m",
		"1Gi":      "1g",
		"256Ki":    "256k",
		"2Ti":      "2048g",
		"512MiB":   "512m",
		"1GiB":     "1g",
		"256KiB":   "256k",
		"2TiB":     "2048g",
		"512mb":    "512m",
		"512MB":    "512m",
		"1gb":      "1g",
		"1GB":      "1g",
		"256kb":    "256k",
		"2t":       "2048g",
		"2tb":      "2048g",
		"500M":     "477m",
		"1G":       "954m",
		"2G":       "1907m",
		"1T":       "931g",
		"1024K":    "1000k",
		"512m":     "512m",
		"1048576b": "1048576",
		"1048576":  "1048576",
		"":         "",
	}
	for in, want := range memCases {
		if got := normalizeDockerMemory(in); got != want {
			t.Errorf("normalizeDockerMemory(%q) = %q, want %q", in, got, want)
		}
	}

	cpuCases := map[string]string{
		"500m":  "0.5",
		"1500m": "1.5",
		"1000m": "1",
		"2":     "2",
		"0.25":  "0.25",
		"":      "",
		"bad":   "",
	}
	for in, want := range cpuCases {
		if got := normalizeDockerCPU(in); got != want {
			t.Errorf("normalizeDockerCPU(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildDockerRunArgsK8sUnitsAndSecurity(t *testing.T) {
	spec := &config.AppSpec{
		Name:  "dns-proxy",
		Image: "dns-proxy:v1",
		Endpoints: []config.EndpointConfig{
			{Name: "dns", Port: 53, Protocol: "UDP"},
			{Name: "http", Port: 8080, Protocol: "TCP"},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot:           true,
			ReadOnlyRootFilesystem: true,
		},
		Health: config.HealthConfig{
			Liveness: &config.ProbeConfig{
				Path:          "/livez",
				Port:          8080,
				PeriodSeconds: 15,
			},
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				Memory: config.ResourceLimit{Limit: "512Mi"},
				CPU:    config.ResourceLimit{Limit: "500m"},
			},
		},
	}

	got := buildDockerRunArgs(spec)
	want := []string{
		"run", "-d", "--name", "dns-proxy",
		"-p", "53:53/udp",
		"-p", "8080:8080",
		"--security-opt=no-new-privileges:true",
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid",
		"-m", "512m",
		"--cpus", "0.5",
		"--health-cmd", "wget -qO- http://127.0.0.1:8080/livez || exit 1",
		"--health-interval", "15s",
		"dns-proxy:v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildDockerRunArgs K8s normalization mismatch:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestDockerDeployMissingLocalImageHint(t *testing.T) {
	adapter := NewDockerAdapter()
	adapter.execCommand = makeFakeExec([]fakeStep{
		{exitCode: 0}, // docker rm -f
		{exitCode: 125, stderr: "Unable to find image 'demo-app:latest' locally"}, // docker run
	}, nil)

	_, err := adapter.Deploy(context.Background(), &config.AppSpec{Name: "demo-app"})
	if err == nil || !strings.Contains(err.Error(), "skipper build") {
		t.Fatalf("expected helpful 'skipper build' hint on missing local image, got: %v", err)
	}
}

func TestDockerAdapterLifecycle(t *testing.T) {
	t.Run("Initialize success and failure", func(t *testing.T) {
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{{stdout: "29.9.2\n"}}, nil),
		}
		if err := d.Initialize(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		dFail := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{{stderr: "cannot connect", exitCode: 1}}, nil),
		}
		if err := dFail.Initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "failed to connect") {
			t.Fatalf("expected connection error, got %v", err)
		}
	})

	t.Run("Deploy success", func(t *testing.T) {
		var calls [][]string
		ts := "2026-11-21T06:00:00.123456Z"
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0},                               // docker rm -f
				{stdout: "container123\n", exitCode: 0},     // docker run
				{stdout: "running|true|" + ts, exitCode: 0}, // docker inspect
			}, &calls),
		}
		status, err := d.Deploy(context.Background(), &config.AppSpec{Name: "api"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.State != "Running" || status.Replicas != 1 || status.Target != "docker" {
			t.Fatalf("unexpected status: %+v", status)
		}
		wantTime, _ := time.Parse(time.RFC3339Nano, ts)
		if !status.CreatedAt.Equal(wantTime) {
			t.Fatalf("expected CreatedAt %v, got %v", wantTime, status.CreatedAt)
		}
		if len(calls) != 3 {
			t.Fatalf("expected 3 docker calls, got %d: %v", len(calls), calls)
		}
	})

	t.Run("Deploy validation and run failure", func(t *testing.T) {
		d := NewDockerAdapter()
		if _, err := d.Deploy(context.Background(), nil); err == nil {
			t.Fatalf("expected error for nil spec")
		}
		if _, err := d.Deploy(context.Background(), &config.AppSpec{}); err == nil {
			t.Fatalf("expected error for empty spec name")
		}

		dFail := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0},
				{stderr: "image not found", exitCode: 1},
			}, nil),
		}
		if _, err := dFail.Deploy(context.Background(), &config.AppSpec{Name: "api"}); err == nil || !strings.Contains(err.Error(), "docker run failed") {
			t.Fatalf("expected docker run failure, got %v", err)
		}
	})

	t.Run("Status stopped and error", func(t *testing.T) {
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{{stdout: "exited|false|not-a-date"}}, nil),
		}
		st, err := d.Status(context.Background(), "api")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.State != "Stopped" || st.Replicas != 0 {
			t.Fatalf("expected Stopped/0, got %+v", st)
		}

		dErr := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{{stderr: "no such container", exitCode: 1}}, nil),
		}
		if _, err := dErr.Status(context.Background(), "missing"); err == nil {
			t.Fatalf("expected inspect error")
		}
	})

	t.Run("Stop", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0},
				{exitCode: 0},
			}, &calls),
		}
		if err := d.Stop(context.Background(), "api"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(calls) < 2 {
			t.Fatalf("expected at least 2 docker calls (stop + rm), got %d: %v", len(calls), calls)
		}
		if strings.Join(calls[0], " ") != "docker stop api" {
			t.Fatalf("unexpected stop command: %v", calls[0])
		}
		if strings.Join(calls[1], " ") != "docker rm -f api" {
			t.Fatalf("unexpected rm command: %v", calls[1])
		}
	})

	t.Run("Logs with all options", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{{stdout: "hello log\n"}}, &calls),
		}
		var out bytes.Buffer
		err := d.Logs(context.Background(), "api", LogOptions{
			Follow:     true,
			TailLines:  50,
			Timestamps: true,
		}, &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "docker logs -f --tail=50 -t api"
		if strings.Join(calls[0], " ") != want {
			t.Fatalf("expected %q, got %q", want, strings.Join(calls[0], " "))
		}
		if !strings.Contains(out.String(), "hello log") {
			t.Fatalf("expected log output, got %q", out.String())
		}
	})

	t.Run("Debug default command, custom command, and exit code", func(t *testing.T) {
		var calls [][]string
		d := &DockerAdapter{
			execCommand: makeFakeExec([]fakeStep{
				{stdout: "ok", exitCode: 0},
				{stderr: "bad cmd", exitCode: 42},
			}, &calls),
		}
		var out, errBuf bytes.Buffer
		code, err := d.Debug(context.Background(), DebugOptions{WorkloadName: "api"}, nil, &out, &errBuf)
		if err != nil || code != 0 {
			t.Fatalf("expected 0/nil, got %d/%v", code, err)
		}
		if strings.Join(calls[0], " ") != "docker exec -it api /bin/sh" {
			t.Fatalf("unexpected debug args: %v", calls[0])
		}

		code, err = d.Debug(context.Background(), DebugOptions{
			WorkloadName: "api",
			Command:      []string{"ls", "-la"},
		}, nil, &out, &errBuf)
		if err != nil || code != 42 {
			t.Fatalf("expected exit code 42/nil, got %d/%v", code, err)
		}
		if strings.Join(calls[1], " ") != "docker exec -it api ls -la" {
			t.Fatalf("unexpected custom debug args: %v", calls[1])
		}
	})
}

func TestDockerAdapterLiveSafeChecks(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not installed; skipping live check")
	}
	d := NewDockerAdapter()
	if err := d.Initialize(context.Background()); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	// Querying a nonexistent container must fail cleanly without side effects.
	if _, err := d.Status(context.Background(), "skipper-nonexistent-unit-test-container-99999"); err == nil {
		t.Fatalf("expected error inspecting nonexistent container")
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

	def := NewKubernetesAdapter("")
	if def.Namespace != "default" {
		t.Fatalf("expected default namespace when empty, got %s", def.Namespace)
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

	for _, want := range []string{
		"kind: Deployment",
		"kind: Service",
		"auth-svc:latest",
		"replicas: 1",
		"cpu: 100m",
		"memory: 128Mi",
		"cpu: 500m",
		"memory: 512Mi",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("expected %q in standard manifest", want)
		}
	}

	// Custom image, replicas, resources, and no endpoints (no Service)
	custom, err := k8s.GenerateStandardManifests(&config.AppSpec{
		Name:  "batch-job",
		Image: "ghcr.io/acme/batch:v3",
		Runtime: config.RuntimeConfig{
			Replicas: config.ReplicaConfig{Min: 3},
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "250m", Limit: "1000m"},
				Memory: config.ResourceLimit{Request: "256Mi", Limit: "1Gi"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(custom, "kind: Service") {
		t.Errorf("did not expect Service when no endpoints are defined")
	}
	for _, want := range []string{"replicas: 3", "ghcr.io/acme/batch:v3", "cpu: 250m", "cpu: 1000m", "memory: 256Mi", "memory: 1Gi"} {
		if !strings.Contains(custom, want) {
			t.Errorf("expected %q in custom manifest", want)
		}
	}

	if _, err := k8s.GenerateStandardManifests(nil); err == nil {
		t.Fatalf("expected error for nil spec")
	}
	if _, err := k8s.GenerateStandardManifests(&config.AppSpec{}); err == nil {
		t.Fatalf("expected error for empty spec name")
	}
}

func TestKubernetesAdapterLifecycle(t *testing.T) {
	t.Run("Initialize success and failure", func(t *testing.T) {
		k := &KubernetesAdapter{
			Namespace:   "default",
			execCommand: makeFakeExec([]fakeStep{{stdout: "Client Version: v1.37.1"}}, nil),
		}
		if err := k.Initialize(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		kFail := &KubernetesAdapter{
			Namespace:   "default",
			execCommand: makeFakeExec([]fakeStep{{stderr: "not found", exitCode: 1}}, nil),
		}
		if err := kFail.Initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "kubectl client unavailable") {
			t.Fatalf("expected kubectl unavailable error, got %v", err)
		}
	})

	t.Run("Deploy success and errors", func(t *testing.T) {
		var calls [][]string
		k := &KubernetesAdapter{
			Namespace: "prod",
			execCommand: makeFakeExec([]fakeStep{
				{stdout: "deployment.apps/web configured", exitCode: 0}, // kubectl apply
				{stdout: "2|2", exitCode: 0},                            // kubectl get deployment
			}, &calls),
		}
		st, err := k.Deploy(context.Background(), &config.AppSpec{Name: "web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.State != "Running" || st.Replicas != 2 || st.Available != 2 || st.Labels["namespace"] != "prod" {
			t.Fatalf("unexpected status: %+v", st)
		}
		if strings.Join(calls[0], " ") != "kubectl apply -n prod -f -" {
			t.Fatalf("unexpected apply args: %v", calls[0])
		}

		// Invalid spec
		if _, err := k.Deploy(context.Background(), nil); err == nil {
			t.Fatalf("expected error for nil spec")
		}

		// Apply failure
		kFail := &KubernetesAdapter{
			Namespace:   "prod",
			execCommand: makeFakeExec([]fakeStep{{stderr: "forbidden", exitCode: 1}}, nil),
		}
		if _, err := kFail.Deploy(context.Background(), &config.AppSpec{Name: "web"}); err == nil || !strings.Contains(err.Error(), "kubectl apply failed") {
			t.Fatalf("expected apply error, got %v", err)
		}
	})

	t.Run("Status states Running, Degraded, Stopped, Error", func(t *testing.T) {
		cases := []struct {
			out           string
			wantState     string
			wantReplicas  int
			wantAvailable int
		}{
			{"3|3", "Running", 3, 3},
			{"3|1", "Degraded", 3, 1},
			{"3|", "Degraded", 3, 0},
			{"0|0", "Stopped", 0, 0},
			{"|", "Stopped", 0, 0},
		}
		for _, tc := range cases {
			k := &KubernetesAdapter{
				Namespace:   "dev",
				execCommand: makeFakeExec([]fakeStep{{stdout: tc.out}}, nil),
			}
			st, err := k.Status(context.Background(), "svc")
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.out, err)
			}
			if st.State != tc.wantState || st.Replicas != tc.wantReplicas || st.Available != tc.wantAvailable {
				t.Errorf("for %q expected %s (%d/%d), got %+v", tc.out, tc.wantState, tc.wantReplicas, tc.wantAvailable, st)
			}
		}

		kErr := &KubernetesAdapter{
			Namespace:   "dev",
			execCommand: makeFakeExec([]fakeStep{{stderr: "not found", exitCode: 1}}, nil),
		}
		if _, err := kErr.Status(context.Background(), "missing"); err == nil {
			t.Fatalf("expected status error")
		}
	})

	t.Run("Stop and Logs", func(t *testing.T) {
		var calls [][]string
		k := &KubernetesAdapter{
			Namespace: "staging",
			execCommand: makeFakeExec([]fakeStep{
				{exitCode: 0},
				{stdout: "pod log line\n", exitCode: 0},
			}, &calls),
		}
		if err := k.Stop(context.Background(), "api"); err != nil {
			t.Fatalf("unexpected stop error: %v", err)
		}
		if strings.Join(calls[0], " ") != "kubectl scale deployment api -n staging --replicas=0" {
			t.Fatalf("unexpected stop args: %v", calls[0])
		}

		var out bytes.Buffer
		err := k.Logs(context.Background(), "api", LogOptions{
			Follow:     true,
			TailLines:  25,
			Timestamps: true,
			Container:  "app",
		}, &out)
		if err != nil {
			t.Fatalf("unexpected logs error: %v", err)
		}
		wantLogs := "kubectl logs deployment/api -n staging -f --tail=25 --timestamps -c app"
		if strings.Join(calls[1], " ") != wantLogs {
			t.Fatalf("expected %q, got %q", wantLogs, strings.Join(calls[1], " "))
		}
	})

	t.Run("Debug pod discovery, explicit pod, no pods, exit code", func(t *testing.T) {
		var calls [][]string
		k := &KubernetesAdapter{
			Namespace: "prod",
			execCommand: makeFakeExec([]fakeStep{
				{stdout: "web-7f8b-xyz\n", exitCode: 0}, // get pods
				{stdout: "debug ok", exitCode: 0},       // debug
			}, &calls),
		}
		var out, errBuf bytes.Buffer
		code, err := k.Debug(context.Background(), DebugOptions{WorkloadName: "web"}, nil, &out, &errBuf)
		if err != nil || code != 0 {
			t.Fatalf("expected 0/nil, got %d/%v", code, err)
		}
		wantDebug := "kubectl debug -it web-7f8b-xyz -n prod --image=nicolaka/netshoot:latest -- /bin/sh"
		if strings.Join(calls[1], " ") != wantDebug {
			t.Fatalf("expected %q, got %q", wantDebug, strings.Join(calls[1], " "))
		}

		// Explicit pod, custom image, target container, custom command, non-zero exit
		calls = nil
		kCustom := &KubernetesAdapter{
			Namespace:   "prod",
			execCommand: makeFakeExec([]fakeStep{{exitCode: 7}}, &calls),
		}
		code, err = kCustom.Debug(context.Background(), DebugOptions{
			TargetPod:       "pod-1",
			TargetContainer: "main",
			DebugImage:      "busybox:latest",
			Command:         []string{"sh", "-c", "id"},
		}, nil, &out, &errBuf)
		if err != nil || code != 7 {
			t.Fatalf("expected 7/nil, got %d/%v", code, err)
		}
		wantCustom := "kubectl debug -it pod-1 -n prod --image=busybox:latest --target=main -- sh -c id"
		if strings.Join(calls[0], " ") != wantCustom {
			t.Fatalf("expected %q, got %q", wantCustom, strings.Join(calls[0], " "))
		}

		// Whitespace-only pod output treated as no pods found
		kEmpty := &KubernetesAdapter{
			Namespace:   "prod",
			execCommand: makeFakeExec([]fakeStep{{stdout: "   \n", exitCode: 0}}, nil),
		}
		code, err = kEmpty.Debug(context.Background(), DebugOptions{WorkloadName: "ghost"}, nil, &out, &errBuf)
		if err == nil || code != 1 || !strings.Contains(err.Error(), "no pods found") {
			t.Fatalf("expected no pods found error with code 1, got %d/%v", code, err)
		}
	})
}

func TestKubernetesAdapterLiveClientCheck(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl binary not installed; skipping live check")
	}
	k := NewKubernetesAdapter("default")
	if err := k.Initialize(context.Background()); err != nil {
		t.Fatalf("live kubectl version --client failed: %v", err)
	}
}

func TestRuntimeFlagInjectionAndInputValidation(t *testing.T) {
	ctx := context.Background()
	d := NewDockerAdapter()
	k := NewKubernetesAdapter("default")

	malicious := []string{"--privileged", "-e", "bad name", "bad\nname"}
	for _, bad := range malicious {
		if _, err := d.Deploy(ctx, &config.AppSpec{Name: bad}); err == nil {
			t.Errorf("DockerAdapter.Deploy should reject workload name %q", bad)
		}
		if _, err := d.Deploy(ctx, &config.AppSpec{Name: "app", Image: bad}); err == nil {
			t.Errorf("DockerAdapter.Deploy should reject image %q", bad)
		}
		if _, err := d.Status(ctx, bad); err == nil {
			t.Errorf("DockerAdapter.Status should reject %q", bad)
		}
		if err := d.Stop(ctx, bad); err == nil {
			t.Errorf("DockerAdapter.Stop should reject %q", bad)
		}
		if err := d.Logs(ctx, bad, LogOptions{}, &bytes.Buffer{}); err == nil {
			t.Errorf("DockerAdapter.Logs should reject %q", bad)
		}
		if _, err := d.Debug(ctx, DebugOptions{WorkloadName: bad}, nil, nil, nil); err == nil {
			t.Errorf("DockerAdapter.Debug should reject %q", bad)
		}
		if _, _, _, err := ResolveDependencyPreset("app", config.ServiceDependency{Name: bad, Type: "redis"}); err == nil {
			t.Errorf("ResolveDependencyPreset should reject dep name %q", bad)
		}
		if _, _, _, err := ResolveDependencyPreset("app", config.ServiceDependency{Name: "cache", Type: "redis", Image: bad}); err == nil {
			t.Errorf("ResolveDependencyPreset should reject dep image %q", bad)
		}

		if _, err := k.GenerateStandardManifests(&config.AppSpec{Name: bad}); err == nil {
			t.Errorf("GenerateStandardManifests should reject workload name %q", bad)
		}
		if _, err := k.GenerateStandardManifests(&config.AppSpec{Name: "app", Image: bad}); err == nil {
			t.Errorf("GenerateStandardManifests should reject image %q", bad)
		}
		if _, err := k.Status(ctx, bad); err == nil {
			t.Errorf("KubernetesAdapter.Status should reject %q", bad)
		}
		if err := k.Stop(ctx, bad); err == nil {
			t.Errorf("KubernetesAdapter.Stop should reject %q", bad)
		}
		if err := k.Logs(ctx, bad, LogOptions{}, &bytes.Buffer{}); err == nil {
			t.Errorf("KubernetesAdapter.Logs should reject workload %q", bad)
		}
		if err := k.Logs(ctx, "app", LogOptions{Container: bad}, &bytes.Buffer{}); err == nil {
			t.Errorf("KubernetesAdapter.Logs should reject container %q", bad)
		}
		if _, err := k.Debug(ctx, DebugOptions{TargetPod: bad}, nil, nil, nil); err == nil {
			t.Errorf("KubernetesAdapter.Debug should reject targetPod %q", bad)
		}
		if _, err := k.Debug(ctx, DebugOptions{TargetPod: "pod-1", TargetContainer: bad}, nil, nil, nil); err == nil {
			t.Errorf("KubernetesAdapter.Debug should reject targetContainer %q", bad)
		}
		if _, err := k.Debug(ctx, DebugOptions{TargetPod: "pod-1", DebugImage: bad}, nil, nil, nil); err == nil {
			t.Errorf("KubernetesAdapter.Debug should reject debugImage %q", bad)
		}
		badNS := NewKubernetesAdapter(bad)
		if _, err := badNS.Deploy(ctx, &config.AppSpec{Name: "app"}); err == nil {
			t.Errorf("KubernetesAdapter.Deploy should reject namespace %q", bad)
		}
	}
}

func TestBuildDockerPlansAndK8sUnitNormalization(t *testing.T) {
	spec := &config.AppSpec{
		Name:  "orders-api",
		Image: "orders-api:v1",
		Dependencies: config.DependenciesConfig{
			Services: []config.ServiceDependency{
				{Name: "db", Type: "postgres"},
			},
		},
		Runtime: config.RuntimeConfig{
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "0.25", Limit: "1.5"},
				Memory: config.ResourceLimit{Request: "256m", Limit: "1gb"},
			},
		},
	}

	deployPlan, err := BuildDockerDeployPlan(spec)
	if err != nil {
		t.Fatalf("BuildDockerDeployPlan failed: %v", err)
	}
	if deployPlan.Network != "skipper-orders-api-net" || len(deployPlan.Containers) != 2 {
		t.Fatalf("unexpected deployPlan: %+v", deployPlan)
	}
	if deployPlan.Containers[0] != "orders-api-db" || deployPlan.Containers[1] != "orders-api" {
		t.Fatalf("unexpected containers order: %v", deployPlan.Containers)
	}

	stopContainers, stopNet, stopCmds, err := BuildDockerStopPlan("orders-api", spec)
	if err != nil {
		t.Fatalf("BuildDockerStopPlan failed: %v", err)
	}
	if len(stopContainers) != 2 || stopNet != "skipper-orders-api-net" || len(stopCmds) != 4 {
		t.Fatalf("unexpected stop plan: containers=%v net=%q cmds=%v", stopContainers, stopNet, stopCmds)
	}

	k := NewKubernetesAdapter("prod")
	spec.Env = []config.EnvVar{
		{Name: "APP_ENV", Value: "production"},
	}
	spec.Security.ReadOnlyRootFilesystem = true
	yamlOut, err := k.GenerateStandardManifests(spec)
	if err != nil {
		t.Fatalf("GenerateStandardManifests failed: %v", err)
	}
	for _, want := range []string{
		"cpu: 250m",
		"memory: 256Mi",
		"cpu: 1500m",
		"memory: 1Gi",
		"name: APP_ENV",
		`value: "production"`,
		"readOnlyRootFilesystem: true",
		"mountPath: /tmp",
		"emptyDir: {}",
	} {
		if !strings.Contains(yamlOut, want) {
			t.Errorf("expected %q in generated K8s manifest:\n%s", want, yamlOut)
		}
	}
}
