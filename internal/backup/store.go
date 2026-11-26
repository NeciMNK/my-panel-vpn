package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// StoredFile is one backup kept on the backup server.
type StoredFile struct {
	Name    string `json:"name" example:"de-fra-1_2026-11-25_030000.tar.gz"`
	Size    int64  `json:"size" example:"524288"`
	ModTime int64  `json:"modTime" example:"1764039600"`
}

// Store is the backup folder on the backup server, reached over SFTP.
type Store struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	dir  string
	// HostKey is the fingerprint the server answered with.
	HostKey string
}

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// SafeName reduces s to a file or folder name that stays inside its folder.
func SafeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
}

// OpenStore logs in to the backup server and makes sure dir exists.
func OpenStore(ctx context.Context, l SSHLogin, dir string) (*Store, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("backup folder is empty")
	}
	client, seen, err := Dial(ctx, l)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("SFTP is not available on the backup server: %w", err)
	}
	s := &Store{ssh: client, sftp: sc, dir: dir, HostKey: seen}
	if err := sc.MkdirAll(dir); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	return s, nil
}

func (s *Store) Close() error {
	_ = s.sftp.Close()
	return s.ssh.Close()
}

func (s *Store) folder(sub string) (string, error) {
	if !safeSegment.MatchString(sub) || sub == ".." {
		return "", fmt.Errorf("invalid backup folder %q", sub)
	}
	return path.Join(s.dir, sub), nil
}

// Put writes r as sub/name, via a temporary name so a cut upload never looks complete.
func (s *Store) Put(sub, name string, r io.Reader) error {
	dir, err := s.folder(sub)
	if err != nil {
		return err
	}
	if !safeSegment.MatchString(name) {
		return fmt.Errorf("invalid backup name %q", name)
	}
	if err := s.sftp.MkdirAll(dir); err != nil {
		return err
	}
	final := path.Join(dir, name)
	tmp := final + ".part"
	f, err := s.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = s.sftp.Remove(tmp)
		return err
	}
	if _, err := f.ReadFrom(r); err != nil {
		_ = f.Close()
		_ = s.sftp.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = s.sftp.Remove(tmp)
		return err
	}
	_ = s.sftp.Remove(final)
	return s.sftp.Rename(tmp, final)
}

// List returns the finished backups in sub, newest first.
func (s *Store) List(sub string) ([]StoredFile, error) {
	dir, err := s.folder(sub)
	if err != nil {
		return nil, err
	}
	entries, err := s.sftp.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]StoredFile, 0, len(entries))
	for _, e := range entries {
		if !e.Mode().IsRegular() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		files = append(files, StoredFile{Name: e.Name(), Size: e.Size(), ModTime: e.ModTime().Unix()})
	}
	slices.SortFunc(files, func(a, b StoredFile) int {
		if a.ModTime != b.ModTime {
			return int(b.ModTime - a.ModTime)
		}
		return strings.Compare(b.Name, a.Name)
	})
	return files, nil
}

// Open reads sub/name.
func (s *Store) Open(sub, name string) (io.ReadCloser, error) {
	dir, err := s.folder(sub)
	if err != nil {
		return nil, err
	}
	if !safeSegment.MatchString(name) {
		return nil, fmt.Errorf("invalid backup name %q", name)
	}
	return s.sftp.Open(path.Join(dir, name))
}

// Prune deletes all but the newest keep backups in sub.
func (s *Store) Prune(sub string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	files, err := s.List(sub)
	if err != nil || len(files) <= keep {
		return err
	}
	dir, _ := s.folder(sub)
	for _, f := range files[keep:] {
		if err := s.sftp.Remove(path.Join(dir, f.Name)); err != nil {
			return err
		}
	}
	return nil
}
