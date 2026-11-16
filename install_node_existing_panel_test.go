package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const installNodeScript = "internal/nodejoin/install-node.sh"

// runNode drives install-node.sh's existing-panel functions against the fake
// root of a foreignPanelHost, with the script's own shell options.
func (h *foreignPanelHost) runNode(t *testing.T, call string, env ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	driver := `set -euo pipefail
red= green= yellow= plain=
release=ubuntu
xui_repo="NeciMNK/my-panel-vpn"
root=$TEST_ROOT
xui_folder=$root/usr/local/x-ui
xui_service=$root/etc/systemd/system
xui_cli=$root/usr/bin/x-ui
xui_rc_script=$root/etc/init.d/x-ui
xui_etc=$root/etc/x-ui
xui_backup_root=$root/root
REPLACE=${TEST_REPLACE:-0}
replace_existing_xui=0
die() { echo "error: $*" >&2; exit 1; }
info() { echo "==> $*"; }
systemctl() { printf 'systemctl %s\n' "$*" >> "$root/calls"; }
pkill() { printf 'pkill %s\n' "$*" >> "$root/calls"; }
xui_env_file_path() { echo "$root/etc/default/x-ui"; }
`
	for _, fn := range []string{"detect_xui_install", "check_existing_panel", "remove_foreign_xui"} {
		driver += scriptFunction(t, installNodeScript, fn)
	}
	driver += call + "\n"
	cmd := exec.Command(bash, "-c", driver)
	cmd.Env = append(os.Environ(), append([]string{"TEST_ROOT=" + h.dir}, env...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// node lays out an earlier node install of this fork.
func (h *foreignPanelHost) node(t *testing.T) {
	t.Helper()
	h.upstream(t)
	h.write(t, "usr/local/x-ui/x-ui", "\x7fELF\x00panelRepo NeciMNK/my-panel-vpn\x00")
	h.write(t, "etc/systemd/system/x-ui.service", "[Unit]\nDescription=x-ui node of https://old-master:2053/p\n")
}

// forkPanel lays out a full (master) panel of this fork.
func (h *foreignPanelHost) forkPanel(t *testing.T) {
	t.Helper()
	h.upstream(t)
	h.write(t, "usr/local/x-ui/x-ui", "\x7fELF\x00panelRepo NeciMNK/my-panel-vpn\x00")
}

const prepareNodeHost = `check_existing_panel; if [[ $replace_existing_xui == 1 ]]; then remove_foreign_xui || die "backup failed"; fi`

// The issue: install-node.sh started the node on whatever /etc/x-ui/x-ui.db an
// earlier 3x-ui left, inheriting its admin logins and API tokens.
func TestInstallNodeBacksUpAndRemovesAnExistingPanel(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*foreignPanelHost, *testing.T)
		env   []string
	}{
		{"upstream 3x-ui", (*foreignPanelHost).upstream, nil},
		{"earlier node of this fork", (*foreignPanelHost).node, nil},
		{"fork panel with --replace", (*foreignPanelHost).forkPanel, []string{"TEST_REPLACE=1"}},
		{"database left without a panel", func(h *foreignPanelHost, t *testing.T) {
			h.write(t, "etc/x-ui/x-ui.db", "SQLite format 3\x00clients")
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newForeignPanelHost(t)
			tc.setup(h, t)
			out, err := h.runNode(t, prepareNodeHost, tc.env...)
			if err != nil {
				t.Fatalf("prepare: %v\n%s", err, out)
			}
			backups, _ := filepath.Glob(h.path("root/3x-ui-backup-*/x-ui/x-ui.db"))
			if len(backups) != 1 || readScriptState(t, filepath.Dir(backups[0]), "x-ui.db") != "SQLite format 3\x00clients" {
				t.Fatalf("old x-ui.db not backed up: %v\n%s", backups, out)
			}
			for _, rel := range []string{"etc/x-ui", "usr/local/x-ui", "etc/systemd/system/x-ui.service.d", "etc/default/x-ui"} {
				if h.exists(rel) {
					t.Errorf("%s survived, the node would start on it", rel)
				}
			}
		})
	}
}

func TestInstallNodeRefusesToReplaceAForkPanelWithoutFlag(t *testing.T) {
	h := newForeignPanelHost(t)
	h.forkPanel(t)
	out, err := h.runNode(t, prepareNodeHost)
	if err == nil || !strings.Contains(out, "--replace") {
		t.Fatalf("a full panel of this fork must not be replaced without --replace, err=%v:\n%s", err, out)
	}
	if !h.exists("etc/x-ui/x-ui.db") || !h.exists("usr/local/x-ui/x-ui") || h.exists("calls") {
		t.Fatalf("refusal touched the panel:\n%s", out)
	}
}

func TestInstallNodeLeavesACleanHostAlone(t *testing.T) {
	h := newForeignPanelHost(t)
	h.blitz(t)
	out, err := h.runNode(t, prepareNodeHost)
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, out)
	}
	if backups, _ := filepath.Glob(h.path("root/3x-ui-backup-*")); len(backups) != 0 || h.exists("calls") {
		t.Fatalf("clean host got a backup %v or service calls:\n%s", backups, out)
	}
}

// The node installer is served by the master and cannot source install.sh, so
// it carries copies; a fix to one must reach the other.
func TestInstallNodeSharesPanelReplacementWithInstaller(t *testing.T) {
	for _, fn := range []string{"xui_env_file_path", "detect_xui_install", "remove_foreign_xui"} {
		if node, main := scriptFunction(t, installNodeScript, fn), scriptFunction(t, "install.sh", fn); node != main {
			t.Errorf("%s differs between %s and install.sh", fn, installNodeScript)
		}
	}
}
