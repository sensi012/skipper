package bootstrapper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

type InitOptions struct {
	Name        string
	Team        string
	Port        int
	Environment string
	SourceDir   string
}

type ProjectInitializer struct {
	builder *build.CNBBuilder
}

func NewProjectInitializer() *ProjectInitializer {
	return &ProjectInitializer{
		builder: build.NewCNBBuilder(),
	}
}

func (p *ProjectInitializer) InspectAndGenerate(ctx context.Context, opts InitOptions) (*config.AppSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 0 and 65535", opts.Port)
	}

	dir := strings.TrimSpace(opts.SourceDir)
	if dir == "" {
		dir = "."
	} else if strings.ContainsAny(dir, "\r\n\x00") {
		return nil, fmt.Errorf("invalid source directory: must not contain control characters")
	}

	b := p.builder
	if b == nil {
		b = build.NewCNBBuilder()
	}

	det, err := b.Detect(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("detection failed: %w", err)
	}

	name := opts.Name
	if name == "" {
		abs, err := filepath.Abs(dir)
		if err == nil {
			name = filepath.Base(abs)
		} else {
			name = "my-service"
		}
	}
	name = sanitizeWorkloadName(name)

	team := opts.Team
	if team == "" {
		team = "platform"
	}

	env := opts.Environment
	if env == "" {
		env = "development"
	}

	port := opts.Port
	if port == 0 {
		port = detectDefaultPort(det.Runtime)
	}

	spec := &config.AppSpec{
		Version:     "v1alpha1",
		Name:        name,
		Team:        team,
		Environment: env,
		Type:        "service",
		Endpoints: []config.EndpointConfig{
			{
				Name:     "http",
				Port:     port,
				Protocol: "TCP",
				Public:   true,
			},
		},
		Health: config.HealthConfig{
			Readiness: &config.ProbeConfig{
				Path:          "/healthz",
				Port:          port,
				PeriodSeconds: 10,
			},
			Liveness: &config.ProbeConfig{
				Path:          "/livez",
				Port:          port,
				PeriodSeconds: 15,
			},
		},
		Security: config.SecurityConfig{
			RunAsNonRoot:             true,
			ReadOnlyRootFilesystem:   false,
			AllowPrivilegeEscalation: false,
		},
		Runtime: config.RuntimeConfig{
			Replicas: config.ReplicaConfig{
				Min: 1,
				Max: 3,
			},
			Resources: config.ResourceConfig{
				CPU:    config.ResourceLimit{Request: "100m", Limit: "500m"},
				Memory: config.ResourceLimit{Request: "128Mi", Limit: "512Mi"},
			},
		},
	}

	return spec, nil
}

func (p *ProjectInitializer) WriteSpecFile(spec *config.AppSpec, targetPath string) error {
	if spec == nil {
		return fmt.Errorf("app spec must not be nil")
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" || strings.ContainsAny(targetPath, "\r\n\x00") {
		return fmt.Errorf("invalid target path %q", targetPath)
	}

	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to encode YAML: %w", err)
	}

	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("file '%s' already exists; delete or overwrite explicitly", targetPath)
	}

	return os.WriteFile(targetPath, data, 0644)
}

func detectDefaultPort(rt build.LanguageRuntime) int {
	switch rt {
	case build.RuntimeNodeJS:
		return 3000
	case build.RuntimePython:
		return 8000
	default:
		return 8080
	}
}

func sanitizeWorkloadName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	var valid []rune
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			valid = append(valid, r)
			prevHyphen = false
		} else if r == '-' && !prevHyphen {
			valid = append(valid, r)
			prevHyphen = true
		}
	}
	out := strings.Trim(string(valid), "-")
	if len(out) == 0 {
		return "my-service"
	}
	return out
}
