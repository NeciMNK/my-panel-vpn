package job

import (
	"context"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// BackupJob runs the scheduled and on-change backups to the backup server.
// Every master runs it: each one only sees the edits made through itself.
type BackupJob struct{ service service.BackupService }

func NewBackupJob() *BackupJob { return new(BackupJob) }

func (j *BackupJob) Run() {
	j.service.Tick(context.Background(), time.Now())
}
