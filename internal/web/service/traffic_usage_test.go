package service

import (
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func msk(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("load Europe/Moscow: %v", err)
	}
	return loc
}

func flushUsage(t *testing.T) {
	t.Helper()
	if err := (&TrafficUsageService{}).Flush(); err != nil {
		t.Fatalf("flush usage: %v", err)
	}
}

func hourUsage(t *testing.T, db *gorm.DB, kind, subject string) (up, down int64) {
	t.Helper()
	var rows []model.TrafficUsageHour
	if err := db.Where("kind = ? AND subject = ?", kind, subject).Find(&rows).Error; err != nil {
		t.Fatalf("read hourly usage: %v", err)
	}
	for _, r := range rows {
		up += r.Up
		down += r.Down
	}
	return up, down
}

func TestTrafficUsageFlushAddsToTheHour(t *testing.T) {
	db := initTrafficTestDB(t)
	usageRecorder.take()
	at := time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC)

	first := newTrafficUsageBatch()
	first.addClient("anna", "0", at, 100, 200)
	usageRecorder.merge(first)
	flushUsage(t)
	second := newTrafficUsageBatch()
	second.addClient("anna", "0", at.Add(40*time.Minute), 1, 2)
	usageRecorder.merge(second)
	flushUsage(t)

	var rows []model.TrafficUsageHour
	if err := db.Where("kind = ? AND subject = ?", model.TrafficUsageClient, "anna").Find(&rows).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 1 || rows[0].Up != 101 || rows[0].Down != 202 || rows[0].Bucket != at.Truncate(time.Hour).Unix() {
		t.Fatalf("a second flush into the same hour must add to its row, got %+v", rows)
	}
	if up, down := hourUsage(t, db, model.TrafficUsageNode, "0"); up != 101 || down != 202 {
		t.Fatalf("server usage = %d/%d, want 101/202", up, down)
	}
}

func TestTrafficUsageRollupFoldsWholeOldDaysInPanelTimezone(t *testing.T) {
	db := initTrafficTestDB(t)
	loc := msk(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, loc)
	oldDay := time.Date(2026, 11, 5, 0, 0, 0, 0, loc)
	hours := []model.TrafficUsageHour{
		// 00:00 MSK is still the previous day in UTC: it must land in the MSK day.
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: oldDay.Unix(), Up: 1, Down: 10},
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: oldDay.Add(23 * time.Hour).Unix(), Up: 2, Down: 20},
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: now.AddDate(0, 0, -2).Unix(), Up: 5, Down: 50},
	}
	if err := db.Create(&hours).Error; err != nil {
		t.Fatalf("seed hours: %v", err)
	}
	stale := model.TrafficUsageDay{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: now.AddDate(0, 0, -401).Unix(), Up: 9, Down: 9}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("seed stale day: %v", err)
	}

	if err := (&TrafficUsageService{}).Rollup(now, loc); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var days []model.TrafficUsageDay
	if err := db.Order("bucket").Find(&days).Error; err != nil {
		t.Fatalf("read days: %v", err)
	}
	if len(days) != 1 || days[0].Bucket != oldDay.Unix() || days[0].Up != 3 || days[0].Down != 30 {
		t.Fatalf("days = %+v, want one MSK day %d holding 3/30 and the 401-day-old row gone", days, oldDay.Unix())
	}
	if up, down := hourUsage(t, db, model.TrafficUsageClient, "anna"); up != 5 || down != 50 {
		t.Fatalf("hourly rows left = %d/%d, want only the recent 5/50", up, down)
	}
}

func TestTrafficUsagePeriodStart(t *testing.T) {
	loc := msk(t)
	// Sunday 2026-11-15 23:30 MSK.
	now := time.Date(2026, 11, 15, 23, 30, 0, 0, loc)
	cases := map[string]time.Time{
		"24h":      now.Add(-24 * time.Hour),
		"day":      time.Date(2026, 11, 15, 0, 0, 0, 0, loc),
		"week":     time.Date(2026, 11, 9, 0, 0, 0, 0, loc),
		"month":    time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		"halfyear": time.Date(2026, 7, 1, 0, 0, 0, 0, loc),
		"year":     time.Date(2026, 1, 1, 0, 0, 0, 0, loc),
	}
	for period, want := range cases {
		t.Run(period, func(t *testing.T) {
			got, ok := trafficUsagePeriodStart(period, now, loc)
			if !ok || !got.Equal(want) {
				t.Fatalf("start of %s at %s = %s, want %s", period, now, got, want)
			}
		})
	}
	t.Run("first half of the year", func(t *testing.T) {
		got, _ := trafficUsagePeriodStart("halfyear", time.Date(2026, 6, 30, 23, 0, 0, 0, loc), loc)
		if want := time.Date(2026, 1, 1, 0, 0, 0, 0, loc); !got.Equal(want) {
			t.Fatalf("half-year start in June = %s, want %s", got, want)
		}
	})
	t.Run("week started on Monday itself", func(t *testing.T) {
		got, _ := trafficUsagePeriodStart("week", time.Date(2026, 11, 16, 0, 5, 0, 0, loc), loc)
		if want := time.Date(2026, 11, 16, 0, 0, 0, 0, loc); !got.Equal(want) {
			t.Fatalf("week start on Monday = %s, want %s", got, want)
		}
	})
}

func TestParseTrafficUsagePeriodsKeepsKnownInOrder(t *testing.T) {
	got := ParseTrafficUsagePeriods("year, 24h,bogus,24h,month")
	want := []string{"24h", "month", "year"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestTrafficUsageReportSumsHourlyAndDailyRows(t *testing.T) {
	db := initTrafficTestDB(t)
	loc := msk(t)
	now := time.Date(2026, 11, 16, 12, 30, 0, 0, loc)
	if err := db.Create(&model.Node{Name: "nl-1", Address: "nl.example.com", Port: 2053}).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	var node model.Node
	db.First(&node)
	nodeSubject := strconv.Itoa(node.Id)
	hours := []model.TrafficUsageHour{
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: now.Add(-time.Hour).Truncate(time.Hour).Unix(), Up: 1, Down: 10},
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: now.Add(-30 * time.Hour).Truncate(time.Hour).Unix(), Up: 2, Down: 20},
		{Kind: model.TrafficUsageNode, Subject: nodeSubject, Bucket: now.Add(-time.Hour).Truncate(time.Hour).Unix(), Up: 7, Down: 70},
	}
	days := []model.TrafficUsageDay{
		// October: inside 365d, outside the calendar month.
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: time.Date(2026, 10, 3, 0, 0, 0, 0, loc).Unix(), Up: 4, Down: 40},
		// Older than a year: outside every period.
		{Kind: model.TrafficUsageClient, Subject: "anna", Bucket: time.Date(2025, 10, 3, 0, 0, 0, 0, loc).Unix(), Up: 8, Down: 80},
	}
	if err := db.Create(&hours).Error; err != nil {
		t.Fatalf("seed hours: %v", err)
	}
	if err := db.Create(&days).Error; err != nil {
		t.Fatalf("seed days: %v", err)
	}

	report, err := usageReport(db, []string{"24h", "month", "365d"}, now, loc)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Clients) != 1 || report.Clients[0].Subject != "anna" {
		t.Fatalf("clients = %+v, want only anna", report.Clients)
	}
	want := []TrafficUsageAmount{{Up: 1, Down: 10}, {Up: 3, Down: 30}, {Up: 7, Down: 70}}
	for i, w := range want {
		if got := report.Clients[0].Usage[i]; got != w {
			t.Errorf("anna %s = %+v, want %+v", report.Periods[i], got, w)
		}
	}
	if len(report.Servers) != 1 || report.Servers[0].Name != "nl-1" || report.Servers[0].Usage[0] != (TrafficUsageAmount{Up: 7, Down: 70}) {
		t.Fatalf("servers = %+v, want nl-1 with 7/70 in 24h", report.Servers)
	}
}

func TestLocalTrafficIsRecordedForClientAndPanel(t *testing.T) {
	db := initTrafficTestDB(t)
	usageRecorder.take()
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: "anna", Enable: true}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	if _, _, err := (&InboundService{}).AddTraffic(nil, []*xray.ClientTraffic{{Email: "anna", Up: 300, Down: 400}}); err != nil {
		t.Fatalf("add traffic: %v", err)
	}
	flushUsage(t)

	if up, down := hourUsage(t, db, model.TrafficUsageClient, "anna"); up != 300 || down != 400 {
		t.Fatalf("client usage = %d/%d, want 300/400", up, down)
	}
	if up, down := hourUsage(t, db, model.TrafficUsageNode, localUsageSubject); up != 300 || down != 400 {
		t.Fatalf("panel usage = %d/%d, want 300/400", up, down)
	}
}

func TestNodeTrafficIsRecordedOncePerSync(t *testing.T) {
	db := initTrafficTestDB(t)
	usageRecorder.take()
	createNodeInboundWithClient(t, db, 3, "n3-a", 41101, "anna")
	createNodeInboundWithClient(t, db, 3, "n3-b", 41102, "anna")
	svc := &InboundService{}
	sync := func(up, down int64) {
		t.Helper()
		stat := xray.ClientTraffic{Email: "anna", Up: up, Down: down, Enable: true}
		snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{
			{Tag: "n3-a", Settings: `{"clients":[{"email":"anna","enable":true}]}`, ClientStats: []xray.ClientTraffic{stat}},
			{Tag: "n3-b", Settings: `{"clients":[{"email":"anna","enable":true}]}`, ClientStats: []xray.ClientTraffic{stat}},
		}}
		if _, err := svc.setRemoteTrafficLocked(3, snap, false, false); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	sync(1000, 1000) // baseline: traffic from before the panel saw the node is not history
	sync(1100, 1300)
	flushUsage(t)

	if up, down := hourUsage(t, db, model.TrafficUsageClient, "anna"); up != 100 || down != 300 {
		t.Fatalf("client usage = %d/%d, want 100/300 counted once across both inbounds", up, down)
	}
	if up, down := hourUsage(t, db, model.TrafficUsageNode, "3"); up != 100 || down != 300 {
		t.Fatalf("node usage = %d/%d, want 100/300", up, down)
	}
}
