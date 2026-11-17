package service

import (
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	speedDropMaxPercent = 90
	speedRecheckRuns    = 2
	// speedRecheckEvery spaces the rechecks so both finish within ten minutes of the first result.
	speedRecheckEvery = 4 * time.Minute
	// speedDropMinHistory results make an average worth comparing against.
	speedDropMinHistory = 3
)

// SpeedDrop is a server whose speed stayed far below its 30-day average on a
// scheduled test and on every recheck after it.
type SpeedDrop struct {
	NodeId          int
	Percent         int
	AvgDownloadMbps float64
	AvgUploadMbps   float64
	Results         []model.SpeedTestResult
}

type speedRecheck struct {
	drop SpeedDrop
	next time.Time
}

var speedRechecks = struct {
	mu     sync.Mutex
	byNode map[int]*speedRecheck
}{byNode: map[int]*speedRecheck{}}

func resetSpeedRechecks() {
	speedRechecks.mu.Lock()
	speedRechecks.byNode = map[int]*speedRecheck{}
	speedRechecks.mu.Unlock()
}

// speedBaseline averages the server's successful non-recheck results of the
// last 30 days before row; ok is false when there are too few.
func speedBaseline(row *model.SpeedTestResult) (down, up float64, ok bool) {
	var rows []model.SpeedTestResult
	since := time.UnixMilli(row.CreatedAt).Add(-speedTestStatsWindow).UnixMilli()
	err := database.GetDB().Where("node_id = ? AND id <> ? AND created_at >= ? AND error = ?", row.NodeId, row.Id, since, "").
		Not(map[string]any{"trigger": SpeedTestRecheck}).Find(&rows).Error
	if err != nil || len(rows) < speedDropMinHistory {
		return 0, 0, false
	}
	for _, r := range rows {
		down += r.DownloadMbps
		up += r.UploadMbps
	}
	n := float64(len(rows))
	return down / n, up / n, true
}

func belowAverage(row *model.SpeedTestResult, d SpeedDrop) bool {
	if row == nil || row.Error != "" {
		return false
	}
	factor := 1 - float64(d.Percent)/100
	return row.DownloadMbps < d.AvgDownloadMbps*factor || row.UploadMbps < d.AvgUploadMbps*factor
}

// watchSpeedDrop queues rechecks for a result far below the server's average.
func watchSpeedDrop(row *model.SpeedTestResult, percent int, now time.Time) {
	if row == nil || row.Error != "" || percent <= 0 {
		return
	}
	down, up, ok := speedBaseline(row)
	if !ok {
		return
	}
	drop := SpeedDrop{NodeId: row.NodeId, Percent: percent, AvgDownloadMbps: down, AvgUploadMbps: up}
	if !belowAverage(row, drop) {
		return
	}
	drop.Results = []model.SpeedTestResult{*row}
	speedRechecks.mu.Lock()
	speedRechecks.byNode[row.NodeId] = &speedRecheck{drop: drop, next: now.Add(speedRecheckEvery)}
	speedRechecks.mu.Unlock()
}

// runRechecks measures every server whose recheck is due and returns the drops
// that every recheck confirmed; one recovered result clears the suspicion.
func (s *SpeedService) runRechecks(now time.Time) []SpeedDrop {
	speedRechecks.mu.Lock()
	var due []int
	for id, rc := range speedRechecks.byNode {
		if !now.Before(rc.next) {
			due = append(due, id)
		}
	}
	speedRechecks.mu.Unlock()

	var drops []SpeedDrop
	for _, id := range due {
		if !claimSpeedTest(id) {
			continue
		}
		row := s.runTest(id, SpeedTestRecheck)
		releaseSpeedTest(id)

		speedRechecks.mu.Lock()
		rc := speedRechecks.byNode[id]
		switch {
		case rc == nil:
		case !belowAverage(row, rc.drop):
			delete(speedRechecks.byNode, id)
		default:
			rc.drop.Results = append(rc.drop.Results, *row)
			rc.next = now.Add(speedRecheckEvery)
			if len(rc.drop.Results) > speedRecheckRuns {
				drops = append(drops, rc.drop)
				delete(speedRechecks.byNode, id)
				logger.Warning("speed test:", nodeLabel(id), "stayed below its average on every recheck")
			}
		}
		speedRechecks.mu.Unlock()
	}
	return drops
}
