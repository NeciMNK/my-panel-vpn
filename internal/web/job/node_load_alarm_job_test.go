package job

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Regression: an overloaded node never paged the admin, because only the
// master's own CPU and RAM were checked. Each node now reports its held load.
func TestNodeLoadAlarmPublishesEachNodesLoadOverTheWindow(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	now := time.Unix(time.Now().Unix()/2*2, 0)
	busy := &model.Node{Id: 7101, Name: "de-1", Address: "10.0.0.1", Port: 2053, Enable: true}
	fresh := &model.Node{Id: 7102, Name: "nl-1", Address: "10.0.0.2", Port: 2053, Enable: true}
	off := &model.Node{Id: 7103, Name: "fi-1", Address: "10.0.0.3", Port: 2053, Enable: true}
	for _, n := range []*model.Node{busy, fresh, off} {
		if err := database.GetDB().Create(n).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	var nodes service.NodeService
	beat := func(n *model.Node, ago time.Duration, cpu, mem float64) {
		t.Helper()
		patch := service.HeartbeatPatch{Status: "online", LastHeartbeat: now.Add(-ago).Unix(), CpuPct: cpu, MemPct: mem}
		if err := nodes.UpdateHeartbeat(n.Id, patch); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
	}
	for ago := 3 * time.Minute; ago >= 0; ago -= 5 * time.Second {
		cpu, mem := 97.0, 60.0
		switch ago {
		case 2*time.Minute + 30*time.Second:
			cpu, mem = 10, 99 // outside the window: must not count
		case time.Minute:
			mem = 62
		}
		beat(busy, ago, cpu, mem)
		beat(off, ago, 99, 99)
	}
	beat(fresh, 20*time.Second, 99, 99)
	beat(fresh, 0, 99, 99)
	if err := nodes.UpdateHeartbeat(off.Id, service.HeartbeatPatch{Status: "offline"}); err != nil {
		t.Fatalf("mark offline: %v", err)
	}

	bus := eventbus.New(0)
	t.Cleanup(bus.Stop)
	got := make(chan eventbus.Event, 8)
	bus.Subscribe("test", func(e eventbus.Event) { got <- e })
	prevBus := EventBus
	EventBus = bus
	t.Cleanup(func() { EventBus = prevBus })

	(&NodeLoadAlarmJob{now: func() time.Time { return now }}).Run()

	var events []eventbus.Event
	for len(events) < 2 {
		select {
		case e := <-got:
			events = append(events, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("got %d events, want cpu and memory of de-1", len(events))
		}
	}
	select {
	case e := <-got:
		t.Fatalf("unexpected extra event %s from %q: a node seen briefly or offline must not alert", e.Type, e.Source)
	case <-time.After(100 * time.Millisecond):
	}
	sort.Slice(events, func(i, k int) bool { return events[i].Type < events[k].Type })
	want := []struct {
		typ       eventbus.EventType
		low, peak float64
	}{
		{eventbus.EventCPUHigh, 97, 97},
		{eventbus.EventMemoryHigh, 60, 62},
	}
	for i, w := range want {
		e := events[i]
		d, _ := e.Data.(*eventbus.SystemMetricData)
		if e.Type != w.typ || e.Source != "de-1" || d == nil || d.Percent != w.low || d.Peak != w.peak {
			t.Errorf("event %d = %s %q %+v, want %s \"de-1\" low %v peak %v", i, e.Type, e.Source, d, w.typ, w.low, w.peak)
		}
	}
}
