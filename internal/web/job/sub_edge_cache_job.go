package job

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SubEdgeCacheJob sends every edge node the subscription responses this master
// gave since the last run; every master sends its own.
type SubEdgeCacheJob struct {
	edges service.SubEdgeService
}

func NewSubEdgeCacheJob() *SubEdgeCacheJob { return &SubEdgeCacheJob{} }

func (j *SubEdgeCacheJob) Run() { j.edges.PushCaches(context.Background()) }
