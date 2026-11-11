package agents

// fork: host reboot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableRebootInConfig(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	fn := extractShellFunc(t, PatchmonInstallScript, "enable_reboot_in_config")
	cases := []struct{ name, in, want string }{
		{"flips an existing false", "integrations:\n    docker: true\n    reboot-enabled: false\nlog_level: info\n",
			"integrations:\n    docker: true\n    reboot-enabled: true\nlog_level: info\n"},
		{"adds it with the block's indentation", "integrations:\n  docker: false\n",
			"integrations:\n  reboot-enabled: true\n  docker: false\n"},
		{"adds the block when missing", "patchmon_server: x\n",
			"patchmon_server: x\nintegrations:\n  reboot-enabled: true\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(c.in), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(sh, "-c", fn+`enable_reboot_in_config "$1"`, "_", path).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, _ := os.ReadFile(path)
			if string(got) != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

func TestInstallScriptRebootFlag(t *testing.T) {
	script := string(PatchmonInstallScript)
	for _, want := range []string{
		`[ "$arg" = "--allow-reboot" ] && ALLOW_REBOOT="true"`,
		"  reboot-enabled: ${ALLOW_REBOOT}",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// The default must stay off.
	if !strings.Contains(script, "ALLOW_REBOOT=\"false\"") {
		t.Error("ALLOW_REBOOT must default to false")
	}
}
