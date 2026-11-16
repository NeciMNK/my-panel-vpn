package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// nodeLoadWindow is how long a node's CPU or RAM must stay above the threshold
// before it alerts, so a short spike does not page the admin.
const nodeLoadWindow = 2 * time.Minute

// NodeLoadAlarmJob publishes every online node's CPU and RAM held over the last
// window as cpu.high / memory.high; each notifier applies its own threshold.
type NodeLoadAlarmJob struct {
	nodeService service.NodeService
	now         func() time.Time
}

func NewNodeLoadAlarmJob() *NodeLoadAlarmJob {
	return &NodeLoadAlarmJob{now: time.Now}
}

func (j *NodeLoadAlarmJob) Run() {
	if EventBus == nil {
		return
	}
	nodes, err := j.nodeService.GetAll()
	if err != nil {
		logger.Warning("node load alarm: load nodes failed:", err)
		return
	}
	now := j.now()
	for _, n := range nodes {
		if !n.Enable || n.Status != "online" {
			continue
		}
		for metric, eventType := range map[string]eventbus.EventType{"cpu": eventbus.EventCPUHigh, "mem": eventbus.EventMemoryHigh} {
			low, high, ok := j.nodeService.NodeLoadSpan(n.Id, metric, nodeLoadWindow, now)
			if !ok {
				continue
			}
			EventBus.Publish(eventbus.Event{
				Type:      eventType,
				Source:    nodeEventSource(n),
				Data:      &eventbus.SystemMetricData{Percent: low, Peak: high},
				Timestamp: now,
			})
		}
	}
}
