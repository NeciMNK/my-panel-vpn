package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// foreignPanelHost is a fake root: every path install.sh's replacement logic
// touches is redirected under dir so the real script functions run unmodified.
type foreignPanelHost struct {
	dir string
}

func newForeignPanelHost(t *testing.T) *foreignPanelHost {
	t.Helper()
	h := &foreignPanelHost{dir: t.TempDir()}
	for _, d := range []string{"usr/local/x-ui/bin", "usr/bin", "etc/systemd/system", "etc/default", "etc/init.d", "root"} {
		if err := os.MkdirAll(h.path(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *foreignPanelHost) path(rel string) string { return filepath.Join(h.dir, rel) }

func (h *foreignPanelHost) write(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(h.path(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.path(rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *foreignPanelHost) exists(rel string) bool {
	_, err := os.Lstat(h.path(rel))
	return err == nil
}

// upstream lays out a stock 3x-ui install: the same paths this fork uses.
func (h *foreignPanelHost) upstream(t *testing.T) {
	t.Helper()
	h.write(t, "usr/local/x-ui/x-ui", "\x7fELF github.com/mhsanaei/3x-ui")
	h.write(t, "usr/local/x-ui/bin/xray-linux-amd64", "xray")
	h.write(t, "usr/bin/x-ui", "#!/bin/bash\nbash <(curl -Ls https://raw.githubusercontent.com/mhsanaei/3x-ui/main/install.sh)\n")
	h.write(t, "etc/systemd/system/x-ui.service", "[Service]\nExecStart=/usr/local/x-ui/x-ui\n")
	h.write(t, "etc/systemd/system/x-ui.service.d/10-xui-sandbox.conf", "[Service]\n")
	h.write(t, "etc/x-ui/x-ui.db", "SQLite format 3\x00clients")
	h.write(t, "etc/default/x-ui", "XUI_LOG_LEVEL=info\n")
}

// blitz lays out a Blitz (Hysteria2) panel, which must survive untouched.
func (h *foreignPanelHost) blitz(t *testing.T) {
	t.Helper()
	h.write(t, "etc/hysteria/menu.sh", "#!/bin/bash\n")
	h.write(t, "etc/hysteria/config.json", `{"listen":":443"}`)
	h.write(t, "etc/systemd/system/hysteria-server.service", "[Service]\n")
}

func (h *foreignPanelHost) run(t *testing.T, call string, env ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	driver := `set -u
red= green= yellow= blue= plain=
release=ubuntu
xui_repo="NeciMNK/my-panel-vpn"
root=$TEST_ROOT
xui_folder=$root/usr/local/x-ui
xui_service=$root/etc/systemd/system
xui_cli=$root/usr/bin/x-ui
xui_rc_script=$root/etc/init.d/x-ui
xui_etc=$root/etc/x-ui
xui_backup_root=$root/root
blitz_dir=$root/etc/hysteria
replace_foreign_xui=0
systemctl() { printf 'systemctl %s\n' "$*" >> "$root/calls"; }
rc-service() { printf 'rc-service %s\n' "$*" >> "$root/calls"; }
rc-update() { printf 'rc-update %s\n' "$*" >> "$root/calls"; }
pkill() { printf 'pkill %s\n' "$*" >> "$root/calls"; }
is_port_in_use() { [[ "$1" == "${TEST_BUSY_PORT:-}" ]]; }
xui_env_file_path() { echo "$root/etc/default/x-ui"; }
`
	for _, fn := range []string{"detect_xui_install", "detect_blitz", "check_existing_panels", "remove_foreign_xui"} {
		driver += scriptFunction(t, "install.sh", fn)
	}
	driver += call + "\n"
	cmd := exec.Command(bash, "-c", driver)
	cmd.Env = append(os.Environ(), append([]string{"TEST_ROOT=" + h.dir, "NONINTERACTIVE=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallDetectsExistingPanel(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*foreignPanelHost, *testing.T)
		want  string
	}{
		{"clean host", func(*foreignPanelHost, *testing.T) {}, "none"},
		{"blitz only is not an x-ui install", (*foreignPanelHost).blitz, "none"},
		{"upstream 3x-ui", (*foreignPanelHost).upstream, "foreign"},
		{"leftover upstream unit without binary", func(h *foreignPanelHost, t *testing.T) {
			h.write(t, "etc/systemd/system/x-ui.service", "[Service]\n")
		}, "foreign"},
		{"this fork", func(h *foreignPanelHost, t *testing.T) {
			h.upstream(t)
			h.write(t, "usr/local/x-ui/x-ui", "\x7fELF\x00panelRepo NeciMNK/my-panel-vpn\x00")
		}, "fork"},
		{"fork binary with an upstream cli script", func(h *foreignPanelHost, t *testing.T) {
			h.upstream(t)
			h.write(t, "usr/bin/x-ui", "#!/bin/bash\nxui_repo=\"NeciMNK/my-panel-vpn\"\n")
		}, "fork"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newForeignPanelHost(t)
			tc.setup(h, t)
			out, err := h.run(t, "detect_xui_install")
			if err != nil {
				t.Fatalf("detect_xui_install: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("detect_xui_install = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInstallCheckExistingPanelsMarksOnlyForeignForReplacement(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*foreignPanelHost, *testing.T)
		replace string
	}{
		{"upstream 3x-ui is replaced", (*foreignPanelHost).upstream, "1"},
		{"blitz alone is left alone", (*foreignPanelHost).blitz, "0"},
		{"fork is updated in place", func(h *foreignPanelHost, t *testing.T) {
			h.write(t, "usr/local/x-ui/x-ui", "NeciMNK/my-panel-vpn")
		}, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newForeignPanelHost(t)
			tc.setup(h, t)
			out, err := h.run(t, `check_existing_panels; echo "replace=${replace_foreign_xui}"`)
			if err != nil {
				t.Fatalf("check_existing_panels: %v\n%s", err, out)
			}
			if !strings.Contains(out, "replace="+tc.replace+"\n") {
				t.Fatalf("want replace=%s, got:\n%s", tc.replace, out)
			}
			if h.exists("calls") {
				t.Fatalf("detection must not stop or kill anything, calls:\n%s", readScriptState(t, h.dir, "calls"))
			}
		})
	}
}

func TestInstallCheckExistingPanelsAbortsWhenReplacementDeclined(t *testing.T) {
	h := newForeignPanelHost(t)
	h.upstream(t)
	out, err := h.run(t, `check_existing_panels <<< n; echo "replace=${replace_foreign_xui}"`, "NONINTERACTIVE=0")
	if err == nil || strings.Contains(out, "replace=") {
		t.Fatalf("declining must abort the install, got err=%v:\n%s", err, out)
	}
	if !h.exists("usr/local/x-ui/x-ui") || !h.exists("etc/x-ui/x-ui.db") {
		t.Fatal("declining removed the existing panel")
	}
}

func TestInstallBlitzNoticeNamesBusyPort80(t *testing.T) {
	h := newForeignPanelHost(t)
	h.blitz(t)
	out, err := h.run(t, "check_existing_panels", "TEST_BUSY_PORT=80")
	if err != nil {
		t.Fatalf("check_existing_panels: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Blitz") || !strings.Contains(out, "port 80 is already in use") {
		t.Fatalf("Blitz notice missing the port-80 warning:\n%s", out)
	}
}

func TestInstallRemoveForeignXUIBacksUpThenRemoves(t *testing.T) {
	h := newForeignPanelHost(t)
	h.upstream(t)
	h.blitz(t)
	out, err := h.run(t, "remove_foreign_xui")
	if err != nil {
		t.Fatalf("remove_foreign_xui: %v\n%s", err, out)
	}

	backups, _ := filepath.Glob(h.path("root/3x-ui-backup-*"))
	if len(backups) != 1 {
		t.Fatalf("want one backup dir, got %v\n%s", backups, out)
	}
	backup := backups[0]
	for rel, want := range map[string]string{
		"x-ui/x-ui.db":                       "SQLite format 3\x00clients",
		"x-ui.env":                           "XUI_LOG_LEVEL=info\n",
		"x-ui.service":                       "[Service]\nExecStart=/usr/local/x-ui/x-ui\n",
		"x-ui.service.d/10-xui-sandbox.conf": "[Service]\n",
	} {
		got, err := os.ReadFile(filepath.Join(backup, rel))
		if err != nil || string(got) != want {
			t.Errorf("backup %s = %q (%v), want %q", rel, got, err, want)
		}
	}
	if !strings.Contains(out, filepath.Join(backup, "x-ui", "x-ui.db")) {
		t.Errorf("output does not point at the backed-up x-ui.db for the import:\n%s", out)
	}

	for _, rel := range []string{
		"usr/local/x-ui", "usr/bin/x-ui", "etc/systemd/system/x-ui.service",
		"etc/systemd/system/x-ui.service.d", "etc/x-ui", "etc/default/x-ui",
	} {
		if h.exists(rel) {
			t.Errorf("%s survived the removal", rel)
		}
	}
	for _, rel := range []string{"etc/hysteria/menu.sh", "etc/hysteria/config.json", "etc/systemd/system/hysteria-server.service"} {
		if !h.exists(rel) {
			t.Errorf("Blitz file %s was removed", rel)
		}
	}

	calls := readScriptState(t, h.dir, "calls")
	if !strings.HasPrefix(calls, "systemctl stop x-ui\n") {
		t.Errorf("old panel must be stopped before it is copied, calls:\n%s", calls)
	}
	if strings.Contains(calls, "hysteria") {
		t.Errorf("Blitz services were touched:\n%s", calls)
	}
}

func TestInstallRemoveForeignXUIKeepsPanelWhenBackupFails(t *testing.T) {
	h := newForeignPanelHost(t)
	h.upstream(t)
	out, err := h.run(t, `cp() { return 1; }; remove_foreign_xui`)
	if err == nil {
		t.Fatalf("a failed backup must fail the replacement:\n%s", out)
	}
	for _, rel := range []string{"usr/local/x-ui/x-ui", "usr/bin/x-ui", "etc/x-ui/x-ui.db", "etc/systemd/system/x-ui.service"} {
		if !h.exists(rel) {
			t.Errorf("%s removed although the backup failed", rel)
		}
	}
	if calls := readScriptState(t, h.dir, "calls"); !strings.Contains(calls, "systemctl start x-ui") {
		t.Errorf("old panel left stopped after a failed backup, calls:\n%s", calls)
	}
}

func TestInstallRemoveForeignXUIOnlyBacksUpCustomDBFolder(t *testing.T) {
	h := newForeignPanelHost(t)
	h.upstream(t)
	h.write(t, "data/xui/x-ui.db", "custom db")
	h.write(t, "etc/default/x-ui", "XUI_DB_FOLDER=\""+h.path("data/xui")+"\"\n")
	out, err := h.run(t, "remove_foreign_xui")
	if err != nil {
		t.Fatalf("remove_foreign_xui: %v\n%s", err, out)
	}
	backups, _ := filepath.Glob(h.path("root/3x-ui-backup-*/x-ui/x-ui.db"))
	if len(backups) != 1 || readScriptState(t, filepath.Dir(backups[0]), "x-ui.db") != "custom db" {
		t.Fatalf("custom XUI_DB_FOLDER not backed up: %v\n%s", backups, out)
	}
	if !h.exists("data/xui/x-ui.db") {
		t.Fatal("a folder named only by the old env file must not be deleted")
	}
}
