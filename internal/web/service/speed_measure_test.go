package service

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/speedtest"
)

// scriptedSpeedTests starts run 1 and then reports final for every poll.
type scriptedSpeedTests struct{ final speedtest.State }

func (s scriptedSpeedTests) start(context.Context, int) (speedtest.State, error) {
	return speedtest.State{Run: 1, Running: true}, nil
}

func (s scriptedSpeedTests) state(context.Context, int) (speedtest.State, error) {
	return s.final, nil
}

func useSpeedTests(t *testing.T, final speedtest.State) {
	t.Helper()
	prevTarget, prevPoll := speedTests, speedTestPoll
	speedTests, speedTestPoll = scriptedSpeedTests{final: final}, time.Millisecond
	t.Cleanup(func() { speedTests, speedTestPoll = prevTarget, prevPoll })
}

func capacityOf(t *testing.T, nodeID int) int {
	t.Helper()
	var rows []model.ServerCapacity
	if err := database.GetDB().Where("node_id = ?", nodeID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return 0
	}
	return rows[0].CapacityMbps
}

func TestSpeedTestResultSetsTheLinkSpeed(t *testing.T) {
	measured := speedtest.State{Run: 1, Result: &speedtest.Result{Provider: speedtest.ProviderYandex, Server: "ya", PingMs: 3, DownloadMbps: 900, UploadMbps: 600.4}}
	cases := []struct {
		name         string
		auto         bool
		final        speedtest.State
		wantCapacity int
		wantRow      model.SpeedTestResult
	}{
		{
			name: "automatic: the slower direction plus the load during the test", auto: true, final: measured, wantCapacity: 640,
			wantRow: model.SpeedTestResult{Trigger: SpeedTestManual, Provider: speedtest.ProviderYandex, Server: "ya", PingMs: 3, DownloadMbps: 900, UploadMbps: 600.4, UsedMbps: 40, CapacityMbps: 640},
		},
		{
			name: "manual link speed stays when automatic is off", auto: false, final: measured, wantCapacity: 1000,
			wantRow: model.SpeedTestResult{Trigger: SpeedTestManual, Provider: speedtest.ProviderYandex, Server: "ya", PingMs: 3, DownloadMbps: 900, UploadMbps: 600.4, UsedMbps: 40},
		},
		{
			name: "a failed test keeps the link speed", auto: true, final: speedtest.State{Run: 1, Error: "speedtest: blocked\nyandex: HTTP 403"}, wantCapacity: 1000,
			wantRow: model.SpeedTestResult{Trigger: SpeedTestManual, UsedMbps: 40, Error: "speedtest: blocked\nyandex: HTTP 403"},
		},
		{
			name: "a server that restarted mid-test is a failure", auto: true, final: speedtest.State{Run: 7}, wantCapacity: 1000,
			wantRow: model.SpeedTestResult{Trigger: SpeedTestManual, UsedMbps: 40, Error: "the server restarted during the test"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
			SetLocalThroughputMbps(40)
			t.Cleanup(func() { SetLocalThroughputMbps(0) })
			s := &SpeedService{}
			if err := s.SetCapacity(&ServerCapacityRequest{NodeId: 0, CapacityMbps: 1000}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetTestSettings(&SpeedTestSettings{AutoCapacity: c.auto, Timezone: "UTC"}); err != nil {
				t.Fatal(err)
			}
			useSpeedTests(t, c.final)

			s.runTest(0, SpeedTestManual)

			if got := capacityOf(t, 0); got != c.wantCapacity {
				t.Fatalf("link speed = %d, want %d", got, c.wantCapacity)
			}
			history, err := s.TestHistory(0, 0)
			if err != nil || len(history) != 1 {
				t.Fatalf("history = %+v, %v", history, err)
			}
			got := history[0]
			got.Id, got.CreatedAt = 0, 0
			if got != c.wantRow {
				t.Fatalf("saved %+v\nwant  %+v", got, c.wantRow)
			}
		})
	}
}

func TestScheduledSpeedTestsPickServersNotTestedWithinTheInterval(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	now := time.Now()
	nodes := []*model.Node{
		{Name: "fresh", Address: "a.example", Port: 2053, Enable: true, Status: "online"},
		{Name: "stale", Address: "b.example", Port: 2053, Enable: true, Status: "online"},
		{Name: "down", Address: "c.example", Port: 2053, Enable: true, Status: "offline"},
	}
	for _, n := range nodes {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []model.SpeedTestResult{
		{NodeId: nodes[0].Id, CreatedAt: now.Add(-time.Hour).UnixMilli()},
		{NodeId: nodes[1].Id, CreatedAt: now.Add(-7 * time.Hour).UnixMilli()},
		{NodeId: nodes[1].Id, CreatedAt: now.Add(-9 * time.Hour).UnixMilli()},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	got := (&SpeedService{}).dueForTest(now, SpeedTestSettings{EveryHours: 6})
	if want := []int{0, nodes[1].Id}; !reflect.DeepEqual(got, want) {
		t.Fatalf("due = %v, want %v", got, want)
	}
}

func TestSpeedTestStatsCoverSuccessfulRunsOfTheLastMonth(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	now := time.Now()
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	rows := []model.SpeedTestResult{
		{NodeId: 0, CreatedAt: at(40 * 24 * time.Hour), DownloadMbps: 10, UploadMbps: 10},
		{NodeId: 0, CreatedAt: at(3 * time.Hour), DownloadMbps: 900, UploadMbps: 800},
		{NodeId: 0, CreatedAt: at(2 * time.Hour), DownloadMbps: 600, UploadMbps: 500},
		{NodeId: 0, CreatedAt: at(time.Hour), Error: "blocked"},
		{NodeId: 4, CreatedAt: at(time.Hour), DownloadMbps: 100, UploadMbps: 90},
	}
	for i := range rows {
		if err := database.GetDB().Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := speedTestStats(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Last == nil || got[1].Last == nil {
		t.Fatalf("stats = %+v", got)
	}
	if got[0].Last.Error != "blocked" || got[1].Last.DownloadMbps != 100 {
		t.Fatalf("last runs = %+v / %+v", got[0].Last, got[1].Last)
	}
	got[0].Last, got[1].Last = nil, nil
	want := []SpeedTestStats{
		{NodeId: 0, Count: 2, AvgDownloadMbps: 750, AvgUploadMbps: 650, MinDownloadMbps: 600, MaxDownloadMbps: 900},
		{NodeId: 4, Count: 1, AvgDownloadMbps: 100, AvgUploadMbps: 90, MinDownloadMbps: 100, MaxDownloadMbps: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stats = %+v\nwant    %+v", got, want)
	}
}

func TestSpeedTestTimesFireOnceAtEachClockTimeInTheirZone(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	node := &model.Node{Name: "de", Address: "a.example", Port: 2053, Enable: true, Status: "online"}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatal(err)
	}
	settings := SpeedTestSettings{Times: []string{"03:00", "13:30", "23:43"}, Timezone: "Europe/Moscow"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 11, h, m, 0, 0, msk) }
	s := &SpeedService{}
	cases := []struct {
		name string
		now  time.Time
		want []int
	}{
		{"before any time", at(2, 59), nil},
		{"a minute after 13:30 MSK, every server", at(13, 31), []int{0, node.Id}},
		{"long after a time whose window passed", at(14, 0), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.dueForTest(c.now.UTC(), settings); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("due = %v, want %v", got, c.want)
			}
		})
	}
	if err := database.GetDB().Create(&model.SpeedTestResult{NodeId: 0, CreatedAt: at(13, 32).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	if got, want := s.dueForTest(at(13, 33).UTC(), settings), []int{node.Id}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the panel ran at 13:32, due = %v, want %v", got, want)
	}
}

func TestSpeedTestSettingsNormaliseTimes(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	s := &SpeedService{}
	if err := s.SetTestSettings(&SpeedTestSettings{Times: []string{"19:20", "3:00", " 08:00 ", "3:00"}, Timezone: "Europe/Moscow"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.TestSettings()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"03:00", "08:00", "19:20"}; !reflect.DeepEqual(got.Times, want) || got.Timezone != "Europe/Moscow" {
		t.Fatalf("settings = %+v, want times %v in Europe/Moscow", got, want)
	}
	for _, bad := range []struct {
		req  SpeedTestSettings
		want string
	}{
		{SpeedTestSettings{Times: []string{"24:00"}, Timezone: "UTC"}, `"24:00" is not a time of day (HH:MM)`},
		{SpeedTestSettings{Times: []string{"7:5"}, Timezone: "UTC"}, `"7:5" is not a time of day (HH:MM)`},
		{SpeedTestSettings{Times: []string{"08:00"}, Timezone: "Mars/Olympus"}, `unknown time zone "Mars/Olympus"`},
	} {
		if err := s.SetTestSettings(&bad.req); err == nil || err.Error() != bad.want {
			t.Fatalf("SetTestSettings(%+v) = %v, want %q", bad.req, err, bad.want)
		}
	}
}
