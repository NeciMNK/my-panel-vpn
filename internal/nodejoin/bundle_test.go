package nodejoin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestWriteBundleShipsBinaryAndCoreButNotHostState(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "x-ui-master")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]os.FileMode{
		exe:                                    0o755,
		filepath.Join(bin, "xray-linux-amd64"): 0o755,
		filepath.Join(bin, "geoip.dat"):        0o644,
		filepath.Join(bin, "config.json"):      0o644,
		filepath.Join(bin, "access.log"):       0o644,
	}
	for p, m := range files {
		if err := os.WriteFile(p, []byte(filepath.Base(p)), m); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := WriteBundle(&buf, exe, bin); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]int64{}
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h.Mode
		names = append(names, h.Name)
	}
	sort.Strings(names)
	want := []string{"bin/geoip.dat", "bin/xray-linux-amd64", "x-ui"}
	if len(names) != len(want) {
		t.Fatalf("bundle = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("bundle = %v, want %v", names, want)
		}
	}
	if got["x-ui"] != 0o755 || got["bin/xray-linux-amd64"] != 0o755 {
		t.Fatalf("executables lost their mode: %v", got)
	}
}
