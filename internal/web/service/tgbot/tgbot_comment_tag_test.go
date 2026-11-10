package tgbot

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
)

func TestTelegramTagFallsBackFromUsername(t *testing.T) {
	cases := []struct {
		name                        string
		id                          int64
		username, first, last, want string
	}{
		{"username wins", 7, "necimnk", "Nikita", "", "@necimnk"},
		{"name when no username", 7, "", "Nikita", "O", "tg:7 (Nikita O)"},
		{"id alone when nameless", 7, "", "", "", "tg:7"},
		{"separators in a name cannot forge the tag's end", 7, "", "a) · b", "", "tg:7 (a b)"},
	}
	for _, c := range cases {
		if got := telegramTag(c.id, c.username, c.first, c.last); got != c.want {
			t.Errorf("%s: telegramTag = %q, want %q", c.name, got, c.want)
		}
	}
}

// The admin's own note must survive every rewrite; only the bot's tag moves.
func TestMergeTelegramTag(t *testing.T) {
	cases := []struct{ name, comment, tag, want string }{
		{"empty comment", "", "@necimnk", "@necimnk"},
		{"admin note is kept", "pays cash", "@necimnk", "@necimnk · pays cash"},
		{"same tag is idempotent", "@necimnk · pays cash", "@necimnk", "@necimnk · pays cash"},
		{"renamed username replaces old", "@oldname · pays cash", "@necimnk", "@necimnk · pays cash"},
		{"username replaces id fallback", "tg:7 (Nikita O) · pays cash", "@necimnk", "@necimnk · pays cash"},
		{"bare old tag replaced", "@oldname", "@necimnk", "@necimnk"},
		{"admin mention without separator is not ours", "@friend invited him", "@necimnk", "@necimnk · @friend invited him"},
	}
	for _, c := range cases {
		if got := mergeTelegramTag(c.comment, c.tag); got != c.want {
			t.Errorf("%s: mergeTelegramTag(%q) = %q, want %q", c.name, c.comment, got, c.want)
		}
	}
}

// newTagTgbot is the links fixture with a VLESS id, which a client write requires.
func newTagTgbot(t *testing.T) *Tgbot {
	t.Helper()
	tb, _ := newLinksCallbackTgbot(t, ownerMail)
	if err := database.GetDB().Model(&model.Inbound{}).Where("1 = 1").Update("settings",
		`{"clients":[{"id":"6f1d2c3e-8a4b-4c5d-9e6f-7a8b9c0d1e2f","email":"`+ownerMail+`","tgId":4242,"subId":"sub-owned"}]}`).Error; err != nil {
		t.Fatalf("give the client an id: %v", err)
	}
	return tb
}

func ownerComment(t *testing.T) (record string, settings string) {
	t.Helper()
	rec, err := (&service.ClientService{}).GetRecordByEmail(nil, ownerMail)
	if err != nil {
		t.Fatalf("load owner record: %v", err)
	}
	var inbound model.Inbound
	if err := database.GetDB().First(&inbound).Error; err != nil {
		t.Fatalf("load inbound: %v", err)
	}
	clients, err := (&service.InboundService{}).GetClients(&inbound)
	if err != nil || len(clients) != 1 {
		t.Fatalf("inbound clients = %v, %v", clients, err)
	}
	return rec.Comment, clients[0].Comment
}

// The tag lands in both the client row and the inbound JSON: a row-only write
// is reverted by the next SyncInbound over the stale settings.
func TestSyncTelegramTagWritesClientComment(t *testing.T) {
	tb := newTagTgbot(t)

	tb.syncTelegramTag(&telego.User{ID: ownerTgID, Username: "oldname"})
	tb.syncTelegramTag(&telego.User{ID: ownerTgID, Username: "necimnk"})

	if rec, js := ownerComment(t); rec != "@necimnk" || js != "@necimnk" {
		t.Errorf("comment after rename = record %q, settings %q; want @necimnk in both", rec, js)
	}

	tb.syncTelegramTag(&telego.User{ID: 999, Username: "stranger"})
	if rec, _ := ownerComment(t); rec != "@necimnk" {
		t.Errorf("another account's sync touched the owner: %q", rec)
	}
}

// Clients bound before this feature never write again, so the bot start looks
// their accounts up instead of waiting for a message.
func TestBackfillTelegramTagsLooksUpBoundAccounts(t *testing.T) {
	tb := newTagTgbot(t)
	mock, calls := staleButtonServer(t, map[string]any{
		"getChat": map[string]any{"ok": true, "result": map[string]any{
			"id": ownerTgID, "type": "private", "username": "necimnk",
		}},
	})
	t.Cleanup(mock.Close)
	swapTestBot(t, mock.URL)

	tb.backfillTelegramTags()

	if n := calls("getChat"); n != 1 {
		t.Errorf("getChat calls = %d, want 1 for the one bound account", n)
	}
	if rec, js := ownerComment(t); rec != "@necimnk" || js != "@necimnk" {
		t.Errorf("comment after backfill = record %q, settings %q; want @necimnk", rec, js)
	}
}
