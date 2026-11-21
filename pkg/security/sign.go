package security

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// SignOptions configures OCI image signing via cosign.
type SignOptions struct {
	Image       string            `json:"image"`
	KeyRef      string            `json:"keyRef,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	DryRun      bool              `json:"dryRun,omitempty"`
	Yes         bool              `json:"yes,omitempty"`
}

// SignResult summarizes the outcome (or dry-run plan) of signing an OCI image.
type SignResult struct {
	Image       string            `json:"image"`
	Keyless     bool              `json:"keyless"`
	KeyRef      string            `json:"keyRef,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	DryRun      bool              `json:"dryRun"`
	Signed      bool              `json:"signed"`
	Command     []string          `json:"command"`
	Output      string            `json:"output,omitempty"`
}

// VerifyOptions configures OCI image signature verification via cosign.
type VerifyOptions struct {
	Image               string            `json:"image"`
	KeyRef              string            `json:"keyRef,omitempty"`
	CertificateIdentity string            `json:"certificateIdentity,omitempty"`
	CertificateIssuer   string            `json:"certificateIssuer,omitempty"`
	Annotations         map[string]string `json:"annotations,omitempty"`
	DryRun              bool              `json:"dryRun,omitempty"`
}

// VerifyResult summarizes the outcome (or dry-run plan) of verifying an OCI image signature.
type VerifyResult struct {
	Image    string   `json:"image"`
	Keyless  bool     `json:"keyless"`
	KeyRef   string   `json:"keyRef,omitempty"`
	DryRun   bool     `json:"dryRun"`
	Verified bool     `json:"verified"`
	Command  []string `json:"command"`
	Output   string   `json:"output,omitempty"`
}

// ImageSigner signs and verifies OCI container images using cosign.
type ImageSigner struct {
	runCmd commandRunner
}

// NewImageSigner creates a new cosign-backed OCI image signer.
func NewImageSigner() *ImageSigner {
	return &ImageSigner{}
}

func (s *ImageSigner) execCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.runCmd != nil {
		return s.runCmd(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Sign signs an OCI image using cosign (key-based or keyless OIDC) or returns
// the exact cosign invocation in dry-run mode.
func (s *ImageSigner) Sign(ctx context.Context, opts SignOptions) (*SignResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		return nil, fmt.Errorf("image reference must not be empty")
	}

	keyRef := strings.TrimSpace(opts.KeyRef)
	keyless := keyRef == ""

	args := []string{"sign"}
	if opts.Yes {
		args = append(args, "--yes")
	}
	if !keyless {
		args = append(args, "--key", keyRef)
	}
	for _, kv := range sortedAnnotationPairs(opts.Annotations) {
		args = append(args, "-a", kv)
	}
	args = append(args, image)

	fullCmd := append([]string{"cosign"}, args...)
	res := &SignResult{
		Image:       image,
		Keyless:     keyless,
		KeyRef:      keyRef,
		Annotations: copyStringMap(opts.Annotations),
		DryRun:      opts.DryRun,
		Command:     fullCmd,
	}

	if opts.DryRun {
		res.Output = "dry-run: " + strings.Join(fullCmd, " ")
		return res, nil
	}

	if _, err := s.execCmd(ctx, "cosign", "version"); err != nil {
		return nil, fmt.Errorf("cosign CLI not found in PATH; install cosign or use --dry-run: %w", err)
	}

	out, err := s.execCmd(ctx, "cosign", args...)
	res.Output = string(out)
	if err != nil {
		return res, fmt.Errorf("cosign sign failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	res.Signed = true
	return res, nil
}

// Verify verifies an OCI image signature using cosign or returns the exact
// cosign verification command in dry-run mode.
func (s *ImageSigner) Verify(ctx context.Context, opts VerifyOptions) (*VerifyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		return nil, fmt.Errorf("image reference must not be empty")
	}

	keyRef := strings.TrimSpace(opts.KeyRef)
	keyless := keyRef == ""

	args := []string{"verify"}
	if !keyless {
		args = append(args, "--key", keyRef)
	} else {
		if id := strings.TrimSpace(opts.CertificateIdentity); id != "" {
			args = append(args, "--certificate-identity", id)
		} else {
			args = append(args, "--certificate-identity-regexp", ".*")
		}
		if issuer := strings.TrimSpace(opts.CertificateIssuer); issuer != "" {
			args = append(args, "--certificate-oidc-issuer", issuer)
		} else {
			args = append(args, "--certificate-oidc-issuer-regexp", ".*")
		}
	}
	for _, kv := range sortedAnnotationPairs(opts.Annotations) {
		args = append(args, "-a", kv)
	}
	args = append(args, image)

	fullCmd := append([]string{"cosign"}, args...)
	res := &VerifyResult{
		Image:   image,
		Keyless: keyless,
		KeyRef:  keyRef,
		DryRun:  opts.DryRun,
		Command: fullCmd,
	}

	if opts.DryRun {
		res.Output = "dry-run: " + strings.Join(fullCmd, " ")
		return res, nil
	}

	if _, err := s.execCmd(ctx, "cosign", "version"); err != nil {
		return nil, fmt.Errorf("cosign CLI not found in PATH; install cosign or use --dry-run: %w", err)
	}

	out, err := s.execCmd(ctx, "cosign", args...)
	res.Output = string(out)
	if err != nil {
		return res, fmt.Errorf("cosign verify failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	res.Verified = true
	return res, nil
}

func sortedAnnotationPairs(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return out
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
