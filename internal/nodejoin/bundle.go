// Package nodejoin turns a fresh server into a node of this master with one
// command: the master serves the installer, its own build and a one-time join.
package nodejoin

import (
	"archive/tar"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

//go:embed install-node.sh
var installScript []byte

// InstallScript is the shell installer the join command pipes into bash.
func InstallScript() []byte { return installScript }

// Arch names the node architecture the installer reports for this master's
// build; a node must match it because the bundle is this very binary.
func Arch() string {
	if goruntime.GOARCH == "arm" {
		return "armv7"
	}
	return goruntime.GOARCH
}

// skipBinFile drops per-host state from bin/: the generated core config, logs
// and temp files must not leak from the master to a node.
func skipBinFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".tmp") || strings.HasPrefix(lower, ".")
}

// WriteBundle streams a tar.gz with the panel executable as x-ui, the files of
// binDir under bin/ and, when present, the x-ui.sh management script.
func WriteBundle(w io.Writer, exePath, binDir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	if err := addFile(tw, exePath, "x-ui", 0o755); err != nil {
		return err
	}
	script := filepath.Join(filepath.Dir(exePath), "x-ui.sh")
	if st, err := os.Stat(script); err == nil && st.Mode().IsRegular() {
		if err := addFile(tw, script, "x-ui.sh", 0o755); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("read bin folder: %w", err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || skipBinFile(e.Name()) {
			continue
		}
		mode := int64(0o644)
		if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := addFile(tw, filepath.Join(binDir, e.Name()), "bin/"+e.Name(), mode); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addFile(tw *tar.Writer, src, name string, mode int64) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: mode, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
