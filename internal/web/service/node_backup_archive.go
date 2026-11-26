package service

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

// nodeCertRoot is where acme.sh, which x-ui.sh drives, keeps issued certificates.
var nodeCertRoot = "/root/cert"

// WriteNodeBackup streams this panel's own state as a node archive: a
// consistent snapshot of its SQLite database and the files beside it.
func (s *ServerService) WriteNodeBackup(w io.Writer) error {
	dbPath := config.GetDBPath()
	skip := func(p string) bool {
		base := filepath.Base(p)
		return strings.HasPrefix(base, filepath.Base(dbPath)) || strings.HasPrefix(base, ".x-ui-backup-")
	}
	files, err := backup.NodeArchiveFiles([]string{config.GetDBFolderPath(), nodeCertRoot}, skip)
	if err != nil {
		return err
	}
	if !database.IsPostgres() {
		snapshot, cleanup, err := s.backupSQLite()
		if err != nil {
			return err
		}
		defer cleanup()
		files = append(files, backup.ArchiveFile{Src: snapshot, Path: filepath.ToSlash(dbPath)})
	}
	return backup.WriteNodeArchive(w, files)
}
