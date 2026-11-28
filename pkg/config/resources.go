package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func splitQuantity(s string) (numStr string, num float64, suffix string, ok bool) {
	i := 0
	dotSeen := false
	digitSeen := false
	for i < len(s) {
		c := s[i]
		if c >= '0' && c <= '9' {
			digitSeen = true
			i++
			continue
		}
		if c == '.' && !dotSeen {
			dotSeen = true
			i++
			continue
		}
		break
	}
	if !digitSeen || i == 0 {
		return "", 0, "", false
	}
	numStr = s[:i]
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
		return "", 0, "", false
	}
	return numStr, val, s[i:], true
}

// NormalizeKubernetesMemory converts Docker/Compose and human memory strings
// (e.g. "512m", "512mb", "1g", "1gb", "512MiB", "256K", "1048576b") into valid
// Kubernetes resource Quantity strings (e.g. "512Mi", "1Gi", "256k", "1048576").
// Already-valid Kubernetes Quantities (e.g. "512Mi", "1Gi", "500M", "1048576") are preserved.
func NormalizeKubernetesMemory(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	numStr, _, suffix, ok := splitQuantity(s)
	if !ok {
		return s
	}

	switch suffix {
	case "", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "k", "M", "G", "T", "P", "E":
		return numStr + suffix
	case "K":
		// Kubernetes decimalSI uses lowercase 'k'; uppercase 'K' is rejected by kubectl.
		return numStr + "k"
	case "m":
		// In Docker/Compose memory specs, lowercase 'm' means MiB (in K8s raw 'm' means millibytes).
		return numStr + "Mi"
	case "g":
		return numStr + "Gi"
	case "t":
		return numStr + "Ti"
	case "p":
		return numStr + "Pi"
	case "e":
		return numStr + "Ei"
	}

	switch strings.ToLower(suffix) {
	case "b":
		return numStr
	case "kib", "kb":
		return numStr + "Ki"
	case "mib", "mb":
		return numStr + "Mi"
	case "gib", "gb":
		return numStr + "Gi"
	case "tib", "tb":
		return numStr + "Ti"
	case "pib", "pb":
		return numStr + "Pi"
	case "eib", "eb":
		return numStr + "Ei"
	default:
		return s
	}
}

// NormalizeKubernetesCPU normalizes fractional or whole CPU strings (e.g. "0.5" -> "500m", "2" -> "2")
// into idiomatic Kubernetes CPU Quantities while preserving valid "m" or "n" quantities.
func NormalizeKubernetesCPU(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	numStr, val, suffix, ok := splitQuantity(s)
	if !ok || val <= 0 {
		return s
	}
	switch suffix {
	case "m", "n":
		return numStr + suffix
	case "":
		if math.Mod(val, 1.0) != 0 {
			milli := int64(math.Round(val * 1000.0))
			if milli < 1 {
				milli = 1
			}
			return fmt.Sprintf("%dm", milli)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return s
	}
}

// MemoryBytesApprox parses a memory quantity (normalizing Docker/Compose units first)
// and returns its approximate size in bytes.
func MemoryBytesApprox(raw string) (float64, bool) {
	norm := NormalizeKubernetesMemory(raw)
	if norm == "" {
		return 0, false
	}
	_, val, suffix, ok := splitQuantity(norm)
	if !ok {
		return 0, false
	}

	var mult float64
	switch suffix {
	case "":
		mult = 1
	case "k":
		mult = 1e3
	case "M":
		mult = 1e6
	case "G":
		mult = 1e9
	case "T":
		mult = 1e12
	case "P":
		mult = 1e15
	case "E":
		mult = 1e18
	case "Ki":
		mult = 1024
	case "Mi":
		mult = 1024 * 1024
	case "Gi":
		mult = 1024 * 1024 * 1024
	case "Ti":
		mult = 1024 * 1024 * 1024 * 1024
	case "Pi":
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	case "Ei":
		mult = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}
	return val * mult, true
}

// CPUMillicoresApprox parses a CPU quantity (normalizing decimal cores first)
// and returns its size in millicores.
func CPUMillicoresApprox(raw string) (float64, bool) {
	norm := NormalizeKubernetesCPU(raw)
	if norm == "" {
		return 0, false
	}
	_, val, suffix, ok := splitQuantity(norm)
	if !ok {
		return 0, false
	}
	switch suffix {
	case "":
		return val * 1000.0, true
	case "m":
		return val, true
	case "n":
		return val / 1e6, true
	default:
		return 0, false
	}
}
