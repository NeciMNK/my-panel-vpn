package job

import "github.com/mhsanaei/3x-ui/v3/internal/web/service"

// SubEdgeTunnelJob keeps this master's tunnel to every subscription edge node
// open; it runs on every master, not only the leader.
type SubEdgeTunnelJob struct {
	edges service.SubEdgeService
}

func NewSubEdgeTunnelJob() *SubEdgeTunnelJob { return &SubEdgeTunnelJob{} }

func (j *SubEdgeTunnelJob) Run() { j.edges.KeepTunnels() }
