package build

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredService describes a buildable service found in a subdirectory.
type DiscoveredService struct {
	Name       string
	Path       string
	Runtime    LanguageRuntime
	FilesFound []string
}

var skipDiscoverDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"venv":         true,
	"__pycache__":  true,
	"bin":          true,
	"obj":          true,
	"coverage":     true,
}

// DiscoverServices walks subdirectories up to maxDepth levels below root to locate
// services with a Dockerfile or supported language stack marker.
func (b *CNBBuilder) DiscoverServices(ctx context.Context, root string, maxDepth int) ([]DiscoveredService, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	if maxDepth <= 0 {
		maxDepth = 2
	}
	root = filepath.Clean(root)

	var services []DiscoveredService
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		name := d.Name()
		if strings.HasPrefix(name, ".") || skipDiscoverDirs[name] {
			return filepath.SkipDir
		}

		slashRel := filepath.ToSlash(rel)
		depth := strings.Count(slashRel, "/") + 1
		if depth > maxDepth {
			return filepath.SkipDir
		}

		det, err := b.Detect(ctx, path)
		if err != nil {
			return err
		}
		if det.Runtime != RuntimeUnknown {
			services = append(services, DiscoveredService{
				Name:       sanitizeDiscoveredName(slashRel),
				Path:       path,
				Runtime:    det.Runtime,
				FilesFound: det.FilesFound,
			})
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Path < services[j].Path
	})
	return services, nil
}

func sanitizeDiscoveredName(rel string) string {
	lower := strings.ToLower(strings.TrimSpace(rel))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "service"
	}
	return out
}
