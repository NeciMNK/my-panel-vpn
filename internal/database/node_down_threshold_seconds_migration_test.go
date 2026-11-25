package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The node.down threshold turns from 5-second heartbeats into seconds, held to a minute at least.
func TestNodeDownThresholdSecondsSeederConvertsChecks(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{{"1", "60"}, {"20", "100"}} {
		t.Run(tc.stored, func(t *testing.T) {
			t.Setenv("XUI_DB_FOLDER", t.TempDir())
			if err := InitDB(config.GetDBPath()); err != nil {
				t.Fatalf("init db: %v", err)
			}
			t.Cleanup(func() { _ = CloseDB() })
			if err := db.Where("seeder_name = ?", "NodeDownThresholdSeconds").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
				t.Fatalf("clear seeder history: %v", err)
			}
			if err := db.Create(&model.Setting{Key: "nodeDownThreshold", Value: tc.stored}).Error; err != nil {
				t.Fatalf("store nodeDownThreshold: %v", err)
			}

			if err := runSeeders(false); err != nil {
				t.Fatalf("runSeeders: %v", err)
			}

			var got model.Setting
			if err := db.Where("key = ?", "nodeDownThreshold").First(&got).Error; err != nil {
				t.Fatalf("reload nodeDownThreshold: %v", err)
			}
			if got.Value != tc.want {
				t.Fatalf("nodeDownThreshold = %q, want %q", got.Value, tc.want)
			}
		})
	}
}
