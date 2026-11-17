package security

import "testing"

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
		Passed:       true,
		Details:      "1 high CVE found",
	}

	if res.ImageScanned != "app:latest" || !res.Passed {
		t.Fatalf("mismatch in scan result fields: %+v", res)
	}
}
