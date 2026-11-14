package commands

// fork: SCAP content from the server is verified like agent updates. The
// datastreams carry the remediation scripts oscap runs as root, so a server
// (or anyone posing as one) must not be able to hand out its own.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"patchmon-agent/internal/client"
	"patchmon-agent/internal/integrations/compliance"
	"patchmon-agent/internal/pkgversion"
)

func init() {
	compliance.RemediationAllowed = func() bool {
		return cfgManager != nil && cfgManager.IsIntegrationEnabled("compliance-remediation-enabled")
	}
}

// ssgTrustedComment is what the signature of a datastream must carry.
func ssgTrustedComment(version, filename string) string {
	return "PatchMon SSG " + version + " " + filename
}

// ssgFetcher is the part of the client that downloadVerifiedSSG needs.
type ssgFetcher interface {
	DownloadSSGContent(ctx context.Context, filename, destPath string) error
	DownloadSSGSignature(ctx context.Context, filename string) ([]byte, error)
}

var _ ssgFetcher = (*client.Client)(nil)

// downloadVerifiedSSG downloads a datastream next to destPath under a name the
// scanner ignores, checks its signature against the embedded key and only then
// moves it into place. Anything that does not verify is deleted.
func downloadVerifiedSSG(ctx context.Context, c ssgFetcher, version, filename, destPath string) error {
	if pkgversion.SigningPublicKey == "" {
		return fmt.Errorf("refusing SSG content: this agent was built without a signing public key")
	}
	if version == "" {
		return fmt.Errorf("refusing SSG content: unknown server SSG version")
	}
	tmp := filepath.Join(filepath.Dir(destPath), "."+filepath.Base(destPath)+".download")
	defer func() { _ = os.Remove(tmp) }()

	if err := c.DownloadSSGContent(ctx, filename, tmp); err != nil {
		return err
	}
	sig, err := c.DownloadSSGSignature(ctx, filename)
	if err != nil {
		return fmt.Errorf("refusing SSG content %s: %w", filename, err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return fmt.Errorf("read downloaded SSG content: %w", err)
	}
	comment, err := verifyMinisign(pkgversion.SigningPublicKey, data, sig)
	if err != nil {
		return fmt.Errorf("refusing SSG content %s: %w", filename, err)
	}
	if want := ssgTrustedComment(version, filename); comment != want {
		return fmt.Errorf("refusing SSG content %s: signature is for %q, expected %q", filename, comment, want)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		return fmt.Errorf("install SSG content: %w", err)
	}
	logger.WithField("file", filename).Info("SSG content signature verified")
	return nil
}
