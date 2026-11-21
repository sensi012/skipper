package transpile

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

type ComposeFile struct {
	Name     string                    `yaml:"name,omitempty"`
	Version  string                    `yaml:"version,omitempty"`
	Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
	Image       string         `yaml:"image,omitempty"`
	Build       interface{}    `yaml:"build,omitempty"`
	Ports       []interface{}  `yaml:"ports,omitempty"`
	Environment interface{}    `yaml:"environment,omitempty"`
	Volumes     []interface{}  `yaml:"volumes,omitempty"`
	Restart     string         `yaml:"restart,omitempty"`
	DependsOn   interface{}    `yaml:"depends_on,omitempty"`
	Deploy      *ComposeDeploy `yaml:"deploy,omitempty"`
}

type ComposeDeploy struct {
	Replicas  int               `yaml:"replicas,omitempty"`
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
		names := sortedServiceNames(compose.Services)
		svcName = names[0]
		for _, n := range names {
			if compose.Services[n].Build != nil {
				svcName = n
				break
			}
		}
	}

	svc, ok := compose.Services[svcName]
	if !ok {
		return nil, fmt.Errorf("service '%s' not found in compose file", targetService)
	}
	if strings.TrimSpace(svcName) == "" || strings.HasPrefix(strings.TrimSpace(svcName), "-") || strings.ContainsAny(svcName, " \t\r\n/\\") {
		return nil, fmt.Errorf("invalid compose service name %q", svcName)
	}

	hasBuild, ctx, dockerfile, target, args := parseBuildConfig(svc.Build, os.LookupEnv, false)
	var buildCfg config.BuildConfig
	if hasBuild {
		buildCfg = config.BuildConfig{
			Context:    ctx,
			Dockerfile: dockerfile,
			Args:       args,
			Target:     target,
		}
	}

	return serviceToSpec(svcName, svc, buildCfg, svc.Image, os.LookupEnv), nil
}

func sortedServiceNames(services map[string]ComposeService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func serviceToSpec(name string, svc ComposeService, buildCfg config.BuildConfig, image string, lookup func(string) (string, bool)) *config.AppSpec {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	spec := &config.AppSpec{
		Version:     "v1alpha1",
		Name:        name,
		Team:        "migrated",
		Environment: "development",
		Type:        "service",
		Image:       image,
		Build:       buildCfg,
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

	// Ports
	for _, p := range svc.Ports {
		hostPort, containerPort := parsePortEntry(p)
		if containerPort > 0 {
			spec.Endpoints = append(spec.Endpoints, config.EndpointConfig{
				Name:     fmt.Sprintf("port-%d", containerPort),
				Port:     containerPort,
				Protocol: "TCP",
				Public:   hostPort > 0,
			})
		}
	}

	// Environment Variables
	spec.Env = parseEnvironment(svc.Environment, lookup)

	// Volumes -> Storage dependencies
	for _, v := range svc.Volumes {
		mountPath := parseVolumeEntry(v)
		if mountPath == "" {
			continue
		}
		spec.Dependencies.Storage = append(spec.Dependencies.Storage, config.StorageDependency{
			Name:        fmt.Sprintf("vol-%d", len(spec.Dependencies.Storage)+1),
			MountPath:   mountPath,
			Size:        "5Gi",
			Persistence: "ReadWriteOnce",
		})
	}

	// Deploy limits
	if svc.Deploy != nil && svc.Deploy.Replicas > 0 {
		spec.Runtime.Replicas.Min = svc.Deploy.Replicas
	}
	if svc.Deploy != nil && svc.Deploy.Resources != nil && svc.Deploy.Resources.Limits != nil {
		if svc.Deploy.Resources.Limits.Memory != "" {
			spec.Runtime.Resources.Memory.Limit = svc.Deploy.Resources.Limits.Memory
		}
	}

	return spec
}

func parseBuildConfig(rawBuild interface{}, lookup func(string) (string, bool), doInterpolate bool) (hasBuild bool, context, dockerfile, target string, args map[string]string) {
	if rawBuild == nil {
		return false, "", "", "", nil
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	interp := func(s string) string {
		if doInterpolate {
			return interpolateVars(s, lookup)
		}
		return s
	}

	switch b := rawBuild.(type) {
	case string:
		return true, interp(b), "", "", nil
	case map[string]interface{}:
		rawCtx, _ := b["context"].(string)
		rawDF, _ := b["dockerfile"].(string)
		rawTarget, _ := b["target"].(string)
		parsedArgs := parseBuildArgs(b["args"], lookup, doInterpolate)
		return true, interp(rawCtx), interp(rawDF), interp(rawTarget), parsedArgs
	case map[interface{}]interface{}:
		norm := make(map[string]interface{}, len(b))
		for k, v := range b {
			norm[fmt.Sprintf("%v", k)] = v
		}
		return parseBuildConfig(norm, lookup, doInterpolate)
	default:
		return false, "", "", "", nil
	}
}

func parseBuildArgs(rawArgs interface{}, lookup func(string) (string, bool), doInterpolate bool) map[string]string {
	if rawArgs == nil {
		return nil
	}
	interp := func(s string) string {
		if doInterpolate {
			return interpolateVars(s, lookup)
		}
		return s
	}

	out := make(map[string]string)
	switch a := rawArgs.(type) {
	case map[string]interface{}:
		for k, v := range a {
			if v == nil {
				if val, ok := lookup(k); ok {
					out[k] = val
				}
				continue
			}
			out[k] = interp(fmt.Sprintf("%v", v))
		}
	case map[interface{}]interface{}:
		for k, v := range a {
			key := fmt.Sprintf("%v", k)
			if v == nil {
				if val, ok := lookup(key); ok {
					out[key] = val
				}
				continue
			}
			out[key] = interp(fmt.Sprintf("%v", v))
		}
	case []interface{}:
		for _, item := range a {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if idx := strings.Index(s, "="); idx != -1 {
				key := strings.TrimSpace(s[:idx])
				val := s[idx+1:]
				if key != "" {
					out[key] = interp(val)
				}
			} else {
				key := strings.TrimSpace(s)
				if key != "" {
					if val, found := lookup(key); found {
						out[key] = val
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseEnvironment(rawEnv interface{}, lookup func(string) (string, bool)) []config.EnvVar {
	if rawEnv == nil {
		return nil
	}
	var out []config.EnvVar
	switch env := rawEnv.(type) {
	case []interface{}:
		for _, item := range env {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if idx := strings.Index(s, "="); idx != -1 {
				out = append(out, config.EnvVar{Name: s[:idx], Value: s[idx+1:]})
			} else {
				key := strings.TrimSpace(s)
				if key != "" {
					if val, found := lookup(key); found {
						out = append(out, config.EnvVar{Name: key, Value: val})
					}
				}
			}
		}
	case map[string]interface{}:
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := env[k]
			if v == nil {
				if val, found := lookup(k); found {
					out = append(out, config.EnvVar{Name: k, Value: val})
				}
				continue
			}
			out = append(out, config.EnvVar{Name: k, Value: fmt.Sprintf("%v", v)})
		}
	case map[interface{}]interface{}:
		norm := make(map[string]interface{}, len(env))
		for k, v := range env {
			norm[fmt.Sprintf("%v", k)] = v
		}
		return parseEnvironment(norm, lookup)
	}
	return out
}

func parsePortEntry(entry interface{}) (int, int) {
	switch p := entry.(type) {
	case string:
		return parsePortMapping(p)
	case int:
		return 0, p
	case int64:
		return 0, int(p)
	case uint64:
		return 0, int(p)
	case float64:
		return 0, int(p)
	case map[string]interface{}:
		target := toInt(p["target"])
		published := toInt(p["published"])
		return published, target
	case map[interface{}]interface{}:
		norm := make(map[string]interface{}, len(p))
		for k, v := range p {
			norm[fmt.Sprintf("%v", k)] = v
		}
		return parsePortEntry(norm)
	default:
		return 0, 0
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	case string:
		s := strings.TrimSpace(n)
		if idx := strings.Index(s, "/"); idx != -1 {
			s = s[:idx]
		}
		val, _ := strconv.Atoi(strings.TrimSpace(s))
		return val
	default:
		return 0
	}
}

func parsePortMapping(portStr string) (int, int) {
	s := strings.TrimSpace(portStr)
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	parts := strings.Split(s, ":")
	if len(parts) == 1 {
		c, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		return 0, c
	}
	if len(parts) >= 2 {
		h, _ := strconv.Atoi(strings.TrimSpace(parts[len(parts)-2]))
		c, _ := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1]))
		return h, c
	}
	return 0, 0
}

func parseVolumeEntry(entry interface{}) string {
	switch v := entry.(type) {
	case string:
		parts := strings.Split(v, ":")
		if len(parts) == 1 {
			return parts[0]
		}
		if len(parts) >= 3 && len(parts[0]) == 1 && (strings.HasPrefix(parts[1], `\`) || strings.HasPrefix(parts[1], `/`)) {
			return parts[2]
		}
		return parts[1]
	case map[string]interface{}:
		target, _ := v["target"].(string)
		return target
	case map[interface{}]interface{}:
		for k, val := range v {
			if fmt.Sprintf("%v", k) == "target" {
				s, _ := val.(string)
				return s
			}
		}
		return ""
	default:
		return ""
	}
}
