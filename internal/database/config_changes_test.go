package database_test

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Admin edits must start an on-change backup; traffic and heartbeat writes,
// which land every few seconds, must not.
func TestConfigChangeTracking(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	ib := &model.Inbound{Tag: "in-1", Port: 4431, Protocol: model.VLESS, Enable: true}
	if err := db.Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	node := &model.Node{Name: "n1", Address: "203.0.113.5", Port: 2053, ApiToken: "x", Enable: true}
	if err := db.Create(node).Error; err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		write   func(*gorm.DB) error
		changed bool
	}{
		{"inbound traffic", func(db *gorm.DB) error {
			return db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Updates(map[string]any{"up": gorm.Expr("up + 5"), "down": 7}).Error
		}, false},
		{"node heartbeat", func(db *gorm.DB) error {
			return db.Model(model.Node{}).Where("id = ?", node.Id).Updates(map[string]any{"status": "online", "latency_ms": 12}).Error
		}, false},
		{"status setting", func(db *gorm.DB) error {
			return db.Create(&model.Setting{Key: "subEdgeDnsStatus", Value: "{}"}).Error
		}, false},
		{"client traffic table", func(db *gorm.DB) error {
			return db.Exec("UPDATE client_traffics SET up = up + 1").Error
		}, false},
		{"inbound remark edit", func(db *gorm.DB) error {
			return db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("remark", "renamed").Error
		}, true},
		{"node address edit", func(db *gorm.DB) error {
			return db.Model(model.Node{}).Where("id = ?", node.Id).Updates(map[string]any{"address": "203.0.113.9", "status": "unknown"}).Error
		}, true},
		{"admin setting", func(db *gorm.DB) error {
			return db.Create(&model.Setting{Key: "webPort", Value: "2054"}).Error
		}, true},
		{"inbound delete", func(db *gorm.DB) error {
			return db.Delete(&model.Inbound{}, ib.Id).Error
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := database.ConfigChanges()
			if err := tc.write(db); err != nil {
				t.Fatal(err)
			}
			if after, _ := database.ConfigChanges(); (after != before) != tc.changed {
				t.Fatalf("config change recorded = %v, want %v", after != before, tc.changed)
			}
		})
	}
}
