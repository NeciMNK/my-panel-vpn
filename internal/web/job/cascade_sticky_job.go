package job

import (
	"context"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// CascadeStickyJob keeps users of multi-exit cascades on the exit they got.
type CascadeStickyJob struct {
	cascades service.CascadeService
	running  sync.Mutex
}

func NewCascadeStickyJob() *CascadeStickyJob { return &CascadeStickyJob{} }

func (j *CascadeStickyJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	j.cascades.StickCascadeUsers(ctx)
}
