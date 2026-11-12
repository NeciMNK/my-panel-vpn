package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fork numbers its releases from v0.0.1, below upstream's old v2.3.5 floor,
// so a pinned fork tag must reach the download instead of being refused.
func TestInstallAcceptsForkReleaseTag(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr/local"), 0o755); err != nil {
		t.Fatal(err)
	}
	driver := `red= green= yellow= plain=
xui_repo="NeciMNK/my-panel-vpn"
xui_folder=$TEST_ROOT/usr/local/x-ui
arch() { echo amd64; }
curl() { echo "curl $*" >&2; return 22; }
` + scriptFunction(t, "install.sh", "install_x-ui") + "install_x-ui v0.0.1\n"
	cmd := exec.Command(bash, "-c", driver)
	cmd.Env = append(os.Environ(), "TEST_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install_x-ui succeeded with a failing download:\n%s", out)
	}
	if !strings.Contains(string(out), "releases/download/v0.0.1/x-ui-linux-amd64.tar.gz") {
		t.Fatalf("install_x-ui v0.0.1 never tried to download the release:\n%s", out)
	}
}
