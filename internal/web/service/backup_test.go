package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/backup/sshtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func setupBackupTarget(t *testing.T) (svc *BackupService, dir string) {
	t.Helper()
	setupConflictDB(t)
	srv := sshtest.Start(t, "backup-pw")
	dir = filepath.Join(t.TempDir(), "backups")
	svc = &BackupService{}
	if err := svc.settingService.setString("webDomain", "panel.test"); err != nil {
		t.Fatal(err)
	}
	pw := "backup-pw"
	err := svc.SaveSettings(BackupSettingsRequest{
		Host: srv.Host, Port: srv.Port, User: "root", Dir: dir, Password: &pw,
		DbEveryHours: 24, DbOnChange: true, NodesEveryHours: 24, Keep: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// An edit must reach the backup server once it settles, without waiting for
// the daily timer, and an idle panel must not upload again and again.
func TestBackupTickBacksUpDatabaseAfterAChange(t *testing.T) {
	svc, dir := setupBackupTarget(t)
	_, at := database.ConfigChanges()
	settled := time.Unix(at, 0).Add(backupChangeSettle + time.Second)

	svc.Tick(context.Background(), time.Unix(at, 0).Add(time.Second))
	if _, err := os.Stat(filepath.Join(dir, backupDbFolder)); !os.IsNotExist(err) {
		t.Fatalf("backed up before the edits settled (stat err %v)", err)
	}
	svc.Tick(context.Background(), settled)
	got := filesIn(t, filepath.Join(dir, backupDbFolder))
	if len(got) != 1 || !strings.HasPrefix(got[0], "panel.test_") || !strings.HasSuffix(got[0], ".db") {
		t.Fatalf("db backups = %v", got)
	}
	if st := svc.Status(); st.Db.Error != "" || st.Db.File != got[0] {
		t.Fatalf("status = %+v", st.Db)
	}
	time.Sleep(1100 * time.Millisecond) // a repeat upload would get a new name
	svc.Tick(context.Background(), settled.Add(time.Hour))
	if again := filesIn(t, filepath.Join(dir, backupDbFolder)); len(again) != 1 {
		t.Fatalf("idle panel backed up again: %v", again)
	}
	cfg, err := svc.Settings()
	if err != nil || cfg.HostKey == "" || !cfg.HasPassword {
		t.Fatalf("settings after first use = %+v, %v", cfg, err)
	}
}

func TestBackupNodesStoresEachNodeArchive(t *testing.T) {
	svc, dir := setupBackupTarget(t)
	src := filepath.Join(t.TempDir(), "x-ui.db")
	if err := os.WriteFile(src, []byte("node db"), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := backup.WriteNodeArchive(&archive, []backup.ArchiveFile{{Src: src, Path: "/etc/x-ui/x-ui.db"}}); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(t.TempDir(), "keys")
	_ = os.WriteFile(evil, []byte("ssh-ed25519 AAAA"), 0o600)
	var unsafe bytes.Buffer
	_ = backup.WriteNodeArchive(&unsafe, []backup.ArchiveFile{{Src: evil, Path: "/root/.ssh/authorized_keys"}})

	serve := func(body []byte) *model.Node {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/panel/api/server/nodeBackup" || r.Header.Get("Authorization") != "Bearer tok" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		u, _ := url.Parse(srv.URL)
		port, _ := strconv.Atoi(u.Port())
		n := &model.Node{Name: "node-" + u.Port(), Scheme: "http", Address: "127.0.0.1", Port: port, BasePath: "/", ApiToken: "tok", Enable: true, AllowPrivateAddress: true}
		if err := (&NodeService{}).Create(n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	good := serve(archive.Bytes())
	bad := serve(unsafe.Bytes())

	for range 3 {
		err := svc.BackupNodes(context.Background())
		if err == nil || !strings.Contains(err.Error(), bad.Name) || strings.Contains(err.Error(), good.Name) {
			t.Fatalf("BackupNodes err = %v, want only %s to fail", err, bad.Name)
		}
		time.Sleep(time.Second)
	}
	kept := filesIn(t, filepath.Join(dir, NodeBackupFolder(good.Id)))
	if len(kept) != 2 {
		t.Fatalf("kept %v, want the newest 2", kept)
	}
	if _, err := os.Stat(filepath.Join(dir, NodeBackupFolder(bad.Id))); !os.IsNotExist(err) {
		t.Fatalf("an unsafe archive was stored (stat err %v)", err)
	}
	st := svc.Status()
	if len(st.Nodes) != 2 || st.NodesAt == 0 {
		t.Fatalf("status = %+v", st)
	}
	files, err := svc.NodeBackups(context.Background(), good.Id)
	if err != nil || len(files) != 2 || files[0].Name != kept[1] {
		t.Fatalf("NodeBackups = %+v, %v (kept %v)", files, err, kept)
	}
}
