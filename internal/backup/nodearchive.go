package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// NodeArchiveRoots are the only places a node archive may write to when it is
// unpacked into / on a new server: the panel's folder and acme.sh's certificates.
var NodeArchiveRoots = []string{"etc/x-ui/", "root/cert/"}

// MaxNodeArchiveBytes caps an unpacked node archive; a node's state is a few MB.
const MaxNodeArchiveBytes = 512 << 20

// ArchiveFile is one file going into a node archive under its absolute path.
type ArchiveFile struct {
	Src  string
	Path string
}

// NodeArchiveFiles lists the files under each existing root, skipping what skip
// reports and the panel's live database files, which go in as a snapshot.
func NodeArchiveFiles(roots []string, skip func(path string) bool) ([]ArchiveFile, error) {
	var files []ArchiveFile
	for _, root := range roots {
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if skip != nil && skip(p) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() {
				files = append(files, ArchiveFile{Src: p, Path: filepath.ToSlash(p)})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// WriteNodeArchive writes files as a tar.gz whose entry names are their paths
// without the leading slash, so `tar -xzf archive -C /` puts them back.
func WriteNodeArchive(w io.Writer, files []ArchiveFile) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := addArchiveFile(tw, f); err != nil {
			return fmt.Errorf("%s: %w", f.Src, err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addArchiveFile(tw *tar.Writer, f ArchiveFile) error {
	src, err := os.Open(f.Src)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:     strings.TrimPrefix(f.Path, "/"),
		Mode:     int64(info.Mode().Perm()),
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.CopyN(tw, src, info.Size())
	return err
}

// ErrUnsafeNodeArchive means an archive would write outside NodeArchiveRoots.
var ErrUnsafeNodeArchive = errors.New("node backup has an entry outside /etc/x-ui and /root/cert")

// NodeArchiveInfo is what a valid node archive holds.
type NodeArchiveInfo struct {
	Files      int
	HasDB      bool
	HasNodeTLS bool
}

// InspectNodeArchive checks that r is a node archive safe to unpack into / as
// root: plain files and folders, only under NodeArchiveRoots.
func InspectNodeArchive(r io.Reader) (NodeArchiveInfo, error) {
	var info NodeArchiveInfo
	gz, err := gzip.NewReader(r)
	if err != nil {
		return info, err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return info, nil
		}
		if err != nil {
			return info, err
		}
		name := hdr.Name
		clean := path.Clean(name)
		if path.IsAbs(name) || clean != strings.TrimSuffix(name, "/") || !underArchiveRoots(clean+"/") {
			return info, fmt.Errorf("%w: %q", ErrUnsafeNodeArchive, name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return info, fmt.Errorf("%w: %q is not a plain file", ErrUnsafeNodeArchive, name)
		}
		total += hdr.Size
		if total > MaxNodeArchiveBytes {
			return info, errors.New("node backup is larger than 512 MB")
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return info, err
		}
		info.Files++
		switch clean {
		case "etc/x-ui/x-ui.db":
			info.HasDB = true
		case "etc/x-ui/node-cert/cert.pem":
			info.HasNodeTLS = true
		}
	}
}

func underArchiveRoots(p string) bool {
	for _, root := range NodeArchiveRoots {
		if strings.HasPrefix(p, root) && len(p) > len(root) {
			return true
		}
	}
	return false
}
