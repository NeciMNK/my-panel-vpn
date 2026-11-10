package job

import (
	"context"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// HomeNetworkJob re-applies home networks after group membership, the server
// list or a failed apply changed what their rules should be.
type HomeNetworkJob struct {
	homes   service.HomeNetworkService
	running sync.Mutex
}

func NewHomeNetworkJob() *HomeNetworkJob { return &HomeNetworkJob{} }

func (j *HomeNetworkJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	j.homes.ReconcileHomeNetworks(ctx)
}
