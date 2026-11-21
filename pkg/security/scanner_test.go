package security

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewTrivyScanner(t *testing.T) {
	scanner := NewTrivyScanner()
	if scanner == nil {
		t.Fatalf("expected non-nil scanner")
	}
}

func TestVulnerabilityScanResultFields(t *testing.T) {
	res := VulnerabilityScanResult{
		ImageScanned: "app:latest",
		ScannerUsed:  "trivy",
		Critical:     0,
		High:         1,
		Medium:       2,
		Low:          3,
		Passed:       true,
		Details:      "1 high CVE found",
	}

	if res.ImageScanned != "app:latest" || !res.Passed || res.High != 1 || res.Medium != 2 || res.Low != 3 {
		t.Fatalf("mismatch in scan result fields: %+v", res)
	}
}

func TestParseSeverityCounts(t *testing.T) {
	tests := []struct {
		name                             string
		input                            string
		wantCrit, wantHigh, wantMed, low int
	}{
		{
			name:  "empty output",
			input: "",
		},
		{
			name:  "trivy clean summary line ignored",
			input: "alpine:3.19 (alpine 3.19.1)\n============================\nTotal: 0 (HIGH: 0, CRITICAL: 0)\n",
		},
		{
			name: "trivy table with mixed severities and summary",
			input: strings.Join([]string{
				"Total: 4 (HIGH: 2, CRITICAL: 1, MEDIUM: 1)",
				"| LIBRARY | VULNERABILITY | SEVERITY | STATUS |",
				"| openssl | CVE-2024-0001 | CRITICAL | fixed  |",
				"| curl    | CVE-2024-0002 | HIGH     | fixed  |",
				"| zlib    | CVE-2024-0003 | HIGH     | fixed  |",
				"| busybox | CVE-2024-0004 | MEDIUM   | fixed  |",
				"| musl    | CVE-2024-0005 | LOW      | fixed  |",
			}, "\n"),
			wantCrit: 1,
			wantHigh: 2,
			wantMed:  1,
			low:      1,
		},
		{
			name: "docker scout findings",
			input: strings.Join([]string{
				"✗ CRITICAL CVE-2024-1111 pkg:apk/alpine/openssl",
				"✗ CRITICAL CVE-2024-2222 pkg:apk/alpine/libcrypto",
				"✗ HIGH     CVE-2024-3333 pkg:apk/alpine/curl",
			}, "\n"),
			wantCrit: 2,
			wantHigh: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, h, m, l := parseSeverityCounts(tc.input)
			if c != tc.wantCrit || h != tc.wantHigh || m != tc.wantMed || l != tc.low {
				t.Fatalf("got (C=%d H=%d M=%d L=%d), want (C=%d H=%d M=%d L=%d)",
					c, h, m, l, tc.wantCrit, tc.wantHigh, tc.wantMed, tc.low)
			}
		})
	}
}

func TestScanImage_ValidationAndContext(t *testing.T) {
	scanner := &TrivyScanner{
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("not found")
		},
	}

	if _, err := scanner.ScanImage(context.Background(), "   ", &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for empty image name")
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.ScanImage(canceledCtx, "app:latest", &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestScanImage_TrivyPaths(t *testing.T) {
	t.Run("trivy clean image passes", func(t *testing.T) {
		var calls [][]string
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, args...))
				if name == "trivy" && len(args) > 0 && args[0] == "--version" {
					return []byte("Version: 0.50.0"), nil
				}
				return []byte("Total: 0 (HIGH: 0, CRITICAL: 0)\n"), nil
			},
		}

		var out bytes.Buffer
		res, err := scanner.ScanImage(context.Background(), "myapp:1.0", &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ScannerUsed != "trivy" || !res.Passed || res.Critical != 0 || res.High != 0 {
			t.Fatalf("unexpected result: %+v", res)
		}
		if !strings.Contains(out.String(), "Trivy security engine") {
			t.Fatalf("expected trivy log output, got %q", out.String())
		}
		if len(calls) != 2 || calls[1][0] != "trivy" || calls[1][1] != "image" {
			t.Fatalf("unexpected command invocations: %v", calls)
		}
	})

	t.Run("trivy high only still passes gate", func(t *testing.T) {
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "--version" {
					return []byte("Version: 0.50.0"), nil
				}
				return []byte("| curl | CVE-2024-0001 | HIGH | fixed |\n"), nil
			},
		}

		res, err := scanner.ScanImage(context.Background(), "myapp:1.0", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Passed || res.High != 1 || res.Critical != 0 {
			t.Fatalf("expected Passed=true with 1 HIGH, got %+v", res)
		}
	})

	t.Run("trivy critical blocks deployment", func(t *testing.T) {
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "--version" {
					return []byte("Version: 0.50.0"), nil
				}
				return []byte("| openssl | CVE-2024-9999 | CRITICAL | fixed |\n| curl | CVE-2024-8888 | HIGH | fixed |\n"), nil
			},
		}

		var out bytes.Buffer
		res, err := scanner.ScanImage(context.Background(), "myapp:vuln", &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Passed || res.Critical != 1 || res.High != 1 || res.ScannerUsed != "trivy" {
			t.Fatalf("expected failed gate with 1 CRITICAL and 1 HIGH, got %+v", res)
		}
	})

	t.Run("trivy canceled mid-scan returns context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "--version" {
					return []byte("Version: 0.50.0"), nil
				}
				cancel()
				return nil, context.Canceled
			},
		}

		if _, err := scanner.ScanImage(ctx, "myapp:1.0", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestScanImage_DockerScoutPaths(t *testing.T) {
	t.Run("docker scout clean passes", func(t *testing.T) {
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "trivy" {
					return nil, errors.New("trivy not installed")
				}
				if name == "docker" && len(args) >= 2 && args[0] == "scout" && args[1] == "version" {
					return []byte("v1.5.0"), nil
				}
				return []byte("0C 0H 0M 0L\n"), nil
			},
		}

		var out bytes.Buffer
		res, err := scanner.ScanImage(context.Background(), "myapp:1.0", &out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ScannerUsed != "docker-scout" || !res.Passed || res.Critical != 0 {
			t.Fatalf("unexpected docker-scout result: %+v", res)
		}
		if !strings.Contains(out.String(), "Docker Scout") {
			t.Fatalf("expected Docker Scout log output, got %q", out.String())
		}
	})

	t.Run("docker scout critical fails gate", func(t *testing.T) {
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "trivy" {
					return nil, errors.New("trivy not installed")
				}
				if name == "docker" && len(args) >= 2 && args[1] == "version" {
					return []byte("v1.5.0"), nil
				}
				return []byte("✗ CRITICAL CVE-2024-1234\n✗ HIGH CVE-2024-5678\n"), nil
			},
		}

		res, err := scanner.ScanImage(context.Background(), "myapp:bad", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ScannerUsed != "docker-scout" || res.Passed || res.Critical != 1 || res.High != 1 {
			t.Fatalf("expected failed docker-scout gate, got %+v", res)
		}
	})

	t.Run("docker scout canceled mid-scan returns context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		scanner := &TrivyScanner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "trivy" {
					return nil, errors.New("trivy not installed")
				}
				if name == "docker" && len(args) >= 2 && args[1] == "version" {
					return []byte("v1.5.0"), nil
				}
				cancel()
				return nil, context.Canceled
			},
		}

		if _, err := scanner.ScanImage(ctx, "myapp:1.0", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestScanImage_StaticFallback(t *testing.T) {
	scanner := &TrivyScanner{
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("binary not found")
		},
	}

	var out bytes.Buffer
	res, err := scanner.ScanImage(context.Background(), "myapp:static", &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ScannerUsed != "internal-static-auditor" || !res.Passed || res.Critical != 0 || res.High != 0 {
		t.Fatalf("unexpected fallback result: %+v", res)
	}
	if !strings.Contains(out.String(), "static OCI manifest baseline") {
		t.Fatalf("expected static audit log output, got %q", out.String())
	}
}
