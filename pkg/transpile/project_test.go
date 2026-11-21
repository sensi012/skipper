package transpile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindComposeFileOrderAndErrors(t *testing.T) {
	t.Run("priority order", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("services:\n  a:\n    image: alpine\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		// Expect compose.yaml first
		got, err := FindComposeFile(dir)
		if err != nil || filepath.Base(got) != "compose.yaml" {
			t.Fatalf("expected compose.yaml, got %q (err=%v)", got, err)
		}

		// Remove compose.yaml -> expect compose.yml
		_ = os.Remove(filepath.Join(dir, "compose.yaml"))
		got, err = FindComposeFile(dir)
		if err != nil || filepath.Base(got) != "compose.yml" {
			t.Fatalf("expected compose.yml, got %q (err=%v)", got, err)
		}

		// Remove compose.yml -> expect docker-compose.yaml
		_ = os.Remove(filepath.Join(dir, "compose.yml"))
		got, err = FindComposeFile(dir)
		if err != nil || filepath.Base(got) != "docker-compose.yaml" {
			t.Fatalf("expected docker-compose.yaml, got %q (err=%v)", got, err)
		}

		// Remove docker-compose.yaml -> expect docker-compose.yml
		_ = os.Remove(filepath.Join(dir, "docker-compose.yaml"))
		got, err = FindComposeFile(dir)
		if err != nil || filepath.Base(got) != "docker-compose.yml" {
			t.Fatalf("expected docker-compose.yml, got %q (err=%v)", got, err)
		}
	})

	t.Run("missing error lists all four candidates", func(t *testing.T) {
		dir := t.TempDir()
		_, err := FindComposeFile(dir)
		if err == nil {
			t.Fatal("expected error when no compose file exists")
		}
		for _, cand := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
			if !strings.Contains(err.Error(), cand) {
				t.Errorf("error %q missing candidate %q", err.Error(), cand)
			}
		}
	})
}

func TestLoadComposeProjectThreeTierFixture(t *testing.T) {
	t.Setenv("REGISTRY", "")
	t.Setenv("TAG", "")
	t.Setenv("FEATURE_FLAG", "")
	// Ensure empty OS env doesn't shadow .env; actually unset them cleanly
	origReg, hasReg := os.LookupEnv("REGISTRY")
	_ = os.Unsetenv("REGISTRY")
	t.Cleanup(func() {
		if hasReg {
			_ = os.Setenv("REGISTRY", origReg)
		}
	})
	origTag, hasTag := os.LookupEnv("TAG")
	_ = os.Unsetenv("TAG")
	t.Cleanup(func() {
		if hasTag {
			_ = os.Setenv("TAG", origTag)
		}
	})
	origFF, hasFF := os.LookupEnv("FEATURE_FLAG")
	_ = os.Unsetenv("FEATURE_FLAG")
	t.Cleanup(func() {
		if hasFF {
			_ = os.Setenv("FEATURE_FLAG", origFF)
		}
	})

	fixturePath := filepath.Join("testdata", "three-tier", "docker-compose.yml")
	proj, err := LoadComposeProject(fixturePath)
	if err != nil {
		t.Fatalf("LoadComposeProject failed: %v", err)
	}

	if proj.Name != "three-tier" {
		t.Errorf("expected project name 'three-tier', got %q", proj.Name)
	}
	if len(proj.Services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(proj.Services))
	}

	// Verify topological order: db -> backend -> frontend
	order := []string{proj.Services[0].Name, proj.Services[1].Name, proj.Services[2].Name}
	if order[0] != "db" || order[1] != "backend" || order[2] != "frontend" {
		t.Fatalf("expected topological order [db backend frontend], got %v", order)
	}

	dbSvc := proj.Services[0]
	if dbSvc.HasBuild || dbSvc.Image != "postgres:16" {
		t.Errorf("unexpected db service: %+v", dbSvc)
	}
	if len(dbSvc.Spec.Dependencies.Storage) != 1 || dbSvc.Spec.Dependencies.Storage[0].MountPath != "/var/lib/postgresql/data" {
		t.Errorf("expected long-syntax volume mount /var/lib/postgresql/data, got %+v", dbSvc.Spec.Dependencies.Storage)
	}

	backendSvc := proj.Services[1]
	wantBackendCtx := filepath.Join("testdata", "three-tier", "backend")
	if !backendSvc.HasBuild || backendSvc.Context != wantBackendCtx {
		t.Errorf("expected backend context %q, got %q", wantBackendCtx, backendSvc.Context)
	}
	if backendSvc.Dockerfile != "Dockerfile.api" || backendSvc.Target != "prod" {
		t.Errorf("expected dockerfile=Dockerfile.api target=prod, got %q %q", backendSvc.Dockerfile, backendSvc.Target)
	}
	if backendSvc.Image != "local/shop-api:1.2.3" {
		t.Errorf("expected interpolated image 'local/shop-api:1.2.3', got %q", backendSvc.Image)
	}
	if backendSvc.Args["BUILD_ENV"] != "production" || backendSvc.Args["TAG"] != "1.2.3" {
		t.Errorf("unexpected backend build args: %+v", backendSvc.Args)
	}
	if _, exists := backendSvc.Args["UNSET_ARG"]; exists {
		t.Errorf("expected unset arg UNSET_ARG to be omitted, got %+v", backendSvc.Args)
	}
	if len(backendSvc.Spec.Endpoints) != 1 || backendSvc.Spec.Endpoints[0].Port != 8080 || !backendSvc.Spec.Endpoints[0].Public {
		t.Errorf("expected backend endpoint port 8080 public, got %+v", backendSvc.Spec.Endpoints)
	}
	if len(backendSvc.Spec.Env) != 2 {
		t.Errorf("expected 2 env vars (LOG_LEVEL and FEATURE_FLAG from .env), got %+v", backendSvc.Spec.Env)
	}

	frontendSvc := proj.Services[2]
	wantFrontendCtx := filepath.Join("testdata", "three-tier", "frontend")
	if !frontendSvc.HasBuild || frontendSvc.Context != wantFrontendCtx {
		t.Errorf("expected frontend context %q, got %q", wantFrontendCtx, frontendSvc.Context)
	}
	if len(frontendSvc.Spec.Endpoints) != 1 || frontendSvc.Spec.Endpoints[0].Port != 80 || !frontendSvc.Spec.Endpoints[0].Public {
		t.Errorf("expected long-syntax port 80 public, got %+v", frontendSvc.Spec.Endpoints)
	}
}

func TestLoadComposeProjectPrecedenceInterpolationAndEdgeCases(t *testing.T) {
	t.Run("project name precedence and sanitization", func(t *testing.T) {
		dir := t.TempDir()
		composePath := filepath.Join(dir, "docker-compose.yml")

		// 1. compose name: wins over COMPOSE_PROJECT_NAME and dir
		t.Setenv("COMPOSE_PROJECT_NAME", "EnvProj")
		_ = os.WriteFile(composePath, []byte("name: My.Awesome_App!\nservices:\n  a:\n    image: alpine\n"), 0644)
		proj, err := LoadComposeProject(composePath)
		if err != nil {
			t.Fatal(err)
		}
		if proj.Name != "myawesome_app" {
			t.Errorf("expected sanitized name 'myawesome_app', got %q", proj.Name)
		}

		// 2. COMPOSE_PROJECT_NAME wins over dir when name: omitted
		_ = os.WriteFile(composePath, []byte("services:\n  a:\n    image: alpine\n"), 0644)
		proj, err = LoadComposeProject(composePath)
		if err != nil {
			t.Fatal(err)
		}
		if proj.Name != "envproj" {
			t.Errorf("expected 'envproj', got %q", proj.Name)
		}
	})

	t.Run("interpolation rules and OS env over .env", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("TAG=from-dotenv\nEMPTY=\n"), 0644)
		t.Setenv("TAG", "from-os")

		composeYAML := `
services:
  app:
    image: repo/app:${TAG:-fallback}
    build:
      context: ${CTX_DIR:-.}
      target: ${EMPTY-dash-only}
      args:
        LITERAL_DOLLAR: "$$100"
        PLAIN: "$TAG"
        EMPTY_COLON: "${EMPTY:-was-empty}"
        NIL_ARG:
`
		composePath := filepath.Join(dir, "compose.yml")
		_ = os.WriteFile(composePath, []byte(composeYAML), 0644)

		proj, err := LoadComposeProject(composePath)
		if err != nil {
			t.Fatal(err)
		}
		svc := proj.Services[0]
		if svc.Image != "repo/app:from-os" {
			t.Errorf("expected OS env override 'repo/app:from-os', got %q", svc.Image)
		}
		if svc.Context != dir {
			t.Errorf("expected default build context %q, got %q", dir, svc.Context)
		}
		if svc.Target != "" {
			t.Errorf("expected ${EMPTY-dash-only} when EMPTY='' to resolve to '', got %q", svc.Target)
		}
		if svc.Args["LITERAL_DOLLAR"] != "$100" {
			t.Errorf("expected $$100 -> $100, got %q", svc.Args["LITERAL_DOLLAR"])
		}
		if svc.Args["PLAIN"] != "from-os" {
			t.Errorf("expected $TAG -> from-os, got %q", svc.Args["PLAIN"])
		}
		if svc.Args["EMPTY_COLON"] != "was-empty" {
			t.Errorf("expected ${EMPTY:-was-empty} -> was-empty, got %q", svc.Args["EMPTY_COLON"])
		}
	})

	t.Run("dependency cycle and unknown depends_on fall back to alphabetical", func(t *testing.T) {
		dir := t.TempDir()
		composeYAML := `
services:
  zeta:
    image: alpine
    depends_on: [alpha]
  alpha:
    image: alpine
    depends_on: [zeta]
`
		p := filepath.Join(dir, "compose.yaml")
		_ = os.WriteFile(p, []byte(composeYAML), 0644)
		proj, err := LoadComposeProject(p)
		if err != nil {
			t.Fatalf("unexpected error on cycle: %v", err)
		}
		if proj.Services[0].Name != "alpha" || proj.Services[1].Name != "zeta" {
			t.Errorf("expected alphabetical fallback [alpha zeta], got [%s %s]", proj.Services[0].Name, proj.Services[1].Name)
		}

		// Unknown dependency
		composeYAML = `
services:
  beta:
    image: alpine
    depends_on: [missing]
  alpha:
    image: alpine
`
		_ = os.WriteFile(p, []byte(composeYAML), 0644)
		proj, err = LoadComposeProject(p)
		if err != nil {
			t.Fatalf("unexpected error on unknown dep: %v", err)
		}
		if proj.Services[0].Name != "alpha" || proj.Services[1].Name != "beta" {
			t.Errorf("expected alphabetical fallback [alpha beta], got [%s %s]", proj.Services[0].Name, proj.Services[1].Name)
		}
	})

	t.Run("remote context flagged", func(t *testing.T) {
		dir := t.TempDir()
		composeYAML := `
services:
  https_svc:
    build: https://github.com/org/repo.git#main
  git_svc:
    build:
      context: git@github.com:org/repo.git
`
		p := filepath.Join(dir, "compose.yaml")
		_ = os.WriteFile(p, []byte(composeYAML), 0644)
		proj, err := LoadComposeProject(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, svc := range proj.Services {
			if !svc.Remote {
				t.Errorf("expected service %s to be marked Remote=true (context=%q)", svc.Name, svc.Context)
			}
		}
	})
}

func TestTranspileComposeDeterministicSelection(t *testing.T) {
	composeYAML := `
services:
  redis:
    image: redis:7
  zeta:
    build: ./zeta
  alpha:
    build: ./alpha
`
	tr := NewTranspiler()
	for i := 0; i < 10; i++ {
		spec, err := tr.TranspileCompose([]byte(composeYAML), "")
		if err != nil {
			t.Fatal(err)
		}
		if spec.Name != "alpha" {
			t.Fatalf("expected deterministic buildable selection 'alpha', got %q on run %d", spec.Name, i)
		}
	}

	// No build fields -> first alphabetically
	noBuildYAML := `
services:
  redis:
    image: redis:7
  api:
    image: myapi:1
`
	spec, err := tr.TranspileCompose([]byte(noBuildYAML), "")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "api" {
		t.Fatalf("expected deterministic fallback 'api', got %q", spec.Name)
	}
}
