package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

// ComposeProject represents a full multi-service Docker Compose workspace.
type ComposeProject struct {
	Name     string
	Dir      string
	File     string
	Services []ProjectService
}

// ProjectService represents an individual service loaded from a Compose file.
type ProjectService struct {
	Name       string
	Image      string
	HasBuild   bool
	Context    string
	Dockerfile string
	Args       map[string]string
	Target     string
	DependsOn  []string
	Remote     bool
	Spec       *config.AppSpec
}

var composeCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// FindComposeFile locates a standard Docker Compose file in dir in priority order.
func FindComposeFile(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	for _, name := range composeCandidates {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no compose file found in %q (checked %s)", dir, strings.Join(composeCandidates, ", "))
}

// LoadComposeProject parses a Compose file, loads .env variables, interpolates fields,
// resolves build contexts, and orders services topologically by depends_on.
func LoadComposeProject(path string) (*ComposeProject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read compose file %q: %w", path, err)
	}

	var compose ComposeFile
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return nil, fmt.Errorf("failed to parse compose file %q: %w", path, err)
	}
	if len(compose.Services) == 0 {
		return nil, fmt.Errorf("no services defined in compose file %q", path)
	}

	projectDir := filepath.Dir(path)
	dotEnv := loadDotEnv(filepath.Join(projectDir, ".env"))
	lookup := func(key string) (string, bool) {
		if val, ok := os.LookupEnv(key); ok {
			return val, true
		}
		if val, ok := dotEnv[key]; ok {
			return val, true
		}
		return "", false
	}

	rawName := strings.TrimSpace(compose.Name)
	if rawName == "" {
		if envName, ok := lookup("COMPOSE_PROJECT_NAME"); ok {
			rawName = strings.TrimSpace(envName)
		}
	}
	if rawName == "" {
		if absDir, err := filepath.Abs(projectDir); err == nil {
			rawName = filepath.Base(absDir)
		} else {
			rawName = filepath.Base(projectDir)
		}
	}
	projectName := SanitizeName(rawName)

	svcMap := make(map[string]ProjectService, len(compose.Services))
	for name, svc := range compose.Services {
		if strings.TrimSpace(name) == "" || strings.HasPrefix(strings.TrimSpace(name), "-") || strings.ContainsAny(name, " \t\r\n/\\") {
			return nil, fmt.Errorf("invalid compose service name %q", name)
		}
		image := interpolateVars(svc.Image, lookup)
		hasBuild, rawCtx, dockerfile, target, args := parseBuildConfig(svc.Build, lookup, true)

		var resolvedCtx string
		var remote bool
		var buildCfg config.BuildConfig

		if hasBuild {
			if isRemoteContext(rawCtx) {
				remote = true
				resolvedCtx = rawCtx
			} else {
				if strings.TrimSpace(rawCtx) == "" {
					rawCtx = "."
				}
				if filepath.IsAbs(rawCtx) {
					resolvedCtx = filepath.Clean(rawCtx)
				} else {
					resolvedCtx = filepath.Join(projectDir, rawCtx)
				}
			}
			buildCfg = config.BuildConfig{
				Context:    resolvedCtx,
				Dockerfile: dockerfile,
				Args:       args,
				Target:     target,
			}
		}

		deps := parseDependsOn(svc.DependsOn)
		spec := serviceToSpec(name, svc, buildCfg, image, lookup)

		svcMap[name] = ProjectService{
			Name:       name,
			Image:      image,
			HasBuild:   hasBuild,
			Context:    resolvedCtx,
			Dockerfile: dockerfile,
			Args:       args,
			Target:     target,
			DependsOn:  deps,
			Remote:     remote,
			Spec:       spec,
		}
	}

	ordered := sortServices(svcMap)

	return &ComposeProject{
		Name:     projectName,
		Dir:      projectDir,
		File:     path,
		Services: ordered,
	}, nil
}

// SanitizeName normalizes a project or service name to lowercase [a-z0-9_-],
// stripping leading/trailing '-' or '_' and falling back to "app" if empty.
func SanitizeName(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "app"
	}
	return out
}

func isRemoteContext(ctx string) bool {
	trimmed := strings.TrimSpace(ctx)
	return strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "git@")
}

func parseDependsOn(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	seen := make(map[string]struct{})
	switch d := raw.(type) {
	case []interface{}:
		for _, item := range d {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					seen[s] = struct{}{}
				}
			}
		}
	case map[string]interface{}:
		for k := range d {
			k = strings.TrimSpace(k)
			if k != "" {
				seen[k] = struct{}{}
			}
		}
	case map[interface{}]interface{}:
		for k := range d {
			key := strings.TrimSpace(fmt.Sprintf("%v", k))
			if key != "" {
				seen[key] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	deps := make([]string, 0, len(seen))
	for k := range seen {
		deps = append(deps, k)
	}
	sort.Strings(deps)
	return deps
}

// sortServices orders services topologically so dependencies precede dependents,
// breaking ties alphabetically. Falls back to alphabetical order on unknown
// dependencies or dependency cycles.
func sortServices(svcMap map[string]ProjectService) []ProjectService {
	allNames := make([]string, 0, len(svcMap))
	for name := range svcMap {
		allNames = append(allNames, name)
	}
	sort.Strings(allNames)

	alphabetical := func() []ProjectService {
		res := make([]ProjectService, 0, len(allNames))
		for _, n := range allNames {
			res = append(res, svcMap[n])
		}
		return res
	}

	inDegree := make(map[string]int, len(svcMap))
	dependents := make(map[string][]string, len(svcMap))
	for _, n := range allNames {
		inDegree[n] = 0
	}

	for _, name := range allNames {
		svc := svcMap[name]
		for _, dep := range svc.DependsOn {
			if _, ok := svcMap[dep]; !ok {
				return alphabetical()
			}
			if dep == name {
				return alphabetical()
			}
			dependents[dep] = append(dependents[dep], name)
			inDegree[name]++
		}
	}

	var ready []string
	for _, n := range allNames {
		if inDegree[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)

	ordered := make([]ProjectService, 0, len(allNames))
	for len(ready) > 0 {
		curr := ready[0]
		ready = ready[1:]
		ordered = append(ordered, svcMap[curr])

		for _, next := range dependents[curr] {
			inDegree[next]--
			if inDegree[next] == 0 {
				ready = append(ready, next)
			}
		}
		sort.Strings(ready)
	}

	if len(ordered) != len(allNames) {
		return alphabetical()
	}
	return ordered
}

func loadDotEnv(path string) map[string]string {
	vars := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return vars
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		vars[key] = val
	}
	return vars
}

func interpolateVars(input string, lookup func(string) (string, bool)) string {
	if !strings.Contains(input, "$") {
		return input
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}

	var b strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' {
			b.WriteByte(input[i])
			i++
			continue
		}
		if i+1 >= len(input) {
			b.WriteByte('$')
			break
		}
		next := input[i+1]
		if next == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if next == '{' {
			end := findClosingBrace(input, i+2)
			if end == -1 {
				b.WriteByte('$')
				i++
				continue
			}
			expr := input[i+2 : end]
			b.WriteString(evalBraceExpr(expr, lookup))
			i = end + 1
			continue
		}
		if isVarStart(next) {
			j := i + 2
			for j < len(input) && isVarCont(input[j]) {
				j++
			}
			varName := input[i+1 : j]
			if val, ok := lookup(varName); ok {
				b.WriteString(val)
			}
			i = j
			continue
		}
		b.WriteByte('$')
		i++
	}
	return b.String()
}

func findClosingBrace(s string, start int) int {
	depth := 1
	for i := start; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			depth++
			i++
			continue
		}
		if s[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func evalBraceExpr(expr string, lookup func(string) (string, bool)) string {
	if idx := strings.Index(expr, ":-"); idx != -1 {
		name := expr[:idx]
		defVal := expr[idx+2:]
		if val, ok := lookup(name); ok && val != "" {
			return val
		}
		return interpolateVars(defVal, lookup)
	}
	if idx := strings.Index(expr, "-"); idx != -1 {
		name := expr[:idx]
		defVal := expr[idx+1:]
		if val, ok := lookup(name); ok {
			return val
		}
		return interpolateVars(defVal, lookup)
	}
	if val, ok := lookup(expr); ok {
		return val
	}
	return ""
}

func isVarStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isVarCont(c byte) bool {
	return isVarStart(c) || (c >= '0' && c <= '9')
}
