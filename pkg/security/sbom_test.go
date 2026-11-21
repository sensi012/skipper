package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSBOMFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    SBOMFormat
		wantErr bool
	}{
		{"", FormatCycloneDX, false},
		{"cyclonedx", FormatCycloneDX, false},
		{"cyclonedx-json", FormatCycloneDX, false},
		{"CycloneDX", FormatCycloneDX, false},
		{"spdx", FormatSPDX, false},
		{"spdx-json", FormatSPDX, false},
		{"SPDX", FormatSPDX, false},
		{"xml", "", true},
	}

	for _, tc := range tests {
		got, err := ParseSBOMFormat(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ParseSBOMFormat(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Fatalf("ParseSBOMFormat(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSBOMGenerator_NativeFallbackDirectory(t *testing.T) {
	dir := t.TempDir()

	goMod := `module example.com/orders

go 1.22

require github.com/spf13/cobra v1.8.0

require (
	gopkg.in/yaml.v3 v3.0.1 // indirect
	github.com/fsnotify/fsnotify v1.7.0
)
`
	pkgJSON := `{
  "name": "web-ui",
  "dependencies": {
    "react": "^18.2.0"
  },
  "devDependencies": {
    "vitest": "~1.6.0"
  }
}`
	reqTxt := `# comment
flask==3.0.2
requests>=2.31.0
-r extra.txt
gunicorn
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte(reqTxt), 0644); err != nil {
		t.Fatal(err)
	}

	gen := &SBOMGenerator{
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("tool not installed")
		},
	}

	t.Run("CycloneDX 1.5 with file output", func(t *testing.T) {
		outFile := filepath.Join(t.TempDir(), "nested", "sbom.cdx.json")
		report, err := gen.Generate(context.Background(), SBOMOptions{
			Target:     dir,
			Format:     FormatCycloneDX,
			OutputPath: outFile,
		})
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		if report.Tool != "skipper-native" || report.Format != FormatCycloneDX {
			t.Fatalf("unexpected report header: %+v", report)
		}
		// 3 go + 2 npm + 3 pypi = 8 components
		if report.ComponentCount != 8 {
			t.Fatalf("expected 8 components, got %d", report.ComponentCount)
		}
		if !json.Valid(report.Document) {
			t.Fatalf("generated document is not valid JSON")
		}

		var doc struct {
			BOMFormat   string `json:"bomFormat"`
			SpecVersion string `json:"specVersion"`
			Components  []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				PURL    string `json:"purl"`
			} `json:"components"`
		}
		if err := json.Unmarshal(report.Document, &doc); err != nil {
			t.Fatalf("unmarshal CycloneDX: %v", err)
		}
		if doc.BOMFormat != "CycloneDX" || doc.SpecVersion != "1.5" || len(doc.Components) != 8 {
			t.Fatalf("unexpected CycloneDX structure: format=%s spec=%s len=%d", doc.BOMFormat, doc.SpecVersion, len(doc.Components))
		}

		written, err := os.ReadFile(outFile)
		if err != nil || string(written) != string(report.Document) {
			t.Fatalf("expected SBOM written to %s, err=%v", outFile, err)
		}
	})

	t.Run("SPDX 2.3", func(t *testing.T) {
		report, err := gen.Generate(context.Background(), SBOMOptions{
			Target: dir,
			Format: "spdx",
		})
		if err != nil {
			t.Fatalf("Generate SPDX failed: %v", err)
		}
		if report.Format != FormatSPDX || report.ComponentCount != 8 {
			t.Fatalf("unexpected SPDX report: format=%s count=%d", report.Format, report.ComponentCount)
		}
		var doc struct {
			SPDXVersion string `json:"spdxVersion"`
			DataLicense string `json:"dataLicense"`
			Packages    []struct {
				SPDXID      string `json:"SPDXID"`
				Name        string `json:"name"`
				VersionInfo string `json:"versionInfo"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(report.Document, &doc); err != nil {
			t.Fatalf("unmarshal SPDX: %v", err)
		}
		if doc.SPDXVersion != "SPDX-2.3" || doc.DataLicense != "CC0-1.0" || len(doc.Packages) != 8 {
			t.Fatalf("unexpected SPDX doc: %+v", doc)
		}
	})
}

func TestSBOMGenerator_NativeFallbackOCIImage(t *testing.T) {
	gen := &SBOMGenerator{
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("not installed")
		},
	}

	report, err := gen.Generate(context.Background(), SBOMOptions{
		Target: "ghcr.io/acme/payments:v2.1.0",
		Format: FormatCycloneDX,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.ComponentCount != 1 || report.Tool != "skipper-native" {
		t.Fatalf("unexpected OCI report: %+v", report)
	}
	if !strings.Contains(string(report.Document), "pkg:oci/ghcr.io/acme/payments@v2.1.0") {
		t.Fatalf("expected OCI PURL in SBOM, got:\n%s", string(report.Document))
	}

	// Digest reference
	name, ver := parseImageRef("ghcr.io/acme/app@sha256:abcdef")
	if name != "ghcr.io/acme/app" || ver != "sha256:abcdef" {
		t.Fatalf("unexpected digest parse: %s %s", name, ver)
	}
	// Bare image without tag
	name, ver = parseImageRef("alpine")
	if name != "alpine" || ver != "latest" {
		t.Fatalf("unexpected bare image parse: %s %s", name, ver)
	}
}

func TestSBOMGenerator_ExternalTools(t *testing.T) {
	t.Run("syft preferred when available", func(t *testing.T) {
		syftOut := `{"bomFormat":"CycloneDX","specVersion":"1.5","components":[{"name":"a"},{"name":"b"}]}`
		gen := &SBOMGenerator{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "syft" && args[0] == "version" {
					return []byte("syft 1.0.0"), nil
				}
				if name == "syft" {
					return []byte(syftOut), nil
				}
				return nil, errors.New("unexpected")
			},
		}

		report, err := gen.Generate(context.Background(), SBOMOptions{Target: "alpine:3.19", Format: FormatCycloneDX})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.Tool != "syft" || report.ComponentCount != 2 {
			t.Fatalf("expected syft with 2 components, got %+v", report)
		}
	})

	t.Run("docker sbom used when syft absent", func(t *testing.T) {
		spdxOut := `{"spdxVersion":"SPDX-2.3","packages":[{"name":"busybox"}]}`
		gen := &SBOMGenerator{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "syft" {
					return nil, errors.New("syft not found")
				}
				if name == "docker" && len(args) >= 2 && args[1] == "--version" {
					return []byte("0.6.0"), nil
				}
				return []byte(spdxOut), nil
			},
		}

		report, err := gen.Generate(context.Background(), SBOMOptions{Target: "alpine:3.19", Format: FormatSPDX})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.Tool != "docker-sbom" || report.ComponentCount != 1 {
			t.Fatalf("expected docker-sbom with 1 package, got %+v", report)
		}
	})

	t.Run("canceled context and invalid format return errors", func(t *testing.T) {
		gen := NewSBOMGenerator()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := gen.Generate(ctx, SBOMOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if _, err := gen.Generate(context.Background(), SBOMOptions{Format: "invalid"}); err == nil {
			t.Fatal("expected error for invalid format")
		}
	})
}
