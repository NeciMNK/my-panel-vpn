package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// A database that briefly drops out (a Patroni primary missing one dial) used
// to leave "config refused" on the status page until the next core restart.
func TestHeldBackClearsOnceTheRunningCoreServesTheDesiredConfig(t *testing.T) {
	cases := []struct {
		name      string
		afterDrop func(t *testing.T)
	}{
		{name: "config unchanged", afterDrop: func(*testing.T) {}},
		{name: "change applied through the core API", afterDrop: func(t *testing.T) {
			inbound := &model.Inbound{Enable: true, Protocol: model.Mixed, Port: freeLoopbackPort(t), Listen: "127.0.0.1", Tag: "inbound-held-back", Settings: `{"auth":"noauth","udp":false}`, StreamSettings: `{"network":"tcp","security":"none"}`}
			if err := database.GetDB().Create(inbound).Error; err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			portConflictLoggerOnce.Do(func() { xuilogger.InitLogger(logging.ERROR) })
			dbDir := t.TempDir()
			t.Setenv("XUI_DB_FOLDER", dbDir)
			dbPath := filepath.Join(dbDir, "x-ui.db")
			dbtest.InitDB(t, dbPath)
			seedTemplateWithoutGeoRules(t)
			startBalancerTestXray(t, balancerTestConfig{outbounds: allBalancerTestOutbounds})
			isManuallyStopped.Store(false)
			isNeedXrayRestart.Store(false)
			svc := &XrayService{}
			if err := svc.RestartXray(false); err != nil {
				t.Fatalf("sync core with the database: %v", err)
			}
			process := currentXrayProcess()
			t.Cleanup(func() { _ = process.Stop() })

			if err := database.CloseDB(); err != nil {
				t.Fatal(err)
			}
			if err := svc.RestartXray(false); err == nil {
				t.Fatal("restart with the database gone succeeded")
			}
			if svc.GetHeldBackConfig() == "" {
				t.Fatal("database outage did not reach the status page")
			}

			if err := database.InitDB(dbPath); err != nil {
				t.Fatal(err)
			}
			tc.afterDrop(t)
			if err := svc.RestartXray(false); err != nil {
				t.Fatalf("restart after the database returned: %v", err)
			}
			if currentXrayProcess() != process {
				t.Fatal("recovery restarted the core instead of keeping it")
			}
			if reason := svc.GetHeldBackConfig(); reason != "" {
				t.Fatalf("status still reports %q after the core caught up", reason)
			}
		})
	}
}

// The default template routes geoip:private, which needs a geoip.dat the
// e2e core binary does not ship with.
func seedTemplateWithoutGeoRules(t *testing.T) {
	t.Helper()
	var template map[string]any
	if err := json.Unmarshal([]byte(xrayTemplateConfig), &template); err != nil {
		t.Fatal(err)
	}
	routing, _ := template["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	kept := []any{}
	for _, rule := range rules {
		raw, _ := json.Marshal(rule)
		if !strings.Contains(string(raw), "geoip:") && !strings.Contains(string(raw), "geosite:") {
			kept = append(kept, rule)
		}
	}
	routing["rules"] = kept
	template["outbounds"] = []any{
		map[string]any{"protocol": "freedom", "tag": "direct"},
		map[string]any{"protocol": "blackhole", "tag": "blocked"},
	}
	raw, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", string(raw)); err != nil {
		t.Fatal(err)
	}
}
