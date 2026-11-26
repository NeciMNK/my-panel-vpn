package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// An install that saved its Telegram events before node RAM and speed-drop
// alerts existed gets both turned on once; a later choice to drop them sticks.
func TestTgAlertEventsSeederEnablesNewAlertsOnce(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	if err := InitDB(config.GetDBPath()); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	if err := db.Where("seeder_name = ?", "TgNodeLoadSpeedDropEvents").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Setting{Key: "tgEnabledEvents", Value: "login.attempt,cpu.high"}).Error; err != nil {
		t.Fatal(err)
	}
	read := func() string {
		var s model.Setting
		if err := db.Where("key = ?", "tgEnabledEvents").First(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.Value
	}

	if err := runSeeders(false); err != nil {
		t.Fatalf("runSeeders: %v", err)
	}
	if got, want := read(), "login.attempt,cpu.high,memory.high,speed.drop"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if err := db.Model(&model.Setting{}).Where("key = ?", "tgEnabledEvents").Update("value", "login.attempt").Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatalf("runSeeders again: %v", err)
	}
	if got := read(); got != "login.attempt" {
		t.Fatalf("events after the admin removed them = %q, want them to stay removed", got)
	}
}
