package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The old 12-hour default moves to hourly on upgrade; an interval the admin chose stays.
func TestSubUpdatesHourlySeederMovesOnlyTheOldDefault(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{{"12", "1"}, {"6", "6"}} {
		t.Run(tc.stored, func(t *testing.T) {
			t.Setenv("XUI_DB_FOLDER", t.TempDir())
			if err := InitDB(config.GetDBPath()); err != nil {
				t.Fatalf("init db: %v", err)
			}
			t.Cleanup(func() { _ = CloseDB() })
			if err := db.Where("seeder_name = ?", "SubUpdatesHourly").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
				t.Fatalf("clear seeder history: %v", err)
			}
			if err := db.Create(&model.Setting{Key: "subUpdates", Value: tc.stored}).Error; err != nil {
				t.Fatalf("store subUpdates: %v", err)
			}

			if err := runSeeders(false); err != nil {
				t.Fatalf("runSeeders: %v", err)
			}

			var got model.Setting
			if err := db.Where("key = ?", "subUpdates").First(&got).Error; err != nil {
				t.Fatalf("reload subUpdates: %v", err)
			}
			if got.Value != tc.want {
				t.Fatalf("subUpdates = %q, want %q", got.Value, tc.want)
			}
		})
	}
}
