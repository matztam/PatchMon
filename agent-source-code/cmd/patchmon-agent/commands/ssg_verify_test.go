package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"patchmon-agent/internal/integrations/compliance"
	"patchmon-agent/internal/pkgversion"
	"patchmon-agent/pkg/models"
)

type fakeSSGServer struct {
	content []byte
	sig     []byte
	sigErr  error
}

func (f *fakeSSGServer) DownloadSSGContent(_ context.Context, _, destPath string) error {
	return os.WriteFile(destPath, f.content, 0o644)
}

func (f *fakeSSGServer) DownloadSSGSignature(context.Context, string) ([]byte, error) {
	return f.sig, f.sigErr
}

func withSigningKey(t *testing.T, key string) {
	t.Helper()
	prev := pkgversion.SigningPublicKey
	pkgversion.SigningPublicKey = key
	t.Cleanup(func() { pkgversion.SigningPublicKey = prev })
}

func TestDownloadVerifiedSSG(t *testing.T) {
	pub, priv, keyID := generateTestKeypair(t)
	_, otherPriv, otherKeyID := generateTestKeypair(t)
	content := []byte("<ds:data-stream-collection/>")
	const file, version = "ssg-debian12-ds.xml", "0.1.80"
	good := signData(t, priv, keyID, content, ssgTrustedComment(version, file))

	cases := []struct {
		name    string
		key     string
		server  *fakeSSGServer
		version string
		wantOK  bool
	}{
		{"valid signature is installed", pub, &fakeSSGServer{content: content, sig: good}, version, true},
		{"tampered content is refused", pub, &fakeSSGServer{content: []byte("<evil/>"), sig: good}, version, false},
		{"other key is refused", pub, &fakeSSGServer{content: content, sig: signData(t, otherPriv, otherKeyID, content, ssgTrustedComment(version, file))}, version, false},
		{"signature for another file is refused", pub, &fakeSSGServer{content: content, sig: signData(t, priv, keyID, content, ssgTrustedComment(version, "ssg-rhel9-ds.xml"))}, version, false},
		{"signature for another version is refused", pub, &fakeSSGServer{content: content, sig: signData(t, priv, keyID, content, ssgTrustedComment("0.1.70", file))}, version, false},
		{"agent signature is refused", pub, &fakeSSGServer{content: content, sig: signData(t, priv, keyID, content, "PatchMon Agent 9.9.9")}, version, false},
		{"missing signature is refused", pub, &fakeSSGServer{content: content, sigErr: errors.New("404")}, version, false},
		{"agent without key refuses", "", &fakeSSGServer{content: content, sig: good}, version, false},
		{"unknown version refuses", pub, &fakeSSGServer{content: content, sig: good}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withSigningKey(t, c.key)
			dir := t.TempDir()
			dest := filepath.Join(dir, file)
			err := downloadVerifiedSSG(context.Background(), c.server, c.version, file, dest)
			_, statErr := os.Stat(dest)
			if c.wantOK {
				if err != nil || statErr != nil {
					t.Fatalf("err=%v stat=%v", err, statErr)
				}
			} else if err == nil || statErr == nil {
				t.Fatalf("expected refusal and no installed file, err=%v stat=%v", err, statErr)
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.Name() != file {
					t.Errorf("left behind %s", e.Name())
				}
			}
		})
	}
}

func TestRemediationGateFollowsLocalConfig(t *testing.T) {
	m := withTestConfig(t, "false")
	if compliance.RemediationAllowed() {
		t.Fatal("remediation allowed by default")
	}
	if err := remediateSingleRule("xccdf_rule_x", "level1_server"); !errors.Is(err, compliance.ErrRemediationNotEnabled) {
		t.Fatalf("remediateSingleRule without opt-in: %v", err)
	}
	if err := compliance.NewOpenSCAPScanner(logger).RunOfflineRemediation(context.Background(), "/nonexistent"); !errors.Is(err, compliance.ErrRemediationNotEnabled) {
		t.Fatalf("RunOfflineRemediation without opt-in: %v", err)
	}
	if err := m.SetIntegrationEnabled("compliance-remediation-enabled", true); err != nil {
		t.Fatal(err)
	}
	if !compliance.RemediationAllowed() {
		t.Fatal("opt-in not honoured")
	}
}

func TestSyncIntegrationStatusIgnoresRemediationSwitch(t *testing.T) {
	m := withTestConfig(t, "false")
	syncIntegrationStatus(&models.IntegrationStatusResponse{
		Success:      true,
		Integrations: map[string]bool{"compliance-remediation-enabled": true},
	})
	if m.IsIntegrationEnabled("compliance-remediation-enabled") {
		t.Fatal("server enabled compliance remediation")
	}
}
