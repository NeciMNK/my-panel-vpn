package database

import (
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// configTables are the tables an admin edits; the rest is traffic, health and
// cache state that changes every few seconds and must not trigger a backup.
var configTables = map[string]bool{
	"users": true, "inbounds": true, "settings": true, "nodes": true, "api_tokens": true,
	"clients": true, "client_inbounds": true, "client_external_links": true, "client_billings": true,
	"payment_orders": true, "ledger_servers": true, "ledger_entries": true, "ledger_free_groups": true,
	"client_groups": true, "inbound_fallbacks": true, "hosts": true, "outbound_subscriptions": true,
	"sub_balancers": true, "cascades": true, "cascade_fixed_exits": true, "cascade_ai_countries": true,
	"home_networks": true, "warp_switches": true, "client_speeds": true, "group_speeds": true,
	"speed_tiers": true, "tls_certificates": true, "telegram_tags": true, "sub_edges": true,
}

// statusColumns are written by traffic and heartbeat jobs on config tables.
var statusColumns = map[string]map[string]bool{
	"inbounds": {"up": true, "down": true, "all_time": true},
	"nodes": {
		"guid": true, "status": true, "last_heartbeat": true, "latency_ms": true, "xray_version": true,
		"panel_version": true, "cpu_pct": true, "mem_pct": true, "uptime_secs": true,
		"unavailable_metrics": true, "net_up": true, "net_down": true, "last_error": true,
		"xray_state": true, "xray_error": true, "seen_by": true, "config_dirty": true,
		"config_dirty_at": true, "inbounds_adopted_at": true, "down_notified": true, "updated_at": true,
	},
}

// statusSettings are setting keys jobs rewrite on their own.
var statusSettings = map[string]bool{
	"subEdgeDnsStatus": true, "warpLastUpdate": true, "backupStatus": true, "backupHostKey": true,
}

var (
	configChangeSeq atomic.Uint64
	configChangeAt  atomic.Int64
)

// ConfigChanges counts this process's admin configuration writes and reports
// when the last one happened (unix seconds).
func ConfigChanges() (seq uint64, at int64) { return configChangeSeq.Load(), configChangeAt.Load() }

func trackConfigChange(tx *gorm.DB) {
	if tx.Error != nil || tx.Statement == nil || tx.Statement.RowsAffected == 0 {
		return
	}
	if isConfigWrite(tx.Statement.Table, tx.Statement.Dest) {
		configChangeAt.Store(time.Now().Unix())
		configChangeSeq.Add(1)
	}
}

func isConfigWrite(table string, dest any) bool {
	if !configTables[table] {
		return false
	}
	switch d := dest.(type) {
	case *model.Setting:
		return !statusSettings[d.Key]
	case map[string]any:
		noise := statusColumns[table]
		for col := range d {
			if !noise[col] {
				return true
			}
		}
		return len(d) == 0
	}
	return true
}

func registerConfigChangeTracker(conn *gorm.DB) error {
	cb := conn.Callback()
	if err := cb.Create().After("gorm:create").Register("mpv:config_change", trackConfigChange); err != nil {
		return err
	}
	if err := cb.Update().After("gorm:update").Register("mpv:config_change", trackConfigChange); err != nil {
		return err
	}
	return cb.Delete().After("gorm:delete").Register("mpv:config_change", trackConfigChange)
}
