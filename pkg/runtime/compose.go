package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode"
)

// ComposeUpOptions configures a `docker compose up` invocation.
type ComposeUpOptions struct {
	File     string
	Services []string
	Build    bool
	Detach   bool
}

// ComposeDownOptions configures a `docker compose down` invocation.
type ComposeDownOptions struct {
	File          string
	RemoveVolumes bool
	RemoveOrphans bool
}

// ComposeServiceStatus represents the normalized status of a single Compose service container.
type ComposeServiceStatus struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Health  string `json:"health,omitempty"`
	Ports   string `json:"ports,omitempty"`
}

// ComposeRunner executes multi-service Docker Compose lifecycle commands safely.
type ComposeRunner struct {
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// NewComposeRunner constructs a ComposeRunner backed by os/exec.CommandContext.
func NewComposeRunner() *ComposeRunner {
	return &ComposeRunner{
		execCommand: exec.CommandContext,
	}
}

// NewComposeRunnerWithExec constructs a ComposeRunner with a custom command factory for testing.
func NewComposeRunnerWithExec(fn func(ctx context.Context, name string, arg ...string) *exec.Cmd) *ComposeRunner {
	if fn == nil {
		fn = exec.CommandContext
	}
	return &ComposeRunner{
		execCommand: fn,
	}
}

func (r *ComposeRunner) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if r != nil && r.execCommand != nil {
		return r.execCommand(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

func validateComposeFile(file string) (string, error) {
	trimmed := strings.TrimSpace(file)
	if trimmed == "" {
		return "", fmt.Errorf("compose file path is required")
	}
	if strings.HasPrefix(trimmed, "-") {
		return "", fmt.Errorf("invalid compose file path %q: must not start with '-'", file)
	}
	for _, r := range trimmed {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("invalid compose file path %q: must not contain control characters or null bytes", file)
		}
	}
	return trimmed, nil
}

func validateComposeServices(services []string) ([]string, error) {
	if len(services) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(services))
	for _, svc := range services {
		if err := validateSafeArg("service name", svc); err != nil {
			return nil, err
		}
		cleaned = append(cleaned, strings.TrimSpace(svc))
	}
	return cleaned, nil
}

// Up launches services defined in the Compose file via `docker compose -f <file> up`.
func (r *ComposeRunner) Up(ctx context.Context, opts ComposeUpOptions, out, errOut io.Writer) error {
	file, err := validateComposeFile(opts.File)
	if err != nil {
		return err
	}
	services, err := validateComposeServices(opts.Services)
	if err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}

	args := []string{"compose", "-f", file, "up"}
	if opts.Detach {
		args = append(args, "-d")
	}
	if opts.Build {
		args = append(args, "--build")
	}
	args = append(args, services...)

	cmd := r.command(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

// Down stops and removes Compose resources via `docker compose -f <file> down`.
func (r *ComposeRunner) Down(ctx context.Context, opts ComposeDownOptions, out, errOut io.Writer) error {
	file, err := validateComposeFile(opts.File)
	if err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}

	args := []string{"compose", "-f", file, "down"}
	if opts.RemoveVolumes {
		args = append(args, "-v")
	}
	if opts.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}

	cmd := r.command(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose down failed: %w", err)
	}
	return nil
}

type rawComposePublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

type rawComposePSItem struct {
	Name       string          `json:"Name"`
	Service    string          `json:"Service"`
	Image      string          `json:"Image"`
	State      string          `json:"State"`
	Status     string          `json:"Status"`
	Health     string          `json:"Health"`
	Ports      string          `json:"Ports"`
	Publishers json.RawMessage `json:"Publishers"`
}

// Status queries container status via `docker compose -f <file> ps -a --format json`
// and parses both Compose v2.21+ NDJSON and legacy JSON array formats.
func (r *ComposeRunner) Status(ctx context.Context, file string, services []string) ([]ComposeServiceStatus, error) {
	cleanFile, err := validateComposeFile(file)
	if err != nil {
		return nil, err
	}
	cleanServices, err := validateComposeServices(services)
	if err != nil {
		return nil, err
	}

	args := []string{"compose", "-f", cleanFile, "ps", "-a", "--format", "json"}
	args = append(args, cleanServices...)

	cmd := r.command(ctx, "docker", args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		errDetail := strings.TrimSpace(stderrBuf.String())
		if errDetail == "" {
			errDetail = strings.TrimSpace(stdoutBuf.String())
		}
		if errDetail != "" {
			return nil, fmt.Errorf("docker compose ps failed: %w (%s)", err, errDetail)
		}
		return nil, fmt.Errorf("docker compose ps failed: %w", err)
	}

	return parseComposePSJSON(stdoutBuf.String())
}

func parseComposePSJSON(raw string) ([]ComposeServiceStatus, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []ComposeServiceStatus{}, nil
	}

	var items []rawComposePSItem
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("failed to parse docker compose ps json array: %w", err)
		}
	} else {
		lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var item rawComposePSItem
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				return nil, fmt.Errorf("failed to parse docker compose ps ndjson line: %w", err)
			}
			items = append(items, item)
		}
	}

	statuses := make([]ComposeServiceStatus, 0, len(items))
	for _, it := range items {
		ports := formatComposePublishers(it.Publishers)
		if ports == "" {
			ports = strings.TrimSpace(it.Ports)
		}
		statuses = append(statuses, ComposeServiceStatus{
			Name:    it.Name,
			Service: it.Service,
			Image:   it.Image,
			State:   it.State,
			Status:  it.Status,
			Health:  it.Health,
			Ports:   ports,
		})
	}
	return statuses, nil
}

func formatComposePublishers(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	var pubs []rawComposePublisher
	if err := json.Unmarshal(raw, &pubs); err == nil {
		seen := make(map[string]struct{}, len(pubs))
		parts := make([]string, 0, len(pubs))
		for _, p := range pubs {
			proto := strings.TrimSpace(p.Protocol)
			if proto == "" {
				proto = "tcp"
			}
			var entry string
			if p.PublishedPort > 0 {
				if strings.TrimSpace(p.URL) != "" {
					entry = fmt.Sprintf("%s:%d->%d/%s", strings.TrimSpace(p.URL), p.PublishedPort, p.TargetPort, proto)
				} else {
					entry = fmt.Sprintf("%d->%d/%s", p.PublishedPort, p.TargetPort, proto)
				}
			} else if p.TargetPort > 0 {
				entry = fmt.Sprintf("%d/%s", p.TargetPort, proto)
			}
			if entry != "" {
				if _, dup := seen[entry]; !dup {
					seen[entry] = struct{}{}
					parts = append(parts, entry)
				}
			}
		}
		return strings.Join(parts, ", ")
	}

	var fallback string
	if err := json.Unmarshal(raw, &fallback); err == nil {
		return strings.TrimSpace(fallback)
	}
	return ""
}
