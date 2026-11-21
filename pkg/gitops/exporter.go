package gitops

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/synthesizer"
)

// Engine identifies the target GitOps reconciliation engine.
type Engine string

const (
	EngineArgoCD Engine = "argocd"
	EngineFlux   Engine = "flux"
)

// ExportOptions configures GitOps repository layout and sync manifest generation.
type ExportOptions struct {
	Engine      Engine `json:"engine"`
	RepoURL     string `json:"repoUrl"`
	Branch      string `json:"branch"`
	TargetPath  string `json:"targetPath"`
	Namespace   string `json:"namespace"`
	Environment string `json:"environment"`
	OutputDir   string `json:"outputDir,omitempty"`
}

// GeneratedFile represents a single manifest file produced by Export.
type GeneratedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ExportResult summarizes the generated GitOps repository artifacts and sync plan.
type ExportResult struct {
	Engine      Engine          `json:"engine"`
	AppName     string          `json:"appName"`
	Environment string          `json:"environment"`
	Namespace   string          `json:"namespace"`
	RepoURL     string          `json:"repoUrl"`
	Branch      string          `json:"branch"`
	TargetPath  string          `json:"targetPath"`
	OutputDir   string          `json:"outputDir,omitempty"`
	Files       []GeneratedFile `json:"files"`
	SyncPlan    string          `json:"syncPlan"`
}

func validateSafeIdentifier(field, val string) error {
	if val == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if strings.HasPrefix(val, "-") || strings.HasPrefix(val, ".") {
		return fmt.Errorf("invalid %s %q: must not start with '-' or '.'", field, val)
	}
	if strings.Contains(val, "..") || strings.ContainsAny(val, "/\\ \t\r\n\x00'\":") {
		return fmt.Errorf("invalid %s %q: contains unsafe characters or path traversal", field, val)
	}
	for _, r := range val {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid %s %q: unsupported character %q", field, val, r)
	}
	return nil
}

func validateSafeSingleLine(field, val string) error {
	if strings.HasPrefix(val, "-") {
		return fmt.Errorf("invalid %s %q: must not start with '-'", field, val)
	}
	if strings.ContainsAny(val, "\r\n\x00") {
		return fmt.Errorf("invalid %s %q: must not contain control characters or newlines", field, val)
	}
	return nil
}

func sanitizeTargetPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if strings.ContainsAny(p, "\\\r\n\x00") {
		return "", fmt.Errorf("invalid targetPath %q: backslashes and control characters are not allowed", raw)
	}
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return "", fmt.Errorf("invalid targetPath %q: must be a relative path", raw)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid targetPath %q: path traversal ('..') is not allowed", raw)
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid targetPath %q: path traversal is not allowed", raw)
	}
	return cleaned, nil
}

// Export compiles an AppSpec into a Kustomize base + environment overlay + GitOps sync manifests
// for ArgoCD or FluxCD, optionally writing all files to opts.OutputDir.
func Export(spec *config.AppSpec, opts ExportOptions) (*ExportResult, error) {
	if spec == nil {
		return nil, fmt.Errorf("app spec must not be nil")
	}
	appName := strings.TrimSpace(spec.Name)
	if appName == "" {
		return nil, fmt.Errorf("app spec name must not be empty")
	}
	if err := validateSafeIdentifier("app spec name", appName); err != nil {
		return nil, err
	}

	engine := opts.Engine
	if engine == "" {
		engine = EngineArgoCD
	}
	if engine != EngineArgoCD && engine != EngineFlux {
		return nil, fmt.Errorf("unsupported gitops engine %q (supported: %q, %q)", engine, EngineArgoCD, EngineFlux)
	}

	env := strings.TrimSpace(opts.Environment)
	if env == "" {
		if strings.TrimSpace(spec.Environment) != "" {
			env = strings.TrimSpace(spec.Environment)
		} else {
			env = "prod"
		}
	}
	if err := validateSafeIdentifier("environment", env); err != nil {
		return nil, err
	}

	ns := strings.TrimSpace(opts.Namespace)
	if ns == "" {
		ns = "default"
	}
	if err := validateSafeIdentifier("namespace", ns); err != nil {
		return nil, err
	}

	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = "main"
	}
	if err := validateSafeSingleLine("branch", branch); err != nil {
		return nil, err
	}

	repoURL := strings.TrimSpace(opts.RepoURL)
	if repoURL == "" {
		repoURL = fmt.Sprintf("https://github.com/example/%s.git", appName)
	}
	if err := validateSafeSingleLine("repoURL", repoURL); err != nil {
		return nil, err
	}

	rawTargetPath := strings.TrimSpace(opts.TargetPath)
	if rawTargetPath == "" {
		rawTargetPath = fmt.Sprintf("overlays/%s", env)
	}
	targetPath, err := sanitizeTargetPath(rawTargetPath)
	if err != nil {
		return nil, err
	}

	// Synthesize base Kubernetes manifests
	synth := synthesizer.NewSynthesizer()
	bundle, err := synth.Synthesize(spec, ns)
	if err != nil {
		return nil, fmt.Errorf("failed to synthesize base manifests: %w", err)
	}

	var files []GeneratedFile
	var baseResources []string

	if bundle.Deployment != "" {
		files = append(files, GeneratedFile{Path: "base/deployment.yaml", Content: bundle.Deployment})
		baseResources = append(baseResources, "deployment.yaml")
	}
	if bundle.Service != "" {
		files = append(files, GeneratedFile{Path: "base/service.yaml", Content: bundle.Service})
		baseResources = append(baseResources, "service.yaml")
	}
	if bundle.NetworkPolicy != "" {
		files = append(files, GeneratedFile{Path: "base/networkpolicy.yaml", Content: bundle.NetworkPolicy})
		baseResources = append(baseResources, "networkpolicy.yaml")
	}
	if bundle.PodDisruption != "" {
		files = append(files, GeneratedFile{Path: "base/pdb.yaml", Content: bundle.PodDisruption})
		baseResources = append(baseResources, "pdb.yaml")
	}

	// Base kustomization.yaml
	files = append(files, GeneratedFile{
		Path:    "base/kustomization.yaml",
		Content: generateBaseKustomization(baseResources),
	})

	// Environment overlay kustomization.yaml and patch.yaml
	overlayDir := fmt.Sprintf("overlays/%s", env)
	files = append(files, GeneratedFile{
		Path:    fmt.Sprintf("%s/kustomization.yaml", overlayDir),
		Content: generateOverlayKustomization(ns, env),
	})
	files = append(files, GeneratedFile{
		Path:    fmt.Sprintf("%s/patch.yaml", overlayDir),
		Content: generateOverlayPatch(spec, appName, env),
	})

	// Engine-specific GitOps sync manifests
	var syncPlan string
	switch engine {
	case EngineArgoCD:
		appYAML := generateArgoCDApplication(appName, env, ns, repoURL, branch, targetPath)
		files = append(files, GeneratedFile{
			Path:    "argocd/application.yaml",
			Content: appYAML,
		})
		syncPlan = fmt.Sprintf(
			"ArgoCD Application '%s-%s' will sync %s@%s (%s) -> namespace '%s' (automated prune & self-heal)",
			appName, env, repoURL, branch, targetPath, ns,
		)
	case EngineFlux:
		gitRepoYAML := generateFluxGitRepository(appName, repoURL, branch)
		kustYAML := generateFluxKustomization(appName, env, ns, targetPath)
		files = append(files,
			GeneratedFile{Path: "flux/gitrepository.yaml", Content: gitRepoYAML},
			GeneratedFile{Path: "flux/kustomization.yaml", Content: kustYAML},
		)
		syncPlan = fmt.Sprintf(
			"FluxCD GitRepository '%s-source' (%s@%s) + Kustomization '%s-%s' (./%s) -> namespace '%s' (prune enabled)",
			appName, repoURL, branch, appName, env, targetPath, ns,
		)
	}

	if strings.TrimSpace(opts.OutputDir) != "" {
		absRoot, err := filepath.Abs(opts.OutputDir)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve output directory: %w", err)
		}
		prefix := absRoot + string(filepath.Separator)

		for _, f := range files {
			dest := filepath.Join(absRoot, filepath.FromSlash(f.Path))
			absDest, err := filepath.Abs(dest)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve path %s: %w", f.Path, err)
			}
			if !strings.HasPrefix(absDest, prefix) {
				return nil, fmt.Errorf("path traversal detected for %s", f.Path)
			}
			if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
				return nil, fmt.Errorf("failed to create directory for %s: %w", f.Path, err)
			}
			if err := os.WriteFile(absDest, []byte(f.Content), 0o644); err != nil {
				return nil, fmt.Errorf("failed to write %s: %w", f.Path, err)
			}
		}
	}

	return &ExportResult{
		Engine:      engine,
		AppName:     appName,
		Environment: env,
		Namespace:   ns,
		RepoURL:     repoURL,
		Branch:      branch,
		TargetPath:  targetPath,
		OutputDir:   opts.OutputDir,
		Files:       files,
		SyncPlan:    syncPlan,
	}, nil
}

func generateBaseKustomization(resources []string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\n")
	buf.WriteString("kind: Kustomization\n")
	buf.WriteString("resources:\n")
	for _, r := range resources {
		buf.WriteString(fmt.Sprintf("- %s\n", r))
	}
	return buf.String()
}

func generateOverlayKustomization(ns, env string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\n")
	buf.WriteString("kind: Kustomization\n")
	buf.WriteString(fmt.Sprintf("namespace: %s\n", ns))
	buf.WriteString("resources:\n")
	buf.WriteString("- ../../base\n")
	buf.WriteString("commonLabels:\n")
	buf.WriteString(fmt.Sprintf("  environment: %s\n", env))
	buf.WriteString("patches:\n")
	buf.WriteString("- path: patch.yaml\n")
	return buf.String()
}

func generateOverlayPatch(spec *config.AppSpec, appName, env string) string {
	replicas := spec.Runtime.Replicas.Min
	if replicas <= 0 {
		if strings.EqualFold(env, "prod") || strings.EqualFold(env, "production") {
			replicas = 2
		} else {
			replicas = 1
		}
	}

	var buf bytes.Buffer
	buf.WriteString("apiVersion: apps/v1\n")
	buf.WriteString("kind: Deployment\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s\n", appName))
	buf.WriteString("spec:\n")
	buf.WriteString(fmt.Sprintf("  replicas: %d\n", replicas))
	buf.WriteString("  template:\n")
	buf.WriteString("    spec:\n")
	buf.WriteString("      containers:\n")
	buf.WriteString(fmt.Sprintf("      - name: %s\n", appName))
	buf.WriteString("        env:\n")
	buf.WriteString("        - name: SKIPPER_ENV\n")
	buf.WriteString(fmt.Sprintf("          value: %q\n", env))
	return buf.String()
}

func generateArgoCDApplication(appName, env, ns, repoURL, branch, targetPath string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: argoproj.io/v1alpha1\n")
	buf.WriteString("kind: Application\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-%s\n", appName, env))
	buf.WriteString("  namespace: argocd\n")
	buf.WriteString("spec:\n")
	buf.WriteString("  project: default\n")
	buf.WriteString("  source:\n")
	buf.WriteString(fmt.Sprintf("    repoURL: %s\n", repoURL))
	buf.WriteString(fmt.Sprintf("    targetRevision: %s\n", branch))
	buf.WriteString(fmt.Sprintf("    path: %s\n", targetPath))
	buf.WriteString("  destination:\n")
	buf.WriteString("    server: https://kubernetes.default.svc\n")
	buf.WriteString(fmt.Sprintf("    namespace: %s\n", ns))
	buf.WriteString("  syncPolicy:\n")
	buf.WriteString("    automated:\n")
	buf.WriteString("      prune: true\n")
	buf.WriteString("      selfHeal: true\n")
	buf.WriteString("    syncOptions:\n")
	buf.WriteString("    - CreateNamespace=true\n")
	return buf.String()
}

func generateFluxGitRepository(appName, repoURL, branch string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: source.toolkit.fluxcd.io/v1\n")
	buf.WriteString("kind: GitRepository\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-source\n", appName))
	buf.WriteString("  namespace: flux-system\n")
	buf.WriteString("spec:\n")
	buf.WriteString("  interval: 1m0s\n")
	buf.WriteString(fmt.Sprintf("  url: %s\n", repoURL))
	buf.WriteString("  ref:\n")
	buf.WriteString(fmt.Sprintf("    branch: %s\n", branch))
	return buf.String()
}

func generateFluxKustomization(appName, env, ns, targetPath string) string {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: kustomize.toolkit.fluxcd.io/v1\n")
	buf.WriteString("kind: Kustomization\n")
	buf.WriteString("metadata:\n")
	buf.WriteString(fmt.Sprintf("  name: %s-%s\n", appName, env))
	buf.WriteString("  namespace: flux-system\n")
	buf.WriteString("spec:\n")
	buf.WriteString("  interval: 5m0s\n")
	buf.WriteString(fmt.Sprintf("  path: ./%s\n", targetPath))
	buf.WriteString("  prune: true\n")
	buf.WriteString(fmt.Sprintf("  targetNamespace: %s\n", ns))
	buf.WriteString("  sourceRef:\n")
	buf.WriteString("    kind: GitRepository\n")
	buf.WriteString(fmt.Sprintf("    name: %s-source\n", appName))
	return buf.String()
}
