package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/backup/sshtest"
	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// fakeInstaller stands in for install-node.sh on the "new server": it keeps
// its arguments and the backup it was handed, then reports an identity.
const fakeInstaller = `#!/bin/bash
set -eu
out=$FAKE_INSTALL_OUT
printf '%s\n' "$@" > "$out/args"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --restore) cp "$2" "$out/restored.tar.gz"; shift 2 ;;
    --bundle) tar -tzf "$2" > "$out/bundle.list"; shift 2 ;;
    *) shift ;;
  esac
done
echo "==> Installing"
echo "node-identity: port=24567 basePath=/restored/ apiToken=new-token certSha256=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
`

func waitRestore(t *testing.T, svc *BackupService) NodeRestoreStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if st := svc.RestoreStatus(); !st.Running {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("restore did not finish")
	return NodeRestoreStatus{}
}

// The lost-node case: one call with a new server's login must bring the latest
// backup there and repoint the node's record so the master syncs it again.
func TestRestoreNodeMovesNodeToNewServer(t *testing.T) {
	svc, dir := setupBackupTarget(t)
	out := t.TempDir()
	t.Setenv("FAKE_INSTALL_OUT", out)
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	old := nodeInstaller
	nodeInstaller = func() []byte { return []byte(fakeInstaller) }
	t.Cleanup(func() { nodeInstaller = old })

	node := &model.Node{Name: "ru-1", Address: "192.0.2.10", Port: 2053, BasePath: "/old/", ApiToken: "old-token", Enable: true, TlsVerifyMode: "pin", PinnedCertSha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := (&NodeService{}).Create(node); err != nil {
		t.Fatal(err)
	}
	// Disabled first: that is how a dead node gets repointed before its restore.
	if err := database.GetDB().Model(node).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "db")
	_ = os.WriteFile(src, []byte("node db"), 0o600)
	var archive bytes.Buffer
	if err := backup.WriteNodeArchive(&archive, []backup.ArchiveFile{{Src: src, Path: "/etc/x-ui/x-ui.db"}}); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, NodeBackupFolder(node.Id))
	_ = os.MkdirAll(folder, 0o700)
	_ = os.WriteFile(filepath.Join(folder, "ru-1_2026-01-01_000000.tar.gz"), []byte("older"), 0o600)
	_ = os.Chtimes(filepath.Join(folder, "ru-1_2026-01-01_000000.tar.gz"), time.Unix(1_700_000_000, 0), time.Unix(1_700_000_000, 0))
	_ = os.WriteFile(filepath.Join(folder, "ru-1_2026-02-01_000000.tar.gz"), archive.Bytes(), 0o600)

	target := sshtest.Start(t, "new-root-pw")
	done := make(chan struct{})
	err := svc.RestoreNode(node.Id, NodeRestoreRequest{Host: target.Host, Port: target.Port, User: "root", Password: "new-root-pw"}, func() { close(done) })
	if err != nil {
		t.Fatal(err)
	}
	st := waitRestore(t, svc)
	if st.Error != "" {
		t.Fatalf("restore failed: %s\n%s", st.Error, strings.Join(st.Log, "\n"))
	}
	<-done

	restored, err := os.ReadFile(filepath.Join(out, "restored.tar.gz"))
	if err != nil || !bytes.Equal(restored, archive.Bytes()) {
		t.Fatalf("the newest backup did not reach the server (err %v)", err)
	}
	args, _ := os.ReadFile(filepath.Join(out, "args"))
	for _, want := range []string{"--identity", "--replace", "--port\n2053"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("installer args %q lack %q", args, want)
		}
	}
	if list, _ := os.ReadFile(filepath.Join(out, "bundle.list")); !strings.Contains(string(list), "x-ui\n") {
		t.Fatalf("bundle has no panel binary: %q", list)
	}
	var got model.Node
	if err := database.GetDB().First(&got, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := nodetoken.Decrypt(got.Id, got.ApiToken)
	if got.Address != target.Host || got.Port != 24567 || got.BasePath != "/restored/" || token != "new-token" ||
		got.PinnedCertSha256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" || got.TlsVerifyMode != "pin" || !got.ConfigDirty {
		t.Fatalf("node after restore = %+v (token %q)", got, token)
	}
	if !got.AllowPrivateAddress {
		t.Fatal("a loopback address must be allowed explicitly")
	}
	if !got.Enable {
		t.Fatal("the restored node stayed disabled")
	}
}

// A node kept by domain keeps its name; the restore must move the name's A
// record to the new server through Cloudflare instead of rewriting the address.
func TestRestoredNodeAddressRepointsDomainThroughCloudflare(t *testing.T) {
	setupConflictDB(t)
	var patched string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
		switch {
		case r.URL.Path == "/zones" && r.URL.Query().Get("name") == "example.com":
			reply([]map[string]string{{"id": "z1"}})
		case r.URL.Path == "/zones":
			reply([]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
			reply([]map[string]any{{"id": "r1", "type": "A", "name": "ru-node-1.example.com", "content": "192.0.2.1"}})
		case r.Method == http.MethodPatch && r.URL.Path == "/zones/z1/dns_records/r1":
			var body struct{ Content string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			patched = body.Content
			reply(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cf.Close)
	cloudflareAPI = cf.URL
	t.Cleanup(func() { cloudflareAPI = "" })
	if err := (&SettingService{}).SetTlsCloudflareToken("tok"); err != nil {
		t.Fatal(err)
	}

	addr, err := restoredNodeAddress(context.Background(), "ru-node-1.example.com", "203.0.113.20")
	if err != nil || addr != "ru-node-1.example.com" {
		t.Fatalf("address = %q, %v; want the domain kept", addr, err)
	}
	if patched != "203.0.113.20" {
		t.Fatalf("Cloudflare record content = %q, want the new IP", patched)
	}
	if addr, _ := restoredNodeAddress(context.Background(), "192.0.2.1", "203.0.113.20"); addr != "203.0.113.20" {
		t.Fatalf("IP node address = %q, want the new IP", addr)
	}
}
