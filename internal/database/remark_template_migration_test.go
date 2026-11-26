package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A panel still on the old default remark template moves to {{AUTO_NAME}}; a
// template the admin wrote is theirs and stays.
func TestSeedAutoNameRemarkTemplate(t *testing.T) {
	cases := map[string]string{
		legacyDefaultRemarkTemplate: "{{AUTO_NAME}}",
		"{{INBOUND}}":               "{{INBOUND}}",
	}
	for stored, want := range cases {
		t.Run(stored, func(t *testing.T) {
			initMigrateDB(t)
			db := GetDB()
			if err := db.Where("seeder_name = ?", "AutoNameRemarkTemplate").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Setting{Key: "remarkTemplate", Value: stored}).Error; err != nil {
				t.Fatal(err)
			}
			if err := seedAutoNameRemarkTemplate(); err != nil {
				t.Fatalf("seed: %v", err)
			}
			var got model.Setting
			if err := db.Where("key = ?", "remarkTemplate").First(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got.Value != want {
				t.Fatalf("remarkTemplate = %q, want %q", got.Value, want)
			}
		})
	}
}
