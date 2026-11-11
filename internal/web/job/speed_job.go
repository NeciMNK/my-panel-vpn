package job

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/net"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// speedAlertEvery keeps one server from paging the admin more than this often.
const speedAlertEvery = 30 * time.Minute

// SpeedJob measures this panel's throughput and alerts when a server can no
// longer give users their minimum.
type SpeedJob struct {
	speed     service.SpeedService
	nodes     service.NodeService
	running   sync.Mutex
	lastBytes uint64
	lastAt    time.Time
	alerted   map[int]time.Time
}

func NewSpeedJob() *SpeedJob { return &SpeedJob{alerted: map[int]time.Time{}} }

func (j *SpeedJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	j.sampleLocal()
	j.alert()
}

// ShapingJob pushes speed caps to every server; it runs often so a capped user's
// new IP is limited within seconds, and unchanged plans cost no request.
type ShapingJob struct {
	speed   service.SpeedService
	running sync.Mutex
}

func NewShapingJob() *ShapingJob { return &ShapingJob{} }

func (j *ShapingJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	j.speed.PushShaping(ctx)
}

func (j *SpeedJob) sampleLocal() {
	stats, err := net.IOCounters(true)
	if err != nil {
		return
	}
	var sent, recv uint64
	for _, s := range stats {
		name := strings.ToLower(s.Name)
		if name == "lo" || strings.HasPrefix(name, "mph") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") {
			continue
		}
		sent += s.BytesSent
		recv += s.BytesRecv
	}
	now := time.Now()
	busiest := max(sent, recv)
	if !j.lastAt.IsZero() && busiest >= j.lastBytes {
		secs := now.Sub(j.lastAt).Seconds()
		if secs > 0 {
			service.SetLocalThroughputMbps(float64(busiest-j.lastBytes) * 8 / 1e6 / secs)
		}
	}
	j.lastBytes, j.lastAt = busiest, now
}

func (j *SpeedJob) alert() {
	shortages, err := j.speed.Shortages()
	if err != nil {
		logger.Warning("speed: shortages:", err)
		return
	}
	for _, s := range shortages {
		if time.Since(j.alerted[s.NodeId]) < speedAlertEvery || EventBus == nil {
			continue
		}
		j.alerted[s.NodeId] = time.Now()
		name := "this panel"
		if s.NodeId != 0 {
			if n, err := j.nodes.GetById(s.NodeId); err == nil {
				name = n.Name
			}
		}
		EventBus.Publish(eventbus.Event{
			Type:      eventbus.EventSpeedLow,
			Source:    name,
			Data:      &eventbus.SpeedLowData{FreePerUserMbps: s.FreePerUserMbps, MinMbps: s.MinMbps, Users: s.Users},
			Timestamp: time.Now(),
		})
	}
}

// SpeedTestJob measures the servers whose scheduled speed test is due.
type SpeedTestJob struct {
	speed   service.SpeedService
	running sync.Mutex
}

func NewSpeedTestJob() *SpeedTestJob { return &SpeedTestJob{} }

func (j *SpeedTestJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	j.speed.RunScheduled(time.Now())
}
