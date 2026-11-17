package job

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SubEdgeDNSJob points the subscription domain at the best live tier; it runs
// on every panel, and the service decides who may act.
type SubEdgeDNSJob struct {
	edges service.SubEdgeService
}

func NewSubEdgeDNSJob() *SubEdgeDNSJob { return &SubEdgeDNSJob{} }

func (j *SubEdgeDNSJob) Run() { j.edges.SwitchDNS(context.Background()) }
