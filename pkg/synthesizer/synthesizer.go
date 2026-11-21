package synthesizer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
)

// ManifestBundle holds all generated production-ready Kubernetes YAML manifests
type ManifestBundle struct {
	Deployment    string
	Service       string
	ConfigMap     string
	NetworkPolicy string
	PodDisruption string
}

// FullYaml concatenates all manifests into a single multi-document YAML stream
func (m *ManifestBundle) FullYaml() string {
	var buf bytes.Buffer
	docs := []string{m.Deployment, m.Service, m.ConfigMap, m.NetworkPolicy, m.PodDisruption}
	first := true
	for _, doc := range docs {
		if doc == "" {
			continue
		}
		if !first {
			buf.WriteString("\n---\n")
		}
		buf.WriteString(doc)
		first = false
	}
	return buf.String()
}

// Synthesizer compiles declarative AppSpec into hardened Kubernetes manifests
type Synthesizer struct {
	DefaultCPURequest    string
	DefaultCPULimit      string
	DefaultMemoryRequest string
	DefaultMemoryLimit   string
}

// NewSynthesizer creates a synthesizer with battle-tested production defaults
func NewSynthesizer() *Synthesizer {
	return &Synthesizer{
		DefaultCPURequest:    "100m",
		DefaultCPULimit:      "500m",
		DefaultMemoryRequest: "128Mi",
		DefaultMemoryLimit:   "512Mi",
	}
}

// Synthesize transforms an AppSpec into a complete, secure Kubernetes manifest bundle
func (s *Synthesizer) Synthesize(spec *config.AppSpec, namespace string) (*ManifestBundle, error) {
	if spec == nil {
		return nil, fmt.Errorf("app spec must not be nil")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("app spec name must not be empty")
	}
	if strings.ContainsAny(spec.Name, "\r\n\x00/\\") || strings.HasPrefix(strings.TrimSpace(spec.Name), "-") {
		return nil, fmt.Errorf("invalid app spec name %q", spec.Name)
	}

	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	}
	if strings.ContainsAny(namespace, "\r\n\x00/\\ ") || strings.HasPrefix(namespace, "-") {
		return nil, fmt.Errorf("invalid namespace %q", namespace)
	}

	if s == nil {
		s = NewSynthesizer()
	}

	bundle := &ManifestBundle{}
	bundle.Deployment = s.generateDeployment(spec, namespace)

	if len(spec.Endpoints) > 0 {
		bundle.Service = s.generateService(spec, namespace)
		bundle.NetworkPolicy = s.generateNetworkPolicy(spec, namespace)
	}

	bundle.PodDisruption = s.generatePodDisruptionBudget(spec, namespace)
	return bundle, nil
}

func (s *Synthesizer) generateDeployment(spec *config.AppSpec, ns string) string {
	replicas := 1
	if spec.Runtime.Replicas.Min > 0 {
		replicas = spec.Runtime.Replicas.Min
	}

	image := spec.Image
	if image == "" {
		image = fmt.Sprintf("%s:latest", spec.Name)
	}

	cpuReq := s.DefaultCPURequest
	if cpuReq == "" {
		cpuReq = "100m"
	}
	if spec.Runtime.Resources.CPU.Request != "" {
		cpuReq = spec.Runtime.Resources.CPU.Request
	}
	cpuLim := s.DefaultCPULimit
	if cpuLim == "" {
		cpuLim = "500m"
	}
	if spec.Runtime.Resources.CPU.Limit != "" {
		cpuLim = spec.Runtime.Resources.CPU.Limit
	}

	memReq := s.DefaultMemoryRequest
	if memReq == "" {
		memReq = "128Mi"
	}
	if spec.Runtime.Resources.Memory.Request != "" {
		memReq = spec.Runtime.Resources.Memory.Request
	}
	memLim := s.DefaultMemoryLimit
	if memLim == "" {
		memLim = "512Mi"
	}
	if spec.Runtime.Resources.Memory.Limit != "" {
		memLim = spec.Runtime.Resources.Memory.Limit
	}

	var buf bytes.Buffer
	buf.WriteString("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n  namespace: %s\n  labels:\n    app: %s\n", spec.Name, ns, spec.Name))
	buf.WriteString("spec:\n")
	buf.WriteString(fmt.Sprintf("  replicas: %d\n  selector:\n    matchLabels:\n      app: %s\n", replicas, spec.Name))
	buf.WriteString("  template:\n    metadata:\n      labels:\n")
	buf.WriteString(fmt.Sprintf("        app: %s\n    spec:\n", spec.Name))

	// Pod security standards: restricted
	buf.WriteString("      securityContext:\n")
	buf.WriteString("        runAsNonRoot: true\n        seccompProfile:\n          type: RuntimeDefault\n")
	buf.WriteString("      containers:\n")
	buf.WriteString(fmt.Sprintf("      - name: %s\n        image: %s\n        imagePullPolicy: IfNotPresent\n", spec.Name, image))
	buf.WriteString("        securityContext:\n")
	buf.WriteString("          allowPrivilegeEscalation: false\n")
	buf.WriteString("          readOnlyRootFilesystem: true\n")
	buf.WriteString("          capabilities:\n            drop:\n            - ALL\n")

	// Ports
	if len(spec.Endpoints) > 0 {
		buf.WriteString("        ports:\n")
		for _, ep := range spec.Endpoints {
			buf.WriteString(fmt.Sprintf("        - containerPort: %d\n          name: %s\n", ep.Port, ep.Name))
		}
	}

	// Environment variables
	if len(spec.Env) > 0 {
		buf.WriteString("        env:\n")
		for _, env := range spec.Env {
			if env.Value != "" {
				buf.WriteString(fmt.Sprintf("        - name: %s\n          value: %q\n", env.Name, env.Value))
			}
		}
	}

	// Resources: requests and limits
	buf.WriteString("        resources:\n          requests:\n")
	buf.WriteString(fmt.Sprintf("            cpu: %s\n            memory: %s\n", cpuReq, memReq))
	buf.WriteString("          limits:\n")
	buf.WriteString(fmt.Sprintf("            cpu: %s\n            memory: %s\n", cpuLim, memLim))

	// Health probes
	if len(spec.Endpoints) > 0 {
		defaultPort := spec.Endpoints[0].Port
		buf.WriteString("        readinessProbe:\n          httpGet:\n")
		buf.WriteString(fmt.Sprintf("            path: /healthz\n            port: %d\n", defaultPort))
		buf.WriteString("          initialDelaySeconds: 5\n          periodSeconds: 10\n")

		buf.WriteString("        livenessProbe:\n          httpGet:\n")
		buf.WriteString(fmt.Sprintf("            path: /livez\n            port: %d\n", defaultPort))
		buf.WriteString("          initialDelaySeconds: 10\n          periodSeconds: 15\n")
	}

	return buf.String()
}

func (s *Synthesizer) generateService(spec *config.AppSpec, ns string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: v1\nkind: Service\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n  namespace: %s\n  labels:\n    app: %s\n", spec.Name, ns, spec.Name))
	buf.WriteString("spec:\n  type: ClusterIP\n  selector:\n")
	buf.WriteString(fmt.Sprintf("    app: %s\n  ports:\n", spec.Name))

	for _, ep := range spec.Endpoints {
		buf.WriteString(fmt.Sprintf("  - port: %d\n    targetPort: %d\n    name: %s\n    protocol: %s\n",
			ep.Port, ep.Port, ep.Name, "TCP"))
	}
	return buf.String()
}

func (s *Synthesizer) generateNetworkPolicy(spec *config.AppSpec, ns string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-isolation\n  namespace: %s\nspec:\n  podSelector:\n", spec.Name, ns))
	buf.WriteString(fmt.Sprintf("    matchLabels:\n      app: %s\n", spec.Name))
	buf.WriteString("  policyTypes:\n  - Ingress\n  - Egress\n  ingress:\n  - from:\n    - podSelector: {}\n")
	buf.WriteString("    ports:\n")
	for _, ep := range spec.Endpoints {
		buf.WriteString(fmt.Sprintf("    - protocol: TCP\n      port: %d\n", ep.Port))
	}
	buf.WriteString("  egress:\n  - to:\n    - namespaceSelector: {}\n") // DNS and cross-service resolution
	return buf.String()
}

func (s *Synthesizer) generatePodDisruptionBudget(spec *config.AppSpec, ns string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-pdb\n  namespace: %s\nspec:\n  minAvailable: 1\n  selector:\n", spec.Name, ns))
	buf.WriteString(fmt.Sprintf("    matchLabels:\n      app: %s\n", spec.Name))
	return buf.String()
}
