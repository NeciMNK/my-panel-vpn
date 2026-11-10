package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// masterXraySyncBeats is how many beats pass between pulls of the shared
// Xray config, so a change made on another master reaches this one's core.
const masterXraySyncBeats = 6

// MasterClusterJob beats this master's heartbeat, follows the election and
// keeps the local core in step with the config the masters share.
type MasterClusterJob struct {
	cluster    service.MasterClusterService
	xray       service.XrayService
	onFlip     func(leader bool)
	lastLeader string // the last other master seen leading
	beats      int
	failing    bool
}

func NewMasterClusterJob(onFlip func(leader bool)) *MasterClusterJob {
	return &MasterClusterJob{onFlip: onFlip}
}

func (j *MasterClusterJob) Run() {
	leader, flipped, err := j.cluster.Beat()
	if err != nil {
		if !j.failing {
			logger.Warning("master cluster: heartbeat failed, this master stops leading:", err)
		}
		j.failing = true
	} else {
		j.failing = false
	}
	if flipped {
		leads := service.IsClusterLeader()
		logger.Infof("master cluster: this master is now the %s (leader: %q)", map[bool]string{true: "leader", false: "follower"}[leads], leader)
		if j.onFlip != nil {
			j.onFlip(leads)
		}
		if leads && EventBus != nil {
			EventBus.Publish(eventbus.Event{
				Type:      eventbus.EventMasterLeader,
				Source:    leader,
				Data:      &eventbus.MasterLeaderData{Previous: j.lastLeader},
				Timestamp: time.Now(),
			})
		}
	}
	if leader != "" && leader != config.GetMasterName() {
		j.lastLeader = leader
	}
	j.beats++
	if j.beats%masterXraySyncBeats == 0 {
		if err := j.xray.RestartXray(false); err != nil {
			logger.Debug("master cluster: sync xray config:", err)
		}
	}
}
