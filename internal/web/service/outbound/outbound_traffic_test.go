package outbound

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// The upsert's SQL differs per dialect; this pins the SQLite one: rows are
// created, later ticks add onto them, and a counter near the cap stays clamped.
func TestAddTrafficAccumulatesAndClamps(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	if err := db.Create(&model.OutboundTraffics{Tag: "full", Up: database.TrafficMax - 1}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &OutboundService{}
	tick := []*xray.Traffic{
		{IsOutbound: true, Tag: "direct", Up: 5, Down: 7},
		{IsOutbound: true, Tag: "direct", Up: 1, Down: 1},
		{IsOutbound: true, Tag: "full", Up: 10},
		{IsInbound: true, Tag: "inbound-only", Up: 99},
	}
	for range 2 {
		if err, _ := svc.AddTraffic(tick, nil); err != nil {
			t.Fatalf("AddTraffic: %v", err)
		}
	}

	var rows []model.OutboundTraffics
	if err := db.Order("tag").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	want := []model.OutboundTraffics{
		{Tag: "direct", Up: 12, Down: 16, Total: 28},
		{Tag: "full", Up: database.TrafficMax, Total: 20},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		got := rows[i]
		got.Id = 0
		if got != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got, want[i])
		}
	}
}
