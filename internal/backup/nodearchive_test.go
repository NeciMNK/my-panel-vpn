package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func rawArchive(t *testing.T, hdrs ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range hdrs {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("x"))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// The archive is unpacked into / as root on the new server, so an entry
// outside the panel's folders would let a node plant e.g. authorized_keys.
func TestInspectNodeArchiveRejectsEscapes(t *testing.T) {
	cases := []struct {
		name string
		hdr  *tar.Header
	}{
		{"ssh keys", &tar.Header{Name: "root/.ssh/authorized_keys", Typeflag: tar.TypeReg}},
		{"dot-dot", &tar.Header{Name: "etc/x-ui/../../root/.ssh/authorized_keys", Typeflag: tar.TypeReg}},
		{"absolute", &tar.Header{Name: "/etc/x-ui/x-ui.db", Typeflag: tar.TypeReg}},
		{"root prefix lookalike", &tar.Header{Name: "etc/x-ui-evil/run.sh", Typeflag: tar.TypeReg}},
		{"symlink", &tar.Header{Name: "etc/x-ui/link", Typeflag: tar.TypeSymlink, Linkname: "/root/.ssh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := InspectNodeArchive(bytes.NewReader(rawArchive(t, tc.hdr)))
			if !errors.Is(err, ErrUnsafeNodeArchive) {
				t.Fatalf("err = %v, want ErrUnsafeNodeArchive", err)
			}
		})
	}
}

func TestNodeArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	snap := write("snap.db", "sqlite")
	files, err := NodeArchiveFiles([]string{filepath.Join(dir, "etc")}, func(p string) bool {
		return filepath.Base(p) == "x-ui.db-wal"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("files from missing root = %v", files)
	}
	write("etc/x-ui/node-cert/cert.pem", "pem")
	write("etc/x-ui/x-ui.db-wal", "wal")
	files, err = NodeArchiveFiles([]string{filepath.Join(dir, "etc")}, func(p string) bool {
		return filepath.Base(p) == "x-ui.db-wal"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %v, want only the certificate", files)
	}
	archive := []ArchiveFile{
		{Src: snap, Path: "/etc/x-ui/x-ui.db"},
		{Src: files[0].Src, Path: "/etc/x-ui/node-cert/cert.pem"},
	}
	var buf bytes.Buffer
	if err := WriteNodeArchive(&buf, archive); err != nil {
		t.Fatal(err)
	}
	info, err := InspectNodeArchive(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if info != (NodeArchiveInfo{Files: 2, HasDB: true, HasNodeTLS: true}) {
		t.Fatalf("info = %+v", info)
	}
}
