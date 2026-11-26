package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A node's backup must carry a readable copy of its live database (not the
// WAL-split files) plus its TLS material, so a restore brings the node back.
func TestWriteNodeBackupSnapshotsDatabaseAndFiles(t *testing.T) {
	setupConflictDB(t)
	dir := config.GetDBFolderPath()
	certRoot := t.TempDir()
	prevCertRoot := nodeCertRoot
	nodeCertRoot = certRoot
	t.Cleanup(func() { nodeCertRoot = prevCertRoot })
	if err := os.WriteFile(filepath.Join(certRoot, "fullchain.cer"), []byte("ACME"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node-cert"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node-cert", "cert.pem"), []byte("PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.Inbound{Tag: "kept-in-backup", Port: 4443, Protocol: model.VLESS, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := (&ServerService{}).WriteNodeBackup(&buf); err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		entries[hdr.Name] = body
	}
	prefix := strings.TrimPrefix(filepath.ToSlash(dir), "/") + "/"
	if string(entries[prefix+"node-cert/cert.pem"]) != "PEM" {
		t.Fatalf("certificate missing; entries: %v", keys(entries))
	}
	if string(entries[strings.TrimPrefix(filepath.ToSlash(certRoot), "/")+"/fullchain.cer"]) != "ACME" {
		t.Fatalf("acme certificate missing; entries: %v", keys(entries))
	}
	for name := range entries {
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.Contains(name, ".x-ui-backup-") {
			t.Fatalf("live database file %s in the archive", name)
		}
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := os.WriteFile(snap, entries[prefix+"x-ui.db"], 0o600); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite3", snap)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM inbounds WHERE tag = 'kept-in-backup'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("snapshot inbound count = %d, err %v", n, err)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
