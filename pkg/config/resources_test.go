package config

import "testing"

func TestNormalizeKubernetesMemory(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"   ":      "",
		"512m":     "512Mi",
		"512mb":    "512Mi",
		"512MB":    "512Mi",
		"512MiB":   "512Mi",
		"512mib":   "512Mi",
		"512Mi":    "512Mi",
		"500M":     "500M",
		"1g":       "1Gi",
		"1gb":      "1Gi",
		"1GB":      "1Gi",
		"1GiB":     "1Gi",
		"1Gi":      "1Gi",
		"2G":       "2G",
		"256K":     "256k",
		"256k":     "256k",
		"256kb":    "256Ki",
		"256KiB":   "256Ki",
		"256Ki":    "256Ki",
		"2t":       "2Ti",
		"2tb":      "2Ti",
		"2Ti":      "2Ti",
		"1048576b": "1048576",
		"1048576":  "1048576",
		"invalid":  "invalid",
	}

	for in, want := range cases {
		if got := NormalizeKubernetesMemory(in); got != want {
			t.Errorf("NormalizeKubernetesMemory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeKubernetesCPU(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"0.5":   "500m",
		"0.25":  "250m",
		"1.5":   "1500m",
		"1.0":   "1",
		"2":     "2",
		"500m":  "500m",
		"1000n": "1000n",
		"bad":   "bad",
	}

	for in, want := range cases {
		if got := NormalizeKubernetesCPU(in); got != want {
			t.Errorf("NormalizeKubernetesCPU(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResourceApproxHelpers(t *testing.T) {
	b1, ok1 := MemoryBytesApprox("512m")
	b2, ok2 := MemoryBytesApprox("512Mi")
	if !ok1 || !ok2 || b1 != b2 || b1 != 512*1024*1024 {
		t.Fatalf("unexpected memory bytes approx: %v/%v vs %v/%v", b1, ok1, b2, ok2)
	}
	if _, ok := MemoryBytesApprox("not-a-size"); ok {
		t.Error("expected false for invalid memory size")
	}

	c1, ok := CPUMillicoresApprox("0.5")
	if !ok || c1 != 500 {
		t.Fatalf("expected 500m for 0.5 CPU, got %v (ok=%v)", c1, ok)
	}
}
