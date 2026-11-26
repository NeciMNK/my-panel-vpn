package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
)

// A restore must bring the node's database and TLS key back into place, or
// fall back to a fresh node when the backup lacks them (e.g. a PostgreSQL node).
func TestInstallNodeRestoresNodeBackup(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	src := t.TempDir()
	file := func(name, body string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	db, cert, key := file("db", "SQLite format 3"), file("cert", "CERT"), file("key", "KEY")
	archive := func(t *testing.T, files ...backup.ArchiveFile) string {
		t.Helper()
		var buf bytes.Buffer
		if err := backup.WriteNodeArchive(&buf, files); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "node.tar.gz")
		if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name     string
		files    []backup.ArchiveFile
		restored bool
	}{
		{"full node", []backup.ArchiveFile{
			{Src: db, Path: "/etc/x-ui/x-ui.db"},
			{Src: cert, Path: "/etc/x-ui/node-cert/cert.pem"},
			{Src: key, Path: "/etc/x-ui/node-cert/key.pem"},
		}, true},
		{"no database", []backup.ArchiveFile{
			{Src: cert, Path: "/etc/x-ui/node-cert/cert.pem"},
			{Src: key, Path: "/etc/x-ui/node-cert/key.pem"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			driver := "set -euo pipefail\nCERT_DIR=/etc/x-ui/node-cert\ndie() { echo \"error: $*\" >&2; exit 1; }\n" +
				scriptFunction(t, installNodeScript, "restore_node_state") +
				"if restore_node_state \"$1\" \"$2\"; then echo restored; else echo fresh; fi\n"
			out, err := exec.Command(bash, "-c", driver, "drv", archive(t, tc.files...), root).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			want := "fresh\n"
			if tc.restored {
				want = "restored\n"
			}
			if string(out) != want {
				t.Fatalf("output %q, want %q", out, want)
			}
			got, err := os.ReadFile(filepath.Join(root, "etc/x-ui/node-cert/key.pem"))
			if err != nil || string(got) != "KEY" {
				t.Fatalf("key.pem = %q, %v", got, err)
			}
			if info, _ := os.Stat(filepath.Join(root, "etc/x-ui/node-cert/key.pem")); info.Mode().Perm() != 0o600 {
				t.Fatalf("key.pem mode = %v, want 0600", info.Mode().Perm())
			}
		})
	}
}
