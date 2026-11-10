package tgbot

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"github.com/mymmrac/telego"
)

const navAdmin = int64(7701)

func newNavigationBot(t *testing.T) *Tgbot {
	t.Helper()
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, navAdmin)
	screens.reset()
	t.Cleanup(screens.reset)
	return tb
}

// screenButtons is every button on the chat's tracked screen, its callback
// decoded, so a hashed long-email button reads as what it does.
func screenButtons(t *testing.T, tb *Tgbot, chatID int64) []telego.InlineKeyboardButton {
	t.Helper()
	sc, ok := tb.screens().get(chatID)
	if !ok || sc.markup == nil {
		t.Fatal("no screen was tracked")
	}
	var out []telego.InlineKeyboardButton
	for _, row := range sc.markup.InlineKeyboard {
		for _, btn := range row {
			if decoded, err := tb.decodeQuery(btn.CallbackData); err == nil {
				btn.CallbackData = decoded
			}
			out = append(out, btn)
		}
	}
	return out
}

// backTargets lists where each "Back" button on the screen leads.
func backTargets(t *testing.T, tb *Tgbot, chatID int64) []string {
	t.Helper()
	label := tb.I18nBot("tgbot.buttons.back")
	var out []string
	for _, btn := range screenButtons(t, tb, chatID) {
		if btn.Text == label {
			out = append(out, btn.CallbackData)
		}
	}
	return out
}

func buttonFor(t *testing.T, tb *Tgbot, chatID int64, data string) bool {
	t.Helper()
	for _, btn := range screenButtons(t, tb, chatID) {
		if btn.CallbackData == data {
			return true
		}
	}
	return false
}

func manyEmails(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%s-%02d@x", prefix, i))
	}
	return out
}

// "Back" on a sub-screen jumped to the main menu, so an admin three taps deep
// had to walk the whole tree again; it must lead one level up, and only once.
func TestBackLeadsToTheParentScreen(t *testing.T) {
	for _, tc := range []struct {
		open, want string
	}{
		{cbServer, cbCatServer},
		{cbInbounds, cbCatServer},
		{cbOnlines, cbCatServer},
		{cbClients, cbCatClients},
		{cbDeplete, cbCatTraffic},
		{cbReport, cbCatTraffic},
	} {
		t.Run(tc.open, func(t *testing.T) {
			tb := newNavigationBot(t)
			seedReportClients(t, "nav", []string{"nav-a@x"})
			tap(tb, navAdmin, tc.open, true)
			if got := backTargets(t, tb, navAdmin); len(got) != 1 || got[0] != tc.want {
				t.Errorf("back buttons after %q = %q, want exactly one to %q", tc.open, got, tc.want)
			}
		})
	}
}

// With inline mode off in @BotFather the Clients screen offered only an
// "Inline mode is off" button, so no client could be opened at all.
func TestClientsListPagesWithoutInlineMode(t *testing.T) {
	tb := newNavigationBot(t)
	inlineCapability.recordForTest(false)
	t.Cleanup(func() { inlineCapability.recordForTest(false) })
	emails := manyEmails("page", clientListPageSize+3)
	seedReportClients(t, "paged", emails)

	tap(tb, navAdmin, cbClients, true)
	for _, email := range emails[:clientListPageSize] {
		if !buttonFor(t, tb, navAdmin, "client_open "+email) {
			t.Errorf("first page has no button opening %s", email)
		}
	}
	if buttonFor(t, tb, navAdmin, "client_open "+emails[clientListPageSize]) {
		t.Errorf("first page also lists %s, want it on the next page", emails[clientListPageSize])
	}
	if buttonFor(t, tb, navAdmin, "inline_help") {
		t.Error("the list still leads to the inline-mode notice")
	}
	next := clientListCallback(0, 1)
	if !buttonFor(t, tb, navAdmin, next) {
		t.Fatalf("first page has no button to %q", next)
	}

	tap(tb, navAdmin, next, true)
	for _, email := range emails[clientListPageSize:] {
		if !buttonFor(t, tb, navAdmin, "client_open "+email) {
			t.Errorf("second page has no button opening %s", email)
		}
	}
}

// An inbound picked from the list must open its own clients, again without
// inline mode, and its Back must return to the inbound list.
func TestInboundListOpensItsClientsWithoutInlineMode(t *testing.T) {
	tb := newNavigationBot(t)
	inlineCapability.recordForTest(false)
	t.Cleanup(func() { inlineCapability.recordForTest(false) })
	seedReportClients(t, "first", []string{"in-first@x"})
	seedReportClients(t, "second", []string{"in-second@x"})
	var second model.Inbound
	if err := database.GetDB().Where("remark = ?", "second").First(&second).Error; err != nil {
		t.Fatalf("read inbound: %v", err)
	}

	tap(tb, navAdmin, cbInbounds, true)
	open := "get_clients " + strconv.Itoa(second.Id)
	if !buttonFor(t, tb, navAdmin, open) {
		t.Fatalf("inbound list has no button %q", open)
	}
	tap(tb, navAdmin, open, true)
	if !buttonFor(t, tb, navAdmin, "client_open in-second@x") || buttonFor(t, tb, navAdmin, "client_open in-first@x") {
		t.Errorf("inbound %d did not list exactly its own client", second.Id)
	}
	if got := backTargets(t, tb, navAdmin); len(got) != 1 || got[0] != cbInbounds {
		t.Errorf("back from an inbound's clients = %q, want %q", got, cbInbounds)
	}
}

// The client card's Back went to the main menu; it must return to the list
// page the card was opened from, and the card's own sub-screens to the card.
func TestClientCardBackReturnsToItsListPage(t *testing.T) {
	tb := newNavigationBot(t)
	emails := manyEmails("card", clientListPageSize+2)
	seedReportClients(t, "cards", emails)
	email := emails[clientListPageSize+1]

	page := clientListCallback(0, 1)
	tap(tb, navAdmin, page, true)
	tap(tb, navAdmin, "client_open "+email, true)
	if got := backTargets(t, tb, navAdmin); len(got) != 1 || got[0] != page {
		t.Errorf("back from the card = %q, want the list page %q", got, page)
	}

	for _, sub := range []string{"ip_log", "tg_user", "client_sub_links"} {
		tap(tb, navAdmin, sub+" "+email, true)
		if got := backTargets(t, tb, navAdmin); len(got) != 1 || got[0] != "client_open "+email {
			t.Errorf("back from %s = %q, want the card", sub, got)
		}
	}
}
