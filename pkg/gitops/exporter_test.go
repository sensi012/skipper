package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sensi012/skipper/pkg/config"
	"gopkg.in/yaml.v3"
)

func TestExportArgoCDWithOutputDir(t *testing.T) {
	outDir := t.TempDir()
	spec := &config.AppSpec{
		Name:  "billing-api",
		Image: "ghcr.io/acme/billing-api:v1.2.0",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
		Runtime: config.RuntimeConfig{
			Replicas: config.ReplicaConfig{Min: 3},
		},
	}

	res, err := Export(spec, ExportOptions{
		Engine:      EngineArgoCD,
		RepoURL:     "https://github.com/acme/gitops-fleet.git",
		Branch:      "release",
		TargetPath:  "apps/billing/overlays/prod",
		Namespace:   "payments",
		Environment: "prod",
		OutputDir:   outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Engine != EngineArgoCD {
		t.Errorf("expected engine argocd, got %s", res.Engine)
	}
	if !strings.Contains(res.SyncPlan, "ArgoCD Application 'billing-api-prod'") {
		t.Errorf("unexpected sync plan: %s", res.SyncPlan)
	}

	fileMap := make(map[string]string, len(res.Files))
	for _, f := range res.Files {
		fileMap[f.Path] = f.Content

		// Verify valid YAML for every generated file
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(f.Content), &parsed); err != nil {
			t.Errorf("file %s is not valid YAML: %v\n%s", f.Path, err, f.Content)
		}

		// Verify file was written to OutputDir
		diskPath := filepath.Join(outDir, filepath.FromSlash(f.Path))
		raw, err := os.ReadFile(diskPath)
		if err != nil {
			t.Errorf("expected file %s on disk: %v", diskPath, err)
			continue
		}
		if string(raw) != f.Content {
			t.Errorf("disk content mismatch for %s", f.Path)
		}
	}

	expectedPaths := []string{
		"base/deployment.yaml",
		"base/service.yaml",
		"base/networkpolicy.yaml",
		"base/pdb.yaml",
		"base/kustomization.yaml",
		"overlays/prod/kustomization.yaml",
		"overlays/prod/patch.yaml",
		"argocd/application.yaml",
	}
	for _, p := range expectedPaths {
		if _, ok := fileMap[p]; !ok {
			t.Errorf("missing expected generated file %q", p)
		}
	}

	argo := fileMap["argocd/application.yaml"]
	for _, snippet := range []string{
		"apiVersion: argoproj.io/v1alpha1",
		"kind: Application",
		"name: billing-api-prod",
		"repoURL: https://github.com/acme/gitops-fleet.git",
		"targetRevision: release",
		"path: apps/billing/overlays/prod",
		"namespace: payments",
		"prune: true",
		"selfHeal: true",
	} {
		if !strings.Contains(argo, snippet) {
			t.Errorf("expected %q in argocd/application.yaml:\n%s", snippet, argo)
		}
	}
}

func TestExportFluxCD(t *testing.T) {
	outDir := t.TempDir()
	spec := &config.AppSpec{
		Name:        "search-worker",
		Environment: "staging",
	}

	res, err := Export(spec, ExportOptions{
		Engine:    EngineFlux,
		RepoURL:   "https://github.com/acme/fleet.git",
		Branch:    "main",
		Namespace: "search",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Engine != EngineFlux {
		t.Errorf("expected engine flux, got %s", res.Engine)
	}
	if res.Environment != "staging" {
		t.Errorf("expected environment staging from spec, got %s", res.Environment)
	}
	if !strings.Contains(res.SyncPlan, "FluxCD GitRepository 'search-worker-source'") {
		t.Errorf("unexpected flux sync plan: %s", res.SyncPlan)
	}

	fileMap := make(map[string]string, len(res.Files))
	for _, f := range res.Files {
		fileMap[f.Path] = f.Content
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(f.Content), &parsed); err != nil {
			t.Errorf("file %s is invalid YAML: %v", f.Path, err)
		}
	}

	gitRepo, ok := fileMap["flux/gitrepository.yaml"]
	if !ok {
		t.Fatal("missing flux/gitrepository.yaml")
	}
	for _, s := range []string{
		"apiVersion: source.toolkit.fluxcd.io/v1",
		"kind: GitRepository",
		"name: search-worker-source",
		"url: https://github.com/acme/fleet.git",
		"branch: main",
	} {
		if !strings.Contains(gitRepo, s) {
			t.Errorf("expected %q in flux/gitrepository.yaml:\n%s", s, gitRepo)
		}
	}

	fluxKust, ok := fileMap["flux/kustomization.yaml"]
	if !ok {
		t.Fatal("missing flux/kustomization.yaml")
	}
	for _, s := range []string{
		"apiVersion: kustomize.toolkit.fluxcd.io/v1",
		"kind: Kustomization",
		"name: search-worker-staging",
		"path: ./overlays/staging",
		"targetNamespace: search",
		"name: search-worker-source",
	} {
		if !strings.Contains(fluxKust, s) {
			t.Errorf("expected %q in flux/kustomization.yaml:\n%s", s, fluxKust)
		}
	}

	// Staging default replicas in overlay patch should be 1
	patch := fileMap["overlays/staging/patch.yaml"]
	if !strings.Contains(patch, "replicas: 1") {
		t.Errorf("expected replicas: 1 in staging overlay patch:\n%s", patch)
	}
}

func TestExportDefaultsAndValidation(t *testing.T) {
	spec := &config.AppSpec{Name: "demo-app"}
	res, err := Export(spec, ExportOptions{})
	if err != nil {
		t.Fatalf("unexpected error with defaults: %v", err)
	}
	if res.Engine != EngineArgoCD || res.Environment != "prod" || res.Namespace != "default" || res.Branch != "main" {
		t.Errorf("unexpected defaults: %+v", res)
	}

	// Prod default replicas in overlay patch should be 2 when spec.Runtime.Replicas.Min == 0
	var patchContent string
	for _, f := range res.Files {
		if f.Path == "overlays/prod/patch.yaml" {
			patchContent = f.Content
		}
	}
	if !strings.Contains(patchContent, "replicas: 2") {
		t.Errorf("expected prod default replicas: 2, got:\n%s", patchContent)
	}

	// Validation errors
	if _, err := Export(nil, ExportOptions{}); err == nil {
		t.Error("expected error for nil spec")
	}
	if _, err := Export(&config.AppSpec{}, ExportOptions{}); err == nil {
		t.Error("expected error for empty spec name")
	}
	if _, err := Export(spec, ExportOptions{Engine: Engine("unknown")}); err == nil {
		t.Error("expected error for unsupported engine")
	}
}

func TestExportSecurityAndTraversalPrevention(t *testing.T) {
	validSpec := &config.AppSpec{Name: "billing-api"}

	cases := []struct {
		name string
		spec *config.AppSpec
		opts ExportOptions
	}{
		{
			name: "environment path traversal",
			spec: validSpec,
			opts: ExportOptions{Environment: "../../etc", OutputDir: t.TempDir()},
		},
		{
			name: "environment slash separator",
			spec: validSpec,
			opts: ExportOptions{Environment: "prod/sub"},
		},
		{
			name: "app name path traversal",
			spec: &config.AppSpec{Name: "../malicious"},
			opts: ExportOptions{},
		},
		{
			name: "app name leading dash",
			spec: &config.AppSpec{Name: "-flag-app"},
			opts: ExportOptions{},
		},
		{
			name: "namespace injection",
			spec: validSpec,
			opts: ExportOptions{Namespace: "prod\n  injected: true"},
		},
		{
			name: "targetPath traversal",
			spec: validSpec,
			opts: ExportOptions{TargetPath: "../../secret"},
		},
		{
			name: "targetPath absolute path",
			spec: validSpec,
			opts: ExportOptions{TargetPath: "/etc/passwd"},
		},
		{
			name: "targetPath backslash",
			spec: validSpec,
			opts: ExportOptions{TargetPath: `overlays\..\secret`},
		},
		{
			name: "branch newline injection",
			spec: validSpec,
			opts: ExportOptions{Branch: "main\n    path: /hack"},
		},
		{
			name: "repoURL leading dash",
			spec: validSpec,
			opts: ExportOptions{RepoURL: "--upload-pack=evil"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Export(tc.spec, tc.opts); err == nil {
				t.Errorf("expected security validation error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestExportConcurrency(t *testing.T) {
	spec := &config.AppSpec{
		Name: "concurrent-app",
		Endpoints: []config.EndpointConfig{
			{Name: "http", Port: 8080},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			eng := EngineArgoCD
			if idx%2 == 1 {
				eng = EngineFlux
			}
			res, err := Export(spec, ExportOptions{
				Engine:      eng,
				Environment: "prod",
				Namespace:   "default",
			})
			if err != nil || len(res.Files) == 0 {
				t.Errorf("concurrent export %d failed: err=%v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}
