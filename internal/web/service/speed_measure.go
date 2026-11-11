package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/speedtest"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

const (
	SpeedTestManual   = "manual"
	SpeedTestSchedule = "schedule"

	// speedTestKeep is how long results stay in the history.
	speedTestKeep = 180 * 24 * time.Hour
	// speedTestStatsWindow is the span the per-server statistics cover.
	speedTestStatsWindow = 30 * 24 * time.Hour
	speedTestMaxHours    = 24 * 7
	// speedTestSlotWindow is how late a clock-time test may still start, so a
	// busy or restarted panel does not skip it.
	speedTestSlotWindow = 15 * time.Minute
	speedTestMaxTimes   = 48
)

// SpeedTestSettings: AutoCapacity makes every successful test set the server's
// link speed; EveryHours > 0 and Times (HH:MM in Timezone) also schedule tests.
type SpeedTestSettings struct {
	AutoCapacity bool     `json:"autoCapacity" example:"true"`
	EveryHours   int      `json:"everyHours" example:"6"`
	Times        []string `json:"times" example:"[\"03:00\",\"13:30\"]"`
	Timezone     string   `json:"timezone" example:"Europe/Moscow"`
}

// SpeedTestStats sums up one server's successful tests over the last 30 days.
type SpeedTestStats struct {
	NodeId          int                    `json:"nodeId" example:"0"`
	Last            *model.SpeedTestResult `json:"last"`
	Count           int                    `json:"count" example:"12"`
	AvgDownloadMbps float64                `json:"avgDownloadMbps" example:"910.4"`
	AvgUploadMbps   float64                `json:"avgUploadMbps" example:"880.2"`
	MinDownloadMbps float64                `json:"minDownloadMbps" example:"640"`
	MaxDownloadMbps float64                `json:"maxDownloadMbps" example:"948.1"`
}

// SpeedTestRequest names the server to measure (node id 0 = this panel).
type SpeedTestRequest struct {
	NodeId int `json:"nodeId" example:"0"`
}

// speedTestTarget starts a measurement on a server and reports its progress.
type speedTestTarget interface {
	start(ctx context.Context, nodeID int) (speedtest.State, error)
	state(ctx context.Context, nodeID int) (speedtest.State, error)
}

// serverSpeedTests runs the panel's own test in-process and a node's on the node.
type serverSpeedTests struct{}

func (serverSpeedTests) start(ctx context.Context, nodeID int) (speedtest.State, error) {
	if nodeID == 0 {
		return speedtest.Local.Start(), nil
	}
	return nodeSpeedTest(ctx, nodeID, http.MethodPost)
}

func (serverSpeedTests) state(ctx context.Context, nodeID int) (speedtest.State, error) {
	if nodeID == 0 {
		return speedtest.Local.State(), nil
	}
	return nodeSpeedTest(ctx, nodeID, http.MethodGet)
}

func nodeSpeedTest(ctx context.Context, nodeID int, method string) (speedtest.State, error) {
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, nodeID, method, "/speedTest", "", "", nil)
	if err != nil {
		return speedtest.State{}, err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return speedtest.State{}, err
	}
	var st speedtest.State
	if err := json.Unmarshal(obj, &st); err != nil {
		return speedtest.State{}, fmt.Errorf("node reply: %w", err)
	}
	return st, nil
}

var (
	speedTests       speedTestTarget = serverSpeedTests{}
	speedTestPoll                    = 3 * time.Second
	speedTestTimeout                 = 5 * time.Minute

	speedTestMu      sync.Mutex
	speedTestRunning = map[int]bool{}
)

func speedTestsRunning() []int {
	speedTestMu.Lock()
	defer speedTestMu.Unlock()
	out := []int{}
	for id := range speedTestRunning {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func claimSpeedTest(nodeID int) bool {
	speedTestMu.Lock()
	defer speedTestMu.Unlock()
	if speedTestRunning[nodeID] {
		return false
	}
	speedTestRunning[nodeID] = true
	return true
}

func releaseSpeedTest(nodeID int) {
	speedTestMu.Lock()
	defer speedTestMu.Unlock()
	delete(speedTestRunning, nodeID)
}

func (s *SpeedService) TestSettings() (SpeedTestSettings, error) {
	settings := &SettingService{}
	auto, err := settings.GetSpeedTestAutoCapacity()
	if err != nil {
		return SpeedTestSettings{}, err
	}
	every, err := settings.GetSpeedTestEveryHours()
	if err != nil {
		return SpeedTestSettings{}, err
	}
	times, err := settings.GetSpeedTestTimes()
	if err != nil {
		return SpeedTestSettings{}, err
	}
	zone, err := settings.GetSpeedTestTimezone()
	if err != nil {
		return SpeedTestSettings{}, err
	}
	out := SpeedTestSettings{AutoCapacity: auto, EveryHours: every, Times: []string{}, Timezone: zone}
	if times != "" {
		out.Times = strings.Split(times, ",")
	}
	return out, nil
}

func (s *SpeedService) SetTestSettings(req *SpeedTestSettings) error {
	if req == nil || req.EveryHours < 0 || req.EveryHours > speedTestMaxHours {
		return common.NewErrorf("the schedule must be 0 to %d hours", speedTestMaxHours)
	}
	times, err := normalizeTestTimes(req.Times)
	if err != nil {
		return err
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil || req.Timezone == "" {
		return common.NewErrorf("unknown time zone %q", req.Timezone)
	}
	settings := &SettingService{}
	if err := settings.SetSpeedTestAutoCapacity(req.AutoCapacity); err != nil {
		return err
	}
	if err := settings.SetSpeedTestEveryHours(req.EveryHours); err != nil {
		return err
	}
	if err := settings.SetSpeedTestTimes(strings.Join(times, ",")); err != nil {
		return err
	}
	return settings.SetSpeedTestTimezone(req.Timezone)
}

// normalizeTestTimes turns "3:00" into "03:00", sorted and without repeats.
func normalizeTestTimes(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		h, m, ok := parseClock(strings.TrimSpace(raw))
		if !ok {
			return nil, common.NewErrorf("%q is not a time of day (HH:MM)", raw)
		}
		t := fmt.Sprintf("%02d:%02d", h, m)
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) > speedTestMaxTimes {
		return nil, common.NewErrorf("at most %d test times", speedTestMaxTimes)
	}
	sort.Strings(out)
	return out, nil
}

func parseClock(v string) (hour, minute int, ok bool) {
	hs, ms, found := strings.Cut(v, ":")
	if !found || len(ms) != 2 || hs == "" || len(hs) > 2 {
		return 0, 0, false
	}
	h, errH := strconv.Atoi(hs)
	m, errM := strconv.Atoi(ms)
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// lastSlot is the latest configured clock time at or before now, in loc.
func lastSlot(now time.Time, times []string, loc *time.Location) (time.Time, bool) {
	var best time.Time
	local := now.In(loc)
	for _, day := range []time.Time{local, local.AddDate(0, 0, -1)} {
		for _, v := range times {
			h, m, ok := parseClock(v)
			if !ok {
				continue
			}
			t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
			if !t.After(now) && t.After(best) {
				best = t
			}
		}
	}
	return best, !best.IsZero()
}

// StartTest measures one server in the background; the result lands in the
// history, and in the server's link speed when that is automatic.
func (s *SpeedService) StartTest(nodeID int) error {
	if nodeID != 0 {
		if _, err := (&NodeService{}).GetById(nodeID); err != nil {
			return common.NewErrorf("server %d not found", nodeID)
		}
	}
	if !claimSpeedTest(nodeID) {
		return common.NewError("a speed test is already running on this server")
	}
	go func() {
		defer releaseSpeedTest(nodeID)
		s.runTest(nodeID, SpeedTestManual)
	}()
	return nil
}

// RunScheduled measures, one after another, every server whose last test is
// older than the schedule; servers sharing an uplink would skew each other.
func (s *SpeedService) RunScheduled(now time.Time) {
	settings, err := s.TestSettings()
	if err != nil {
		return
	}
	for _, id := range s.dueForTest(now, settings) {
		if !claimSpeedTest(id) {
			continue
		}
		s.runTest(id, SpeedTestSchedule)
		releaseSpeedTest(id)
	}
}

// dueForTest lists the panel and every enabled online node that is past its
// interval, or has not been tested since the latest clock time began.
func (s *SpeedService) dueForTest(now time.Time, settings SpeedTestSettings) []int {
	every := time.Duration(settings.EveryHours) * time.Hour
	var slot time.Time
	if loc, err := time.LoadLocation(settings.Timezone); err == nil {
		if t, ok := lastSlot(now, settings.Times, loc); ok && now.Sub(t) < speedTestSlotWindow {
			slot = t
		}
	}
	if every <= 0 && slot.IsZero() {
		return nil
	}
	var nodes []*model.Node
	if err := database.GetDB().Where("enable = ? AND status = ?", true, "online").Order("id asc").Find(&nodes).Error; err != nil {
		logger.Warning("speed test: list nodes:", err)
		return nil
	}
	ids := []int{0}
	for _, n := range nodes {
		ids = append(ids, n.Id)
	}
	var last []struct {
		NodeId int
		At     int64
	}
	if err := database.GetDB().Model(&model.SpeedTestResult{}).Select("node_id, MAX(created_at) AS at").Group("node_id").Scan(&last).Error; err != nil {
		logger.Warning("speed test: last runs:", err)
		return nil
	}
	lastAt := map[int]int64{}
	for _, l := range last {
		lastAt[l.NodeId] = l.At
	}
	var out []int
	for _, id := range ids {
		pastInterval := every > 0 && now.UnixMilli()-lastAt[id] >= every.Milliseconds()
		missedSlot := !slot.IsZero() && lastAt[id] < slot.UnixMilli()
		if pastInterval || missedSlot {
			out = append(out, id)
		}
	}
	return out
}

func (s *SpeedService) runTest(nodeID int, trigger string) *model.SpeedTestResult {
	row := &model.SpeedTestResult{NodeId: nodeID, Trigger: trigger}
	if w, err := loadSpeedWorld(); err == nil {
		row.UsedMbps = math.Round(w.used()[nodeID]*10) / 10
	}
	ctx, cancel := context.WithTimeout(context.Background(), speedTestTimeout)
	defer cancel()
	st, err := awaitSpeedTest(ctx, nodeID)
	switch {
	case err != nil:
		row.Error = err.Error()
	case st.Result == nil:
		row.Error = st.Error
	default:
		r := st.Result
		row.Provider, row.Server, row.PingMs, row.DownloadMbps, row.UploadMbps = r.Provider, r.Server, r.PingMs, r.DownloadMbps, r.UploadMbps
	}
	if row.Error == "" {
		if err := s.applyMeasuredCapacity(row); err != nil {
			logger.Warning("speed test: set link speed of", nodeLabel(nodeID), ":", err)
		}
	} else {
		logger.Warning("speed test on", nodeLabel(nodeID), "failed:", row.Error)
	}
	row.CreatedAt = time.Now().UnixMilli()
	db := database.GetDB()
	if err := db.Create(row).Error; err != nil {
		logger.Warning("speed test: save result:", err)
	}
	db.Where("created_at < ?", time.Now().Add(-speedTestKeep).UnixMilli()).Delete(&model.SpeedTestResult{})
	return row
}

// awaitSpeedTest starts (or joins) the server's run and polls until that run ends.
func awaitSpeedTest(ctx context.Context, nodeID int) (speedtest.State, error) {
	started, err := speedTests.start(ctx, nodeID)
	if err != nil {
		return speedtest.State{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return speedtest.State{}, fmt.Errorf("no result within %s", speedTestTimeout)
		case <-time.After(speedTestPoll):
		}
		st, err := speedTests.state(ctx, nodeID)
		if err != nil {
			logger.Debug("speed test: poll", nodeLabel(nodeID), ":", err)
			continue
		}
		if st.Run != started.Run {
			return speedtest.State{}, fmt.Errorf("the server restarted during the test")
		}
		if !st.Running {
			return st, nil
		}
	}
}

// applyMeasuredCapacity sets the link speed to the slower direction plus the
// load users already put on the link while it was measured.
func (s *SpeedService) applyMeasuredCapacity(row *model.SpeedTestResult) error {
	settings, err := s.TestSettings()
	if err != nil || !settings.AutoCapacity {
		return err
	}
	capacity := int(math.Round(min(row.DownloadMbps, row.UploadMbps) + row.UsedMbps))
	if capacity <= 0 {
		return nil
	}
	if err := s.SetCapacity(&ServerCapacityRequest{NodeId: row.NodeId, CapacityMbps: capacity}); err != nil {
		return err
	}
	row.CapacityMbps = capacity
	return nil
}

// TestHistory is a server's results, newest first.
func (s *SpeedService) TestHistory(nodeID, limit int) ([]model.SpeedTestResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []model.SpeedTestResult{}
	err := database.GetDB().Where("node_id = ?", nodeID).Order("created_at desc").Order("id desc").Limit(limit).Find(&out).Error
	return out, err
}

func speedTestStats(now time.Time) ([]SpeedTestStats, error) {
	var rows []model.SpeedTestResult
	since := now.Add(-speedTestStatsWindow).UnixMilli()
	if err := database.GetDB().Where("created_at >= ?", since).Order("created_at asc").Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	byNode := map[int]*SpeedTestStats{}
	var order []int
	for i := range rows {
		r := &rows[i]
		st := byNode[r.NodeId]
		if st == nil {
			st = &SpeedTestStats{NodeId: r.NodeId}
			byNode[r.NodeId] = st
			order = append(order, r.NodeId)
		}
		st.Last = r
		if r.Error != "" {
			continue
		}
		if st.Count == 0 || r.DownloadMbps < st.MinDownloadMbps {
			st.MinDownloadMbps = r.DownloadMbps
		}
		st.MaxDownloadMbps = max(st.MaxDownloadMbps, r.DownloadMbps)
		st.AvgDownloadMbps += r.DownloadMbps
		st.AvgUploadMbps += r.UploadMbps
		st.Count++
	}
	sort.Ints(order)
	out := make([]SpeedTestStats, 0, len(order))
	for _, id := range order {
		st := byNode[id]
		if st.Count > 0 {
			st.AvgDownloadMbps = math.Round(st.AvgDownloadMbps/float64(st.Count)*10) / 10
			st.AvgUploadMbps = math.Round(st.AvgUploadMbps/float64(st.Count)*10) / 10
		}
		out = append(out, *st)
	}
	return out, nil
}
