package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
)

type resolvedDependency struct {
	name  string
	image string
	port  int
	env   map[string]string
}

// DockerNetworkName returns the deterministic Docker bridge network name for an app's local dependencies.
func DockerNetworkName(appName string) string {
	return fmt.Sprintf("skipper-%s-net", appName)
}

// ResolveDependencyPreset resolves a ServiceDependency into its container image, port, and environment variables.
// Known presets (postgres, redis, mysql, mongo) provide sensible local defaults, while dep.Image, dep.Port,
// and dep.Env override or extend those defaults. Custom dependencies require dep.Image to be set.
func ResolveDependencyPreset(appName string, dep config.ServiceDependency) (image string, port int, env map[string]string, err error) {
	if err := validateSafeArg("dependency name", dep.Name); err != nil {
		return "", 0, nil, err
	}
	if err := validateOptionalSafeArg("dependency image", dep.Image); err != nil {
		return "", 0, nil, err
	}
	env = make(map[string]string)
	dbName := strings.TrimSpace(appName)
	if dbName == "" {
		dbName = "skipper"
	}

	depType := strings.ToLower(strings.TrimSpace(dep.Type))
	switch depType {
	case "postgres":
		image = "postgres:16-alpine"
		port = 5432
		env["POSTGRES_PASSWORD"] = "skipper"
		env["POSTGRES_DB"] = dbName
	case "redis":
		image = "redis:7-alpine"
		port = 6379
	case "mysql":
		image = "mysql:8.4"
		port = 3306
		env["MYSQL_ROOT_PASSWORD"] = "skipper"
		env["MYSQL_DATABASE"] = dbName
	case "mongo":
		image = "mongo:7"
		port = 27017
	case "", "custom":
		if strings.TrimSpace(dep.Image) == "" {
			return "", 0, nil, fmt.Errorf("dependency %q requires either a known type preset (postgres, redis, mysql, mongo) or a custom image", dep.Name)
		}
		image = strings.TrimSpace(dep.Image)
		port = dep.Port
	default:
		return "", 0, nil, fmt.Errorf("unsupported dependency type %q for %q (supported: postgres, redis, mysql, mongo, custom)", dep.Type, dep.Name)
	}

	if customImg := strings.TrimSpace(dep.Image); customImg != "" {
		image = customImg
	}
	if dep.Port > 0 {
		port = dep.Port
	}
	for k, v := range dep.Env {
		env[k] = v
	}

	return image, port, env, nil
}

func dependencyEnvPrefix(depName string) string {
	s := strings.ToUpper(strings.TrimSpace(depName))
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_")
	return replacer.Replace(s)
}

func buildDependencyRunArgs(appName, networkName string, dep config.ServiceDependency, image string, env map[string]string) []string {
	containerName := fmt.Sprintf("%s-%s", appName, dep.Name)
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--network", networkName,
		"--network-alias", dep.Name,
		"--label", fmt.Sprintf("skipper.parent=%s", appName),
	}

	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, env[k]))
		}
	}

	args = append(args, image)
	return args
}

func buildDependencyEnvVars(deps []resolvedDependency) []config.EnvVar {
	var vars []config.EnvVar
	for _, dep := range deps {
		prefix := dependencyEnvPrefix(dep.name)
		vars = append(vars, config.EnvVar{
			Name:  prefix + "_HOST",
			Value: dep.name,
		})
		if dep.port > 0 {
			vars = append(vars, config.EnvVar{
				Name:  prefix + "_PORT",
				Value: strconv.Itoa(dep.port),
			})
		}
	}
	return vars
}
