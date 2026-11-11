package tgbot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mymmrac/telego"
)

// The sub server links to the bot by the stored username, so a rename must
// replace it and an unreachable Telegram must not wipe the last known one.
func TestRememberBotUsername(t *testing.T) {
	for _, tc := range []struct {
		name, stored, reported, want string
		telegramDown                 bool
	}{
		{name: "first start", reported: "mnk_vpn_bot", want: "mnk_vpn_bot"},
		{name: "renamed bot", stored: "old_bot", reported: "mnk_vpn_bot", want: "mnk_vpn_bot"},
		{name: "telegram unreachable", stored: "mnk_vpn_bot", telegramDown: true, want: "mnk_vpn_bot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := initReportDB(t)
			if err := tb.settingService.SetTgBotUsername(tc.stored); err != nil {
				t.Fatalf("seed username: %v", err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.telegramDown {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
					"id": 42, "is_bot": true, "first_name": "bot", "username": tc.reported,
				}})
			}))
			defer srv.Close()
			b, err := telego.NewBot(testBotToken, telego.WithAPIServer(srv.URL), telego.WithDiscardLogger())
			if err != nil {
				t.Fatalf("NewBot: %v", err)
			}
			tb.rememberBotUsername(b)
			got, err := tb.settingService.GetTgBotUsername()
			if err != nil || got != tc.want {
				t.Fatalf("stored username = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}
