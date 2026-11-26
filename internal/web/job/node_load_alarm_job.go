package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// NodeLoadAlarmJob publishes every online node's CPU and RAM held over the last
// window as cpu.high / memory.high, carrying the node's own threshold if it has one.
type NodeLoadAlarmJob struct {
	nodeService    service.NodeService
	settingService service.SettingService
	now            func() time.Time
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
	// nodeLoadMinutes is how long the load must hold, so a short spike does not page.
	window := 2 * time.Minute
	if minutes, err := j.settingService.GetNodeLoadMinutes(); err == nil && minutes > 0 {
		window = time.Duration(minutes) * time.Minute
	}
	for _, n := range nodes {
		if !n.Enable || n.Status != "online" {
			continue
		}
		for _, m := range []struct {
			metric    string
			eventType eventbus.EventType
			threshold int
		}{{"cpu", eventbus.EventCPUHigh, n.AlertCpu}, {"mem", eventbus.EventMemoryHigh, n.AlertMemory}} {
			low, high, ok := j.nodeService.NodeLoadSpan(n.Id, m.metric, window, now)
			if !ok {
				continue
			}
			EventBus.Publish(eventbus.Event{
				Type:      m.eventType,
				Source:    nodeEventSource(n),
				Data:      &eventbus.SystemMetricData{Percent: low, Peak: high, Threshold: m.threshold},
				Timestamp: now,
			})
		}
	}
}
