package cluster

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/runtime"
	"gopkg.in/yaml.v3"
)

// DriftType classifies a detected configuration drift between desired and live Kubernetes state.
type DriftType string

const (
	DriftImage         DriftType = "Image"
	DriftReplicas      DriftType = "Replicas"
	DriftPorts         DriftType = "Ports"
	DriftResources     DriftType = "Resources"
	DriftEnv           DriftType = "Env"
	DriftMissingInLive DriftType = "MissingInLive"
	DriftExtraInLive   DriftType = "ExtraInLive"
)

// ResourceDrift represents a single deterministic field-level or existence diff.
type ResourceDrift struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Field     string    `json:"field"`
	DriftType DriftType `json:"driftType"`
	Desired   string    `json:"desired,omitempty"`
	Live      string    `json:"live,omitempty"`
}

// DriftReport aggregates all detected drift entries for a workload or manifest set.
type DriftReport struct {
	Context   string          `json:"context,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Workload  string          `json:"workload,omitempty"`
	HasDrift  bool            `json:"hasDrift"`
	Drifts    []ResourceDrift `json:"drifts"`
	CheckedAt time.Time       `json:"checkedAt"`
}

// DriftDetector compares desired Kubernetes manifests against live cluster manifests.
type DriftDetector struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewDriftDetector constructs a DriftDetector.
func NewDriftDetector() *DriftDetector {
	return &DriftDetector{
		execCommand: exec.CommandContext,
	}
}

func (d *DriftDetector) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if d.execCommand != nil {
		return d.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

type k8sDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas *int `yaml:"replicas"`
		Template struct {
			Spec struct {
				Containers []k8sContainer `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
		Ports []k8sServicePort `yaml:"ports"`
	} `yaml:"spec"`
}

type k8sContainer struct {
	Name  string `yaml:"name"`
	Image string `yaml:"image"`
	Ports []struct {
		Name          string `yaml:"name"`
		ContainerPort int    `yaml:"containerPort"`
	} `yaml:"ports"`
	Env []struct {
		Name  string `yaml:"name"`
		Value string `yaml:"value"`
	} `yaml:"env"`
	Resources struct {
		Requests map[string]string `yaml:"requests"`
		Limits   map[string]string `yaml:"limits"`
	} `yaml:"resources"`
}

type k8sServicePort struct {
	Name       string      `yaml:"name"`
	Port       int         `yaml:"port"`
	TargetPort interface{} `yaml:"targetPort"`
}

func parseK8sDocuments(raw string) (map[string]k8sDoc, error) {
	docs := make(map[string]k8sDoc)
	if strings.TrimSpace(raw) == "" {
		return docs, nil
	}
	dec := yaml.NewDecoder(strings.NewReader(raw))
	for {
		var doc k8sDoc
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse kubernetes YAML: %w", err)
		}
		if doc.Kind == "" || doc.Metadata.Name == "" {
			continue
		}
		key := doc.Kind + "/" + doc.Metadata.Name
		docs[key] = doc
	}
	return docs, nil
}

// DetectDrift compares desired multi-document Kubernetes YAML against live YAML
// and computes deterministic field-level diffs.
func (d *DriftDetector) DetectDrift(desiredYAML, liveYAML string) (*DriftReport, error) {
	desiredDocs, err := parseK8sDocuments(desiredYAML)
	if err != nil {
		return nil, fmt.Errorf("invalid desired YAML: %w", err)
	}
	liveDocs, err := parseK8sDocuments(liveYAML)
	if err != nil {
		return nil, fmt.Errorf("invalid live YAML: %w", err)
	}

	keySet := make(map[string]struct{}, len(desiredDocs)+len(liveDocs))
	for k := range desiredDocs {
		keySet[k] = struct{}{}
	}
	for k := range liveDocs {
		keySet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	drifts := make([]ResourceDrift, 0)
	for _, key := range keys {
		des, hasDes := desiredDocs[key]
		live, hasLive := liveDocs[key]

		if hasDes && !hasLive {
			drifts = append(drifts, ResourceDrift{
				Kind:      des.Kind,
				Name:      des.Metadata.Name,
				Field:     "resource",
				DriftType: DriftMissingInLive,
				Desired:   key,
				Live:      "<missing>",
			})
			continue
		}
		if !hasDes && hasLive {
			drifts = append(drifts, ResourceDrift{
				Kind:      live.Kind,
				Name:      live.Metadata.Name,
				Field:     "resource",
				DriftType: DriftExtraInLive,
				Desired:   "<absent>",
				Live:      key,
			})
			continue
		}

		switch des.Kind {
		case "Deployment":
			drifts = append(drifts, diffDeployment(des, live)...)
		case "Service":
			drifts = append(drifts, diffService(des, live)...)
		}
	}

	return &DriftReport{
		HasDrift:  len(drifts) > 0,
		Drifts:    drifts,
		CheckedAt: time.Now().UTC(),
	}, nil
}

func diffDeployment(des, live k8sDoc) []ResourceDrift {
	var drifts []ResourceDrift
	name := des.Metadata.Name

	desRep := 1
	if des.Spec.Replicas != nil {
		desRep = *des.Spec.Replicas
	}
	liveRep := 1
	if live.Spec.Replicas != nil {
		liveRep = *live.Spec.Replicas
	}
	if desRep != liveRep {
		drifts = append(drifts, ResourceDrift{
			Kind:      "Deployment",
			Name:      name,
			Field:     "spec.replicas",
			DriftType: DriftReplicas,
			Desired:   strconv.Itoa(desRep),
			Live:      strconv.Itoa(liveRep),
		})
	}

	if len(des.Spec.Template.Spec.Containers) == 0 && len(live.Spec.Template.Spec.Containers) == 0 {
		return drifts
	}

	var dc, lc k8sContainer
	if len(des.Spec.Template.Spec.Containers) > 0 {
		dc = des.Spec.Template.Spec.Containers[0]
	}
	if len(live.Spec.Template.Spec.Containers) > 0 {
		lc = live.Spec.Template.Spec.Containers[0]
		for _, c := range live.Spec.Template.Spec.Containers {
			if c.Name == dc.Name {
				lc = c
				break
			}
		}
	}

	if dc.Image != lc.Image {
		drifts = append(drifts, ResourceDrift{
			Kind:      "Deployment",
			Name:      name,
			Field:     "spec.template.spec.containers[0].image",
			DriftType: DriftImage,
			Desired:   dc.Image,
			Live:      lc.Image,
		})
	}

	desPorts := formatContainerPorts(dc)
	livePorts := formatContainerPorts(lc)
	if desPorts != livePorts {
		drifts = append(drifts, ResourceDrift{
			Kind:      "Deployment",
			Name:      name,
			Field:     "spec.template.spec.containers[0].ports",
			DriftType: DriftPorts,
			Desired:   desPorts,
			Live:      livePorts,
		})
	}

	desRes := formatResources(dc)
	liveRes := formatResources(lc)
	if desRes != liveRes {
		drifts = append(drifts, ResourceDrift{
			Kind:      "Deployment",
			Name:      name,
			Field:     "spec.template.spec.containers[0].resources",
			DriftType: DriftResources,
			Desired:   desRes,
			Live:      liveRes,
		})
	}

	desEnv := formatEnv(dc)
	liveEnv := formatEnv(lc)
	if desEnv != liveEnv {
		drifts = append(drifts, ResourceDrift{
			Kind:      "Deployment",
			Name:      name,
			Field:     "spec.template.spec.containers[0].env",
			DriftType: DriftEnv,
			Desired:   desEnv,
			Live:      liveEnv,
		})
	}

	return drifts
}

func diffService(des, live k8sDoc) []ResourceDrift {
	desPorts := formatServicePorts(des.Spec.Ports)
	livePorts := formatServicePorts(live.Spec.Ports)
	if desPorts == livePorts {
		return nil
	}
	return []ResourceDrift{
		{
			Kind:      "Service",
			Name:      des.Metadata.Name,
			Field:     "spec.ports",
			DriftType: DriftPorts,
			Desired:   desPorts,
			Live:      livePorts,
		},
	}
}

func formatContainerPorts(c k8sContainer) string {
	items := make([]string, 0, len(c.Ports))
	for _, p := range c.Ports {
		items = append(items, fmt.Sprintf("%s:%d", p.Name, p.ContainerPort))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func formatServicePorts(ports []k8sServicePort) string {
	items := make([]string, 0, len(ports))
	for _, p := range ports {
		items = append(items, fmt.Sprintf("%s:%d->%v", p.Name, p.Port, p.TargetPort))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func formatResources(c k8sContainer) string {
	return fmt.Sprintf("req(cpu=%s,mem=%s) lim(cpu=%s,mem=%s)",
		c.Resources.Requests["cpu"],
		c.Resources.Requests["memory"],
		c.Resources.Limits["cpu"],
		c.Resources.Limits["memory"],
	)
}

func formatEnv(c k8sContainer) string {
	items := make([]string, 0, len(c.Env))
	for _, e := range c.Env {
		items = append(items, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// DetectClusterDrift synthesizes desired manifests from spec, fetches live manifests from the
// target Kubernetes cluster/context, and computes a DriftReport.
func (d *DriftDetector) DetectClusterDrift(ctx context.Context, spec *config.AppSpec, namespace, contextName string) (*DriftReport, error) {
	if spec == nil || strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("app spec with a non-empty name is required")
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}

	k8s := runtime.NewKubernetesAdapter(namespace)
	desiredYAML, err := k8s.GenerateStandardManifests(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate desired manifests: %w", err)
	}

	var liveParts []string
	depYAML, err := d.fetchLiveResource(ctx, "deployment", spec.Name, namespace, contextName)
	if err != nil {
		return nil, err
	}
	if depYAML != "" {
		liveParts = append(liveParts, depYAML)
	}

	if len(spec.Endpoints) > 0 {
		svcYAML, err := d.fetchLiveResource(ctx, "service", spec.Name, namespace, contextName)
		if err != nil {
			return nil, err
		}
		if svcYAML != "" {
			liveParts = append(liveParts, svcYAML)
		}
	}

	report, err := d.DetectDrift(desiredYAML, strings.Join(liveParts, "\n---\n"))
	if err != nil {
		return nil, err
	}
	report.Context = contextName
	report.Namespace = namespace
	report.Workload = spec.Name
	return report, nil
}

func (d *DriftDetector) fetchLiveResource(ctx context.Context, kind, name, namespace, contextName string) (string, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(contextName) != "" {
		args = append(args, "--context", strings.TrimSpace(contextName))
	}
	args = append(args, "get", kind, name, "-n", namespace, "-o", "yaml")

	cmd := d.command(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "notfound") || strings.Contains(lower, "not found") {
			return "", nil
		}
		return "", fmt.Errorf("failed to fetch live %s/%s: %w (%s)", kind, name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
