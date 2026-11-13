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

// All fork tags before 1.0 are GitHub pre-releases, so releases/latest is empty
// and install/update fell through to dev-latest (#6); they must pick vX.Y.Z.
func TestResolveLatestTagPicksNewestPreRelease(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	const atom = `<link rel="alternate" href="https://github.com/NeciMNK/my-panel-vpn/releases/tag/dev-latest"/>
<link rel="alternate" href="https://github.com/NeciMNK/my-panel-vpn/releases/tag/v0.0.10"/>
<link rel="alternate" href="https://github.com/NeciMNK/my-panel-vpn/releases/tag/v0.0.9"/>
<link rel="alternate" href="https://github.com/NeciMNK/my-panel-vpn/releases/tag/0.0.1"/>`
	const api = `[
  {"tag_name": "dev-latest"},
  {"tag_name": "v0.0.3"},
  {"tag_name": "v0.0.2"}
]`
	for _, script := range []string{"install.sh", "update.sh"} {
		for _, tc := range []struct{ name, feed, want string }{
			{"atom feed", atom, "v0.0.10"},
			{"api fallback", "", "v0.0.3"},
		} {
			t.Run(script+"/"+tc.name, func(t *testing.T) {
				driver := `xui_repo="NeciMNK/my-panel-vpn"
curl_bin=curl
curl() { case "$*" in *releases.atom*) [[ -n "$FEED" ]] && printf '%s\n' "$FEED" || return 22 ;; *api.github.com*releases*) printf '%s\n' "$API" ;; *) return 22 ;; esac; }
` + scriptFunction(t, script, "resolve_latest_tag") + "resolve_latest_tag\n"
				cmd := exec.Command(bash, "-c", driver)
				cmd.Env = append(os.Environ(), "FEED="+tc.feed, "API="+api)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("resolve_latest_tag failed: %v\n%s", err, out)
				}
				if got := strings.TrimSpace(string(out)); got != tc.want {
					t.Fatalf("resolve_latest_tag = %q, want %q", got, tc.want)
				}
			})
		}
	}
}
