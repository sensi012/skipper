package security

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestImageSigner_Sign(t *testing.T) {
	t.Run("dry-run keyless with sorted annotations works without cosign", func(t *testing.T) {
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				t.Fatal("runCmd should not be invoked in dry-run mode")
				return nil, nil
			},
		}

		res, err := signer.Sign(context.Background(), SignOptions{
			Image: "ghcr.io/acme/api:v1.0.0",
			Annotations: map[string]string{
				"env":    "prod",
				"commit": "abc123",
			},
			DryRun: true,
			Yes:    true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.DryRun || res.Signed || !res.Keyless {
			t.Fatalf("unexpected dry-run result: %+v", res)
		}
		wantCmd := []string{"cosign", "sign", "--yes", "-a", "commit=abc123", "-a", "env=prod", "ghcr.io/acme/api:v1.0.0"}
		if !reflect.DeepEqual(res.Command, wantCmd) {
			t.Fatalf("got command %v, want %v", res.Command, wantCmd)
		}
		if !strings.HasPrefix(res.Output, "dry-run: cosign sign") {
			t.Fatalf("unexpected dry-run output: %q", res.Output)
		}
	})

	t.Run("key-based signing succeeds when cosign available", func(t *testing.T) {
		var calls [][]string
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, args...))
				if len(args) > 0 && args[0] == "version" {
					return []byte("v2.2.4"), nil
				}
				return []byte("Pushing signature to ghcr.io/acme/api:v1.0.0"), nil
			},
		}

		res, err := signer.Sign(context.Background(), SignOptions{
			Image:  "ghcr.io/acme/api:v1.0.0",
			KeyRef: "cosign.key",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Signed || res.Keyless || res.KeyRef != "cosign.key" {
			t.Fatalf("unexpected sign result: %+v", res)
		}
		wantCmd := []string{"cosign", "sign", "--key", "cosign.key", "ghcr.io/acme/api:v1.0.0"}
		if !reflect.DeepEqual(res.Command, wantCmd) {
			t.Fatalf("got command %v, want %v", res.Command, wantCmd)
		}
		if len(calls) != 2 {
			t.Fatalf("expected 2 cosign calls (version + sign), got %d", len(calls))
		}
	})

	t.Run("cosign missing returns actionable error", func(t *testing.T) {
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return nil, errors.New("executable file not found")
			},
		}
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "app:v1"}); err == nil || !strings.Contains(err.Error(), "cosign CLI not found") {
			t.Fatalf("expected cosign missing error, got %v", err)
		}
	})

	t.Run("cosign sign failure returns wrapped error", func(t *testing.T) {
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "version" {
					return []byte("v2.2.4"), nil
				}
				return []byte("unauthorized"), errors.New("exit status 1")
			},
		}
		res, err := signer.Sign(context.Background(), SignOptions{Image: "app:v1", KeyRef: "cosign.key"})
		if err == nil || !strings.Contains(err.Error(), "cosign sign failed") {
			t.Fatalf("expected cosign sign failure, got %v", err)
		}
		if res == nil || res.Signed {
			t.Fatalf("expected Signed=false on failure, got %+v", res)
		}
	})

	t.Run("validation and canceled context", func(t *testing.T) {
		signer := NewImageSigner()
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "   "}); err == nil {
			t.Fatal("expected error for empty image")
		}
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "--key=evil.key"}); err == nil {
			t.Fatal("expected error for flag-injection image starting with '-'")
		}
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "app:v1", KeyRef: "--force"}); err == nil {
			t.Fatal("expected error for flag-injection KeyRef starting with '-'")
		}
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "app:v1", Annotations: map[string]string{"bad=key": "v"}}); err == nil {
			t.Fatal("expected error for annotation key containing '='")
		}
		if _, err := signer.Sign(context.Background(), SignOptions{Image: "app:v1", Annotations: map[string]string{"k": "v\n--flag"}}); err == nil {
			t.Fatal("expected error for annotation value containing newline")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := signer.Sign(ctx, SignOptions{Image: "app:v1"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestImageSigner_Verify(t *testing.T) {
	t.Run("dry-run keyless with custom identity and issuer", func(t *testing.T) {
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				t.Fatal("runCmd should not run in dry-run")
				return nil, nil
			},
		}

		res, err := signer.Verify(context.Background(), VerifyOptions{
			Image:               "ghcr.io/acme/api:v1.0.0",
			CertificateIdentity: "ci@acme.dev",
			CertificateIssuer:   "https://token.actions.githubusercontent.com",
			Annotations:         map[string]string{"env": "prod"},
			DryRun:              true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.DryRun || res.Verified || !res.Keyless {
			t.Fatalf("unexpected verify dry-run result: %+v", res)
		}
		wantCmd := []string{
			"cosign", "verify",
			"--certificate-identity", "ci@acme.dev",
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
			"-a", "env=prod",
			"ghcr.io/acme/api:v1.0.0",
		}
		if !reflect.DeepEqual(res.Command, wantCmd) {
			t.Fatalf("got command %v, want %v", res.Command, wantCmd)
		}
	})

	t.Run("keyless default regexes and key-based verify succeed", func(t *testing.T) {
		signer := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "version" {
					return []byte("v2.2.4"), nil
				}
				return []byte("Verification for app:v1 -- The following checks were performed"), nil
			},
		}

		// Keyless default
		res, err := signer.Verify(context.Background(), VerifyOptions{Image: "app:v1"})
		if err != nil || !res.Verified || !res.Keyless {
			t.Fatalf("expected verified keyless result, got res=%+v err=%v", res, err)
		}
		if !strings.Contains(strings.Join(res.Command, " "), "--certificate-identity-regexp .*") {
			t.Fatalf("expected default identity regexp, got %v", res.Command)
		}

		// Key-based
		res2, err := signer.Verify(context.Background(), VerifyOptions{Image: "app:v1", KeyRef: "cosign.pub"})
		if err != nil || !res2.Verified || res2.Keyless {
			t.Fatalf("expected verified key-based result, got res=%+v err=%v", res2, err)
		}
	})

	t.Run("cosign missing and verify failure", func(t *testing.T) {
		missing := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return nil, errors.New("not found")
			},
		}
		if _, err := missing.Verify(context.Background(), VerifyOptions{Image: "app:v1"}); err == nil {
			t.Fatal("expected error when cosign missing")
		}

		failing := &ImageSigner{
			runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "version" {
					return []byte("v2.2.4"), nil
				}
				return []byte("no matching signatures"), errors.New("exit status 1")
			},
		}
		res, err := failing.Verify(context.Background(), VerifyOptions{Image: "app:v1", KeyRef: "cosign.pub"})
		if err == nil || !strings.Contains(err.Error(), "cosign verify failed") || res.Verified {
			t.Fatalf("expected verify failure, got res=%+v err=%v", res, err)
		}
	})

	t.Run("validation and canceled context", func(t *testing.T) {
		signer := NewImageSigner()
		if _, err := signer.Verify(context.Background(), VerifyOptions{}); err == nil {
			t.Fatal("expected error for empty image")
		}
		if _, err := signer.Verify(context.Background(), VerifyOptions{Image: "--insecure-ignore-tlog"}); err == nil {
			t.Fatal("expected error for flag-injection image")
		}
		if _, err := signer.Verify(context.Background(), VerifyOptions{Image: "app:v1", CertificateIdentity: "--bad"}); err == nil {
			t.Fatal("expected error for flag-injection CertificateIdentity")
		}
		if _, err := signer.Verify(context.Background(), VerifyOptions{Image: "app:v1", CertificateIssuer: "--bad"}); err == nil {
			t.Fatal("expected error for flag-injection CertificateIssuer")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := signer.Verify(ctx, VerifyOptions{Image: "app:v1"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}
