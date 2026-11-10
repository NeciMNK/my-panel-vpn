package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// An upgrade used to drop a bot SOCKS proxy outright, so a bot that only worked
// through it went silent; a dialable proxy now becomes the panel proxy link.
func TestLegacyProxySettingsMoveToPanelProxyLink(t *testing.T) {
	tests := []struct {
		name       string
		seed       map[string]string
		wantMode   string
		wantSource string
	}{
		{
			name:       "bot socks proxy",
			seed:       map[string]string{"tgBotProxy": "socks5://u:p@198.51.100.7:1080"},
			wantMode:   "link",
			wantSource: "socks5://u:p@198.51.100.7:1080",
		},
		{
			name:       "panel proxy when the bot had none",
			seed:       map[string]string{"panelProxy": " http://198.51.100.8:3128 "},
			wantMode:   "link",
			wantSource: "http://198.51.100.8:3128",
		},
		{
			name:       "the bot's own proxy wins over the panel one",
			seed:       map[string]string{"tgBotProxy": "socks5://198.51.100.7:1080", "panelProxy": "http://198.51.100.8:3128"},
			wantMode:   "link",
			wantSource: "socks5://198.51.100.7:1080",
		},
		{
			name: "a scheme Xray cannot dial is dropped",
			seed: map[string]string{"tgBotProxy": "socks4://198.51.100.7:1080"},
		},
		{
			name: "a proxy without a port is dropped",
			seed: map[string]string{"tgBotProxy": "socks5://198.51.100.7"},
		},
		{
			name: "a hand-picked panel outbound is left in charge",
			seed: map[string]string{"tgBotProxy": "socks5://198.51.100.7:1080", "panelOutbound": "warp"},
		},
		{
			name:       "an existing quick panel proxy is kept",
			seed:       map[string]string{"tgBotProxy": "socks5://198.51.100.7:1080", "panelProxyMode": "client", "panelProxySource": "alice"},
			wantMode:   "client",
			wantSource: "alice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XUI_DB_FOLDER", t.TempDir())
			if err := InitDB(config.GetDBPath()); err != nil {
				t.Fatalf("init db: %v", err)
			}
			t.Cleanup(func() { _ = CloseDB() })
			if err := db.Where("seeder_name = ?", "LegacyProxySettingsCleanup").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
				t.Fatalf("clear seeder history: %v", err)
			}
			for k, v := range tt.seed {
				if err := db.Where("key = ?", k).Delete(&model.Setting{}).Error; err != nil {
					t.Fatalf("clear %s: %v", k, err)
				}
				if err := db.Create(&model.Setting{Key: k, Value: v}).Error; err != nil {
					t.Fatalf("seed %s: %v", k, err)
				}
			}

			if err := runSeeders(false); err != nil {
				t.Fatalf("runSeeders: %v", err)
			}

			read := func(key string) string {
				var rows []model.Setting
				if err := db.Where("key = ?", key).Find(&rows).Error; err != nil {
					t.Fatalf("read %s: %v", key, err)
				}
				if len(rows) == 0 {
					return ""
				}
				return rows[0].Value
			}
			if got := read("panelProxyMode"); got != tt.wantMode {
				t.Errorf("panelProxyMode = %q, want %q", got, tt.wantMode)
			}
			if got := read("panelProxySource"); got != tt.wantSource {
				t.Errorf("panelProxySource = %q, want %q", got, tt.wantSource)
			}
			for _, legacy := range []string{"tgBotProxy", "panelProxy"} {
				if got := read(legacy); got != "" {
					t.Errorf("%s = %q, want it removed", legacy, got)
				}
			}
		})
	}
}
