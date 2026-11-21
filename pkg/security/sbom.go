package security

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SBOMFormat identifies the output format of a generated Software Bill of Materials.
type SBOMFormat string

const (
	FormatCycloneDX SBOMFormat = "cyclonedx-json"
	FormatSPDX      SBOMFormat = "spdx-json"
)

// ParseSBOMFormat normalizes user-facing format strings (including short aliases
// "cyclonedx" and "spdx") into a canonical SBOMFormat.
func ParseSBOMFormat(raw string) (SBOMFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "cyclonedx", string(FormatCycloneDX):
		return FormatCycloneDX, nil
	case "spdx", string(FormatSPDX):
		return FormatSPDX, nil
	default:
		return "", fmt.Errorf("unsupported SBOM format %q (supported: cyclonedx-json, spdx-json)", raw)
	}
}

// SBOMOptions configures SBOM generation.
type SBOMOptions struct {
	Target     string     `json:"target"`
	Format     SBOMFormat `json:"format"`
	OutputPath string     `json:"outputPath,omitempty"`
}

// SBOMComponent represents a single software dependency or artifact in an SBOM.
type SBOMComponent struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Type      string `json:"type"`
	Ecosystem string `json:"ecosystem"`
	PURL      string `json:"purl,omitempty"`
}

// SBOMReport summarizes a generated SBOM and carries its JSON document bytes.
type SBOMReport struct {
	Format         SBOMFormat `json:"format"`
	Target         string     `json:"target"`
	Tool           string     `json:"tool"`
	ComponentCount int        `json:"componentCount"`
	OutputPath     string     `json:"outputPath,omitempty"`
	Document       []byte     `json:"document"`
}

// SBOMGenerator generates CycloneDX 1.5 or SPDX 2.3 SBOMs using syft, docker sbom,
// or a deterministic Go-native fallback parser.
type SBOMGenerator struct {
	runCmd commandRunner
}

// NewSBOMGenerator creates a new SBOM generator.
func NewSBOMGenerator() *SBOMGenerator {
	return &SBOMGenerator{}
}

func (g *SBOMGenerator) execCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	if g.runCmd != nil {
		return g.runCmd(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Generate produces an SBOM for a directory or OCI image reference.
func (g *SBOMGenerator) Generate(ctx context.Context, opts SBOMOptions) (*SBOMReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	format, err := ParseSBOMFormat(string(opts.Format))
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(opts.Target)
	if target == "" {
		target = "."
	}

	var doc []byte
	var tool string
	var count int

	// 1. Prefer syft when available
	if _, err := g.execCmd(ctx, "syft", "version"); err == nil {
		raw, runErr := g.execCmd(ctx, "syft", target, "-o", string(format))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if runErr == nil && json.Valid(raw) {
			doc = raw
			tool = "syft"
			count = countSBOMComponents(raw, format)
		}
	}

	// 2. Next try docker sbom when available
	if len(doc) == 0 {
		if _, err := g.execCmd(ctx, "docker", "sbom", "--version"); err == nil {
			raw, runErr := g.execCmd(ctx, "docker", "sbom", target, "--format", string(format))
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if runErr == nil && json.Valid(raw) {
				doc = raw
				tool = "docker-sbom"
				count = countSBOMComponents(raw, format)
			}
		}
	}

	// 3. Deterministic Go-native fallback
	if len(doc) == 0 {
		components, err := discoverNativeComponents(target)
		if err != nil {
			return nil, err
		}
		doc, err = renderNativeSBOM(target, format, components)
		if err != nil {
			return nil, err
		}
		tool = "skipper-native"
		count = len(components)
	}

	if opts.OutputPath != "" {
		if dir := filepath.Dir(opts.OutputPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create output directory for SBOM: %w", err)
			}
		}
		if err := os.WriteFile(opts.OutputPath, doc, 0644); err != nil {
			return nil, fmt.Errorf("failed to write SBOM to %s: %w", opts.OutputPath, err)
		}
	}

	return &SBOMReport{
		Format:         format,
		Target:         target,
		Tool:           tool,
		ComponentCount: count,
		OutputPath:     opts.OutputPath,
		Document:       doc,
	}, nil
}

func countSBOMComponents(raw []byte, format SBOMFormat) int {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return 0
	}
	key := "components"
	if format == FormatSPDX {
		key = "packages"
	}
	arrRaw, ok := parsed[key]
	if !ok {
		return 0
	}
	var items []json.RawMessage
	if err := json.Unmarshal(arrRaw, &items); err != nil {
		return 0
	}
	return len(items)
}

func discoverNativeComponents(target string) ([]SBOMComponent, error) {
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		// Treat non-directory targets as OCI image references
		name, version := parseImageRef(target)
		purl := fmt.Sprintf("pkg:oci/%s@%s", name, version)
		return []SBOMComponent{
			{
				Name:      name,
				Version:   version,
				Type:      "container",
				Ecosystem: "oci",
				PURL:      purl,
			},
		}, nil
	}

	var components []SBOMComponent

	if goComps, err := parseGoMod(filepath.Join(target, "go.mod")); err == nil {
		components = append(components, goComps...)
	}
	if npmComps, err := parsePackageJSON(filepath.Join(target, "package.json")); err == nil {
		components = append(components, npmComps...)
	}
	if pyComps, err := parseRequirementsTxt(filepath.Join(target, "requirements.txt")); err == nil {
		components = append(components, pyComps...)
	}

	sort.Slice(components, func(i, j int) bool {
		if components[i].Ecosystem != components[j].Ecosystem {
			return components[i].Ecosystem < components[j].Ecosystem
		}
		if components[i].Name != components[j].Name {
			return components[i].Name < components[j].Name
		}
		return components[i].Version < components[j].Version
	})

	// Deduplicate
	dedup := make([]SBOMComponent, 0, len(components))
	seen := make(map[string]struct{}, len(components))
	for _, c := range components {
		k := c.Ecosystem + "|" + c.Name + "|" + c.Version
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dedup = append(dedup, c)
	}
	return dedup, nil
}

func parseImageRef(ref string) (name, version string) {
	clean := strings.TrimSpace(ref)
	if idx := strings.Index(clean, "@"); idx != -1 {
		return clean[:idx], clean[idx+1:]
	}
	lastSlash := strings.LastIndex(clean, "/")
	colon := strings.LastIndex(clean, ":")
	if colon > lastSlash {
		return clean[:colon], clean[colon+1:]
	}
	return clean, "latest"
}

func parseGoMod(path string) ([]SBOMComponent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var comps []SBOMComponent
	inRequire := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "//"); idx != -1 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "require (") || line == "require(" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}
		if strings.HasPrefix(line, "require ") {
			fields := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(fields) >= 2 {
				comps = append(comps, SBOMComponent{
					Name:      fields[0],
					Version:   fields[1],
					Type:      "library",
					Ecosystem: "golang",
					PURL:      fmt.Sprintf("pkg:golang/%s@%s", fields[0], fields[1]),
				})
			}
			continue
		}
		if inRequire {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				comps = append(comps, SBOMComponent{
					Name:      fields[0],
					Version:   fields[1],
					Type:      "library",
					Ecosystem: "golang",
					PURL:      fmt.Sprintf("pkg:golang/%s@%s", fields[0], fields[1]),
				})
			}
		}
	}
	return comps, scanner.Err()
}

func parsePackageJSON(path string) ([]SBOMComponent, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	var comps []SBOMComponent
	addDeps := func(deps map[string]string) {
		for name, ver := range deps {
			cleanVer := strings.TrimLeft(strings.TrimSpace(ver), "^~")
			comps = append(comps, SBOMComponent{
				Name:      name,
				Version:   cleanVer,
				Type:      "library",
				Ecosystem: "npm",
				PURL:      fmt.Sprintf("pkg:npm/%s@%s", name, cleanVer),
			})
		}
	}
	addDeps(pkg.Dependencies)
	addDeps(pkg.DevDependencies)
	return comps, nil
}

func parseRequirementsTxt(path string) ([]SBOMComponent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var comps []SBOMComponent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		name := line
		version := "unknown"
		for _, sep := range []string{"==", ">=", "<=", "~=", "!="} {
			if idx := strings.Index(line, sep); idx != -1 {
				name = strings.TrimSpace(line[:idx])
				version = strings.TrimSpace(line[idx+len(sep):])
				break
			}
		}
		if name == "" {
			continue
		}
		comps = append(comps, SBOMComponent{
			Name:      name,
			Version:   version,
			Type:      "library",
			Ecosystem: "pypi",
			PURL:      fmt.Sprintf("pkg:pypi/%s@%s", name, version),
		})
	}
	return comps, scanner.Err()
}

func renderNativeSBOM(target string, format SBOMFormat, components []SBOMComponent) ([]byte, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	switch format {
	case FormatCycloneDX:
		type cdxComponent struct {
			BOMRef  string `json:"bom-ref"`
			Type    string `json:"type"`
			Name    string `json:"name"`
			Version string `json:"version"`
			PURL    string `json:"purl,omitempty"`
		}
		cdxComps := make([]cdxComponent, 0, len(components))
		for i, c := range components {
			ref := c.PURL
			if ref == "" {
				ref = fmt.Sprintf("component-%d", i+1)
			}
			cdxComps = append(cdxComps, cdxComponent{
				BOMRef:  ref,
				Type:    c.Type,
				Name:    c.Name,
				Version: c.Version,
				PURL:    c.PURL,
			})
		}
		doc := map[string]interface{}{
			"bomFormat":   "CycloneDX",
			"specVersion": "1.5",
			"version":     1,
			"metadata": map[string]interface{}{
				"timestamp": now,
				"tools": []map[string]string{
					{"vendor": "skipper", "name": "skipper-native", "version": "0.1.0"},
				},
				"component": map[string]string{
					"type": "application",
					"name": target,
				},
			},
			"components": cdxComps,
		}
		return json.MarshalIndent(doc, "", "  ")

	case FormatSPDX:
		type spdxExtRef struct {
			ReferenceCategory string `json:"referenceCategory"`
			ReferenceType     string `json:"referenceType"`
			ReferenceLocator  string `json:"referenceLocator"`
		}
		type spdxPkg struct {
			SPDXID           string       `json:"SPDXID"`
			Name             string       `json:"name"`
			VersionInfo      string       `json:"versionInfo"`
			DownloadLocation string       `json:"downloadLocation"`
			ExternalRefs     []spdxExtRef `json:"externalRefs,omitempty"`
		}
		pkgs := make([]spdxPkg, 0, len(components))
		for i, c := range components {
			p := spdxPkg{
				SPDXID:           fmt.Sprintf("SPDXRef-Package-%d", i+1),
				Name:             c.Name,
				VersionInfo:      c.Version,
				DownloadLocation: "NOASSERTION",
			}
			if c.PURL != "" {
				p.ExternalRefs = []spdxExtRef{
					{
						ReferenceCategory: "PACKAGE-MANAGER",
						ReferenceType:     "purl",
						ReferenceLocator:  c.PURL,
					},
				}
			}
			pkgs = append(pkgs, p)
		}
		doc := map[string]interface{}{
			"spdxVersion":       "SPDX-2.3",
			"dataLicense":       "CC0-1.0",
			"SPDXID":            "SPDXRef-DOCUMENT",
			"name":              target,
			"documentNamespace": fmt.Sprintf("https://skipper.dev/spdx/%s", sanitizeSPDXName(target)),
			"creationInfo": map[string]interface{}{
				"created":  now,
				"creators": []string{"Tool: skipper-native-0.1.0"},
			},
			"packages": pkgs,
		}
		return json.MarshalIndent(doc, "", "  ")

	default:
		return nil, fmt.Errorf("unsupported SBOM format %q", format)
	}
}

func sanitizeSPDXName(s string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "@", "-")
	clean := strings.Trim(r.Replace(s), "-")
	if clean == "" {
		return "workspace"
	}
	return clean
}
