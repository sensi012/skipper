package transpile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

type ComposeFile struct {
	Version  string                    `yaml:"version,omitempty"`
	Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
	Image       string            `yaml:"image,omitempty"`
	Build       interface{}       `yaml:"build,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Environment interface{}       `yaml:"environment,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Restart     string            `yaml:"restart,omitempty"`
	Deploy      *ComposeDeploy    `yaml:"deploy,omitempty"`
}

type ComposeDeploy struct {
	Replicas  int              `yaml:"replicas,omitempty"`
	Resources *ComposeResources `yaml:"resources,omitempty"`
}

type ComposeResources struct {
	Limits       *ComposeResourceLimit `yaml:"limits,omitempty"`
	Reservations *ComposeResourceLimit `yaml:"reservations,omitempty"`
}

type ComposeResourceLimit struct {
	Cpus   string `yaml:"cpus,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

type Transpiler struct{}

func NewTranspiler() *Transpiler {
	return &Transpiler{}
}

func (t *Transpiler) TranspileCompose(yamlBytes []byte, targetService string) (*config.AppSpec, error) {
	var compose ComposeFile
	if err := yaml.Unmarshal(yamlBytes, &compose); err != nil {
		return nil, fmt.Errorf("failed to parse docker-compose yaml: %w", err)
	}

	if len(compose.Services) == 0 {
		return nil, fmt.Errorf("no services defined in compose file")
	}

	svcName := targetService
	if svcName == "" {
		for name := range compose.Services {
			svcName = name
			break
		}
	}

	svc, ok := compose.Services[svcName]
	if !ok {
		return nil, fmt.Errorf("service '%s' not found in compose file", targetService)
	}

	spec := &config.AppSpec{
		Version:     "v1alpha1",
		Name:        svcName,
		Team:        "migrated",
		Environment: "development",
		Type:        "service",
		Image:       svc.Image,
		Security: config.SecurityConfig{
			RunAsNonRoot:           true,
			ReadOnlyRootFilesystem: false,
		},
		Runtime: config.RuntimeConfig{
			Replicas: config.ReplicaConfig{Min: 1, Max: 3},
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "100m", Limit: "500m"},
				Memory: config.ResourceLimit{Request: "128Mi", Limit: "512Mi"},
			},
		},
	}

	// 1. Build context
	if svc.Build != nil {
		switch b := svc.Build.(type) {
		case string:
			spec.Build = config.BuildConfig{Context: b}
		case map[string]interface{}:
			ctx, _ := b["context"].(string)
			dockerfile, _ := b["dockerfile"].(string)
			spec.Build = config.BuildConfig{Context: ctx, Dockerfile: dockerfile}
		}
	}

	// 2. Ports
	for _, p := range svc.Ports {
		hostPort, containerPort := parsePortMapping(p)
		if containerPort > 0 {
			spec.Endpoints = append(spec.Endpoints, config.EndpointConfig{
				Name:     fmt.Sprintf("port-%d", containerPort),
				Port:     containerPort,
				Protocol: "TCP",
				Public:   hostPort > 0,
			})
		}
	}

	// 3. Environment Variables
	if svc.Environment != nil {
		switch env := svc.Environment.(type) {
		case []interface{}:
			for _, item := range env {
				if s, ok := item.(string); ok {
					parts := strings.SplitN(s, "=", 2)
					val := ""
					if len(parts) == 2 {
						val = parts[1]
					}
					spec.Env = append(spec.Env, config.EnvVar{Name: parts[0], Value: val})
				}
			}
		case map[string]interface{}:
			for k, v := range env {
				spec.Env = append(spec.Env, config.EnvVar{Name: k, Value: fmt.Sprintf("%v", v)})
			}
		}
	}

	// 4. Volumes -> Storage dependencies
	for _, v := range svc.Volumes {
		parts := strings.Split(v, ":")
		mountPath := parts[0]
		if len(parts) >= 2 {
			mountPath = parts[1]
		}
		spec.Dependencies.Storage = append(spec.Dependencies.Storage, config.StorageDependency{
			Name:        fmt.Sprintf("vol-%d", len(spec.Dependencies.Storage)+1),
			MountPath:   mountPath,
			Size:        "5Gi",
			Persistence: "ReadWriteOnce",
		})
	}

	// 5. Deploy limits
	if svc.Deploy != nil && svc.Deploy.Replicas > 0 {
		spec.Runtime.Replicas.Min = svc.Deploy.Replicas
	}
	if svc.Deploy != nil && svc.Deploy.Resources != nil && svc.Deploy.Resources.Limits != nil {
		if svc.Deploy.Resources.Limits.Memory != "" {
			spec.Runtime.Resources.Memory.Limit = svc.Deploy.Resources.Limits.Memory
		}
	}

	return spec, nil
}

func parsePortMapping(portStr string) (int, int) {
	parts := strings.Split(portStr, ":")
	if len(parts) == 1 {
		p, _ := strconv.Atoi(parts[0])
		return 0, p
	}
	if len(parts) >= 2 {
		h, _ := strconv.Atoi(parts[0])
		c, _ := strconv.Atoi(parts[1])
		return h, c
	}
	return 0, 0
}
