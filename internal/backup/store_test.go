package backup_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/backup/sshtest"
)

func TestStoreKeepsNewestAndHidesPartial(t *testing.T) {
	srv := sshtest.Start(t, "pw")
	login := backup.SSHLogin{Host: srv.Host, Port: srv.Port, User: "root", Password: srv.Password}
	dir := filepath.Join(t.TempDir(), "backups")
	ctx := context.Background()
	st, err := backup.OpenStore(ctx, login, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.HostKey != srv.Fingerprint {
		t.Fatalf("host key = %q, want %q", st.HostKey, srv.Fingerprint)
	}
	for i, name := range []string{"a_1.tar.gz", "a_2.tar.gz", "a_3.tar.gz"} {
		if err := st.Put("nodes_n1", name, strings.NewReader(name)); err != nil {
			t.Fatal(err)
		}
		mt := time.Unix(1_700_000_000+int64(i)*60, 0)
		if err := os.Chtimes(filepath.Join(dir, "nodes_n1", name), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "nodes_n1", "a_4.tar.gz.part"), []byte("cut"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Prune("nodes_n1", 2); err != nil {
		t.Fatal(err)
	}
	files, err := st.List("nodes_n1")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); got != "a_3.tar.gz,a_2.tar.gz" {
		t.Fatalf("kept = %s, want a_3.tar.gz,a_2.tar.gz", got)
	}
	rc, err := st.Open("nodes_n1", "a_3.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "a_3.tar.gz" {
		t.Fatalf("body = %q", body)
	}
	if err := st.Put("../escape", "x", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "invalid backup folder") {
		t.Fatalf("Put outside the folder: err = %v", err)
	}
}

func TestDialRefusesChangedHostKey(t *testing.T) {
	srv := sshtest.Start(t, "pw")
	login := backup.SSHLogin{Host: srv.Host, Port: srv.Port, User: "root", Password: srv.Password}
	l := login
	l.HostKey = "SHA256:somethingelse"
	_, _, err := backup.Dial(context.Background(), l)
	if !errors.Is(err, backup.ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged", err)
	}
	l.HostKey = srv.Fingerprint
	c, _, err := backup.Dial(context.Background(), l)
	if err != nil {
		t.Fatalf("pinned key: %v", err)
	}
	out, err := backup.Run(c, "printf %s "+backup.ShellQuote("it's ok"))
	_ = c.Close()
	if err != nil || out != "it's ok" {
		t.Fatalf("Run = %q, %v", out, err)
	}
}

// A backup holds the whole database and node keys, so the server's umask
// (0664 on the stand) must not leave it readable to other accounts.
func TestStorePutWritesOwnerOnlyFile(t *testing.T) {
	srv := sshtest.Start(t, "pw")
	login := backup.SSHLogin{Host: srv.Host, Port: srv.Port, User: "root", Password: srv.Password}
	dir := filepath.Join(t.TempDir(), "backups")
	st, err := backup.OpenStore(context.Background(), login, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Put("db", "x-ui_1.db", strings.NewReader("secret")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "db", "x-ui_1.db"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("backup mode = %v, want -rw-------", perm)
	}
}
