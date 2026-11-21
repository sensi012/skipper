package cluster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

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

func TestContextManager(t *testing.T) {
	rawContexts := "*   prod-us-east   prod-cluster   admin-prod   payments\r\n" +
		"    staging-eu     stage-cluster  dev-user\r\n"

	t.Run("ListContexts parses current, namespace, and defaults", func(t *testing.T) {
		var calls [][]string
		cm := NewContextManager()
		cm.execCommand = makeFakeExec([]fakeStep{{stdout: rawContexts}}, &calls)

		list, err := cm.ListContexts(context.Background())
		if err != nil {
			t.Fatalf("ListContexts failed: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("expected 2 contexts, got %d", len(list))
		}
		if !list[0].Current || list[0].Name != "prod-us-east" || list[0].Cluster != "prod-cluster" || list[0].User != "admin-prod" || list[0].Namespace != "payments" {
			t.Fatalf("unexpected first context: %+v", list[0])
		}
		if list[1].Current || list[1].Name != "staging-eu" || list[1].Namespace != "default" {
			t.Fatalf("unexpected second context: %+v", list[1])
		}
		if strings.Join(calls[0], " ") != "kubectl config get-contexts --no-headers" {
			t.Fatalf("unexpected command: %v", calls[0])
		}
	})

	t.Run("CurrentContext and UseContext", func(t *testing.T) {
		var calls [][]string
		cm := NewContextManager()
		cm.execCommand = makeFakeExec([]fakeStep{
			{stdout: "prod-us-east\n"},
			{stdout: "Switched to context \"staging-eu\".\n"},
		}, &calls)

		cur, err := cm.CurrentContext(context.Background())
		if err != nil || cur != "prod-us-east" {
			t.Fatalf("expected prod-us-east, got %q (err=%v)", cur, err)
		}
		if err := cm.UseContext(context.Background(), "staging-eu"); err != nil {
			t.Fatalf("UseContext failed: %v", err)
		}
		if strings.Join(calls[1], " ") != "kubectl config use-context staging-eu" {
			t.Fatalf("unexpected use-context args: %v", calls[1])
		}

		if err := cm.UseContext(context.Background(), "   "); err == nil {
			t.Fatalf("expected error for empty context name")
		}
	})

	t.Run("FleetStatus probes reachability and server URL", func(t *testing.T) {
		cm := NewContextManager()
		cm.execCommand = makeFakeExec([]fakeStep{
			{stdout: rawContexts},
			{stdout: "Kubernetes control plane is running at https://10.0.0.1:6443\n", exitCode: 0},
			{stderr: "connection refused", exitCode: 1},
		}, nil)

		summary, err := cm.FleetStatus(context.Background())
		if err != nil {
			t.Fatalf("FleetStatus failed: %v", err)
		}
		if summary.CurrentContext != "prod-us-east" || summary.TotalClusters != 2 || summary.ReachableCount != 1 {
			t.Fatalf("unexpected summary: %+v", summary)
		}
		if !summary.Contexts[0].Reachable || summary.Contexts[0].Server != "https://10.0.0.1:6443" {
			t.Fatalf("expected prod reachable with server URL, got %+v", summary.Contexts[0])
		}
		if summary.Contexts[1].Reachable {
			t.Fatalf("expected staging unreachable, got %+v", summary.Contexts[1])
		}
	})

	t.Run("error propagation", func(t *testing.T) {
		cm := &ContextManager{
			execCommand: makeFakeExec([]fakeStep{
				{stderr: "kubeconfig missing", exitCode: 1},
				{stderr: "no current context", exitCode: 1},
				{stderr: "context not found", exitCode: 1},
				{stderr: "list failed", exitCode: 1},
			}, nil),
		}
		if _, err := cm.ListContexts(context.Background()); err == nil {
			t.Fatalf("expected ListContexts error")
		}
		if _, err := cm.CurrentContext(context.Background()); err == nil {
			t.Fatalf("expected CurrentContext error")
		}
		if err := cm.UseContext(context.Background(), "bad"); err == nil {
			t.Fatalf("expected UseContext error")
		}
		if _, err := cm.FleetStatus(context.Background()); err == nil {
			t.Fatalf("expected FleetStatus error")
		}
	})
}

func TestDriftDetectorDetectDrift(t *testing.T) {
	desired := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: api
        image: ghcr.io/acme/api:v2.0
        ports:
        - name: http
          containerPort: 8080
        env:
        - name: LOG_LEVEL
          value: info
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
---
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  ports:
  - name: http
    port: 8080
    targetPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: metrics-svc
spec:
  ports:
  - name: metrics
    port: 9090
    targetPort: 9090
`

	t.Run("no drift when desired matches live", func(t *testing.T) {
		dd := NewDriftDetector()
		rep, err := dd.DetectDrift(desired, desired)
		if err != nil {
			t.Fatalf("DetectDrift failed: %v", err)
		}
		if rep.HasDrift || len(rep.Drifts) != 0 {
			t.Fatalf("expected no drift, got %+v", rep.Drifts)
		}
	})

	t.Run("detects Image, Replicas, Ports, Resources, Env, MissingInLive, ExtraInLive", func(t *testing.T) {
		live := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: api
        image: ghcr.io/acme/api:v1.9
        ports:
        - name: http
          containerPort: 8081
        env:
        - name: LOG_LEVEL
          value: debug
        resources:
          requests:
            cpu: 200m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
---
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  ports:
  - name: http
    port: 80
    targetPort: 8081
---
apiVersion: v1
kind: Service
metadata:
  name: stray-svc
spec:
  ports:
  - name: http
    port: 9999
    targetPort: 9999
`
		dd := NewDriftDetector()
		rep, err := dd.DetectDrift(desired, live)
		if err != nil {
			t.Fatalf("DetectDrift failed: %v", err)
		}
		if !rep.HasDrift {
			t.Fatalf("expected drift to be detected")
		}

		seen := map[DriftType]int{}
		for _, dr := range rep.Drifts {
			seen[dr.DriftType]++
		}
		for _, wantType := range []DriftType{
			DriftReplicas, DriftImage, DriftPorts, DriftResources, DriftEnv, DriftMissingInLive, DriftExtraInLive,
		} {
			if seen[wantType] == 0 {
				t.Errorf("expected drift type %s, got drifts: %+v", wantType, rep.Drifts)
			}
		}
	})

	t.Run("invalid YAML returns error", func(t *testing.T) {
		dd := NewDriftDetector()
		if _, err := dd.DetectDrift(":::bad\n\t[yaml", ""); err == nil {
			t.Fatalf("expected error for invalid desired YAML")
		}
		if _, err := dd.DetectDrift("", ":::bad\n\t[yaml"); err == nil {
			t.Fatalf("expected error for invalid live YAML")
		}
	})
}

func TestDriftDetectorDetectClusterDrift(t *testing.T) {
	spec := &config.AppSpec{
		Name:  "checkout",
		Image: "ghcr.io/acme/checkout:v1",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
	}

	t.Run("fetches live deployment and reports missing service", func(t *testing.T) {
		var calls [][]string
		liveDep := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: checkout
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: checkout
        image: ghcr.io/acme/checkout:v0
        ports:
        - name: http
          containerPort: 8080
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
`
		dd := NewDriftDetector()
		dd.execCommand = makeFakeExec([]fakeStep{
			{stdout: liveDep, exitCode: 0},
			{stderr: `Error from server (NotFound): services "checkout" not found`, exitCode: 1},
		}, &calls)

		rep, err := dd.DetectClusterDrift(context.Background(), spec, "prod", "prod-us-east")
		if err != nil {
			t.Fatalf("DetectClusterDrift failed: %v", err)
		}
		if !rep.HasDrift || rep.Context != "prod-us-east" || rep.Namespace != "prod" || rep.Workload != "checkout" {
			t.Fatalf("unexpected report metadata: %+v", rep)
		}
		if len(rep.Drifts) != 2 {
			t.Fatalf("expected 2 drifts (Image + MissingInLive Service), got %+v", rep.Drifts)
		}
		if strings.Join(calls[0], " ") != "kubectl --context prod-us-east get deployment checkout -n prod -o yaml" {
			t.Fatalf("unexpected kubectl get deployment args: %v", calls[0])
		}
	})

	t.Run("validation and non-NotFound kubectl errors", func(t *testing.T) {
		dd := NewDriftDetector()
		if _, err := dd.DetectClusterDrift(context.Background(), nil, "", ""); err == nil {
			t.Fatalf("expected error for nil spec")
		}

		dd.execCommand = makeFakeExec([]fakeStep{{stderr: "forbidden", exitCode: 1}}, nil)
		if _, err := dd.DetectClusterDrift(context.Background(), spec, "", ""); err == nil || !strings.Contains(err.Error(), "failed to fetch live") {
			t.Fatalf("expected fetch error, got %v", err)
		}
	})
}
