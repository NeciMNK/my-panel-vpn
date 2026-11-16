package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/speedtest"
)

// switchableSpeedTests measures whatever speed the test last set.
type switchableSpeedTests struct {
	mu       sync.Mutex
	down, up float64
}

func (s *switchableSpeedTests) set(down, up float64) {
	s.mu.Lock()
	s.down, s.up = down, up
	s.mu.Unlock()
}

func (s *switchableSpeedTests) start(context.Context, int) (speedtest.State, error) {
	return speedtest.State{Run: 1, Running: true}, nil
}

func (s *switchableSpeedTests) state(context.Context, int) (speedtest.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return speedtest.State{Run: 1, Result: &speedtest.Result{Provider: "test", DownloadMbps: s.down, UploadMbps: s.up}}, nil
}

// A scheduled test far below the month's average is measured twice more within
// ten minutes; only when both agree does the admin hear about it.
func TestSpeedDropIsConfirmedByTwoRechecks(t *testing.T) {
	cases := []struct {
		name      string
		rechecks  [2]float64
		wantDrop  bool
		wantTests int
	}{
		{"both rechecks low: alert", [2]float64{480, 510}, true, 3},
		{"second recheck recovered: no alert", [2]float64{480, 880}, false, 3},
		{"first recheck recovered: stop early", [2]float64{870, 480}, false, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
			resetSpeedRechecks()
			t.Cleanup(resetSpeedRechecks)
			target := &switchableSpeedTests{}
			prevTarget, prevPoll := speedTests, speedTestPoll
			speedTests, speedTestPoll = target, time.Millisecond
			t.Cleanup(func() { speedTests, speedTestPoll = prevTarget, prevPoll })

			s := &SpeedService{}
			if err := s.SetTestSettings(&SpeedTestSettings{EveryHours: 6, Timezone: "UTC"}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			for i, down := range []float64{900, 950, 850, 900} {
				row := model.SpeedTestResult{NodeId: 0, Trigger: SpeedTestSchedule, DownloadMbps: down, UploadMbps: 900, CreatedAt: now.Add(-time.Duration(7+6*i) * time.Hour).UnixMilli()}
				if err := database.GetDB().Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}

			target.set(500, 900)
			var drops []SpeedDrop
			drops = append(drops, s.RunScheduled(now)...)
			drops = append(drops, s.RunScheduled(now.Add(time.Minute))...)
			target.set(c.rechecks[0], 900)
			drops = append(drops, s.RunScheduled(now.Add(speedRecheckEvery))...)
			target.set(c.rechecks[1], 900)
			drops = append(drops, s.RunScheduled(now.Add(2*speedRecheckEvery))...)
			target.set(400, 900)
			drops = append(drops, s.RunScheduled(now.Add(3*speedRecheckEvery))...)

			if got := len(drops) == 1; got != c.wantDrop {
				t.Fatalf("drops = %+v, want alert %v", drops, c.wantDrop)
			}
			if c.wantDrop {
				d := drops[0]
				if d.NodeId != 0 || d.AvgDownloadMbps != 900 || len(d.Results) != 3 || d.Results[2].DownloadMbps != 510 {
					t.Fatalf("drop = %+v, want avg 900 and the three low results", d)
				}
			}
			var tests int64
			database.GetDB().Model(&model.SpeedTestResult{}).Where("created_at > ?", now.Add(-time.Minute).UnixMilli()).Count(&tests)
			if tests != int64(c.wantTests) {
				t.Fatalf("tests run = %d, want %d", tests, c.wantTests)
			}
			stats, err := speedTestStats(time.Now())
			if err != nil || len(stats) != 1 || stats[0].Count != 5 {
				t.Fatalf("stats = %+v, %v: rechecks must not count toward the average", stats, err)
			}
		})
	}
}
