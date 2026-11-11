package tgbot

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// seedAIExitWorld: the client's master inbound enters a cascade to DE (2, 3)
// and NL (4); DE node 3 has used up its 100 Mbit/s.
func seedAIExitWorld(t *testing.T) {
	t.Helper()
	rows := []any{
		&model.Node{Id: 2, Name: "de-1", Address: "2.2.2.2", Port: 1, Enable: true, Status: "online", Country: "DE"},
		&model.Node{Id: 3, Name: "de-2", Address: "3.3.3.3", Port: 1, Enable: true, Status: "online", Country: "DE", NetDown: 100e6 / 8},
		&model.Node{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Status: "online", Country: "NL"},
		&model.ServerCapacity{NodeId: 3, CapacityMbps: 100},
		&model.Cascade{Name: "eu", Enable: true, EntryNodeId: 0, Links: []model.CascadeLink{{NodeId: 2}, {NodeId: 3}, {NodeId: 4}}},
	}
	for _, r := range rows {
		if err := database.GetDB().Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func pinnedExits(t *testing.T, email string) []int {
	t.Helper()
	var ids []int
	if err := database.GetDB().Model(&model.CascadeFixedExit{}).Where("email = ?", email).Pluck("node_id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	return ids
}

func TestTrustedClientPinsAnAIExitThroughTheQuiz(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 6060, "ai@x")
	seedAIExitWorld(t)

	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); strings.Contains(markup, `"`+cbAIExit+`"`) {
		t.Fatalf("a client who is no friend and did not pay is offered the AI button: %s", markup)
	}
	tap(tb, 6060, cbAIExit, false)
	if got := pinnedExits(t, "ai@x"); len(got) != 0 {
		t.Fatalf("a client who is no friend pinned %v", got)
	}

	tap(tb, 9001, "friend ai@x", true)
	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"`+cbAIExit+`"`) {
		t.Fatalf("a friend has no AI button: %s", markup)
	}

	tap(tb, 6060, cbAIExit, false)
	tap(tb, 6060, "ai_k *", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("after AI in any country pins = %v, want NL's only server 4 pinned at once", got)
	}
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"ai_y DE"`) || !strings.Contains(markup, `"ai_n DE"`) {
		t.Fatalf("DE has two servers, so the quiz must ask about it: %s", markup)
	}

	tap(tb, 6060, "ai_y DE", false)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"ai_p 2"`) || strings.Contains(markup, `"ai_p 3"`) {
		t.Fatalf("remembered-IP list must offer node 2 and hide full node 3: %s", markup)
	}
	tap(tb, 6060, "ai_p 3", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("a forged tap pinned the full server: %v", got)
	}

	tap(tb, 6060, "ai_n DE", false)
	tap(tb, 6060, "ai_a DE", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{2, 4}) {
		t.Fatalf("auto pick pinned %v, want DE node 2 (node 3 is full) and NL node 4", got)
	}
}

func TestTrustedClientUsesAIOnlyInOneCountry(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 6060, "ai@x")
	seedAIExitWorld(t)
	tap(tb, 9001, "friend ai@x", true)

	tap(tb, 6060, cbAIExit, false)
	if got := pinnedExits(t, "ai@x"); len(got) != 0 {
		t.Fatalf("pins = %v before the client said where they use AI", got)
	}
	_, markup := screenMarkup(t, tb, 6060)
	for _, cb := range []string{`"ai_k DE"`, `"ai_k NL"`, `"ai_k *"`} {
		if !strings.Contains(markup, cb) {
			t.Fatalf("country question lacks %s: %s", cb, markup)
		}
	}

	tap(tb, 6060, "ai_k DE", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("AI only in DE pinned %v, want DE node 2 at once (node 3 is full, NL untouched)", got)
	}
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"ai_y DE"`) {
		t.Fatalf("DE has two servers, so the quiz must still ask: %s", markup)
	}

	tap(tb, 6060, cbAIExit, false)
	body, markup := screenMarkup(t, tb, 6060)
	if strings.Contains(markup, `"ai_k NL"`) || strings.Contains(markup, `"ai_c NL"`) || strings.Contains(body, "NL") {
		t.Fatalf("with AI only in DE the screen still offers NL: %s %s", body, markup)
	}

	tap(tb, 6060, "ai_k *", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{2, 4}) {
		t.Fatalf("any country pins = %v, want NL's only server 4 added", got)
	}
}

// Friends get the AI button free; anyone else buys it once by card, and with
// the price at 0 they never see it.
func TestAIButtonIsFreeForFriendsAndSoldOnceToOthers(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	fake := useBotFakeProvider(t)
	seedBilledClient(t, 6060, "buyer@x")
	seedAIExitWorld(t)
	seedSettings(t, map[string]string{"tgPaymentCurrency": "RUB", "tgAIExitPrice": "0"})

	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); strings.Contains(markup, `"`+cbAIExit) {
		t.Fatalf("at price 0 a client who is no friend sees the AI button: %s", markup)
	}

	setAIExitPrice(t, "50")
	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"`+cbAIExitBuy+`"`) ||
		strings.Contains(markup, `"`+cbAIExit+`"`) || !strings.Contains(markup, "50 RUB") {
		t.Fatalf("an unpaid client must be offered the AI button for 50 RUB only: %s", markup)
	}
	tap(tb, 6060, "ai_k NL", false)
	if got := pinnedExits(t, "buyer@x"); len(got) != 0 {
		t.Fatalf("an unpaid client pinned %v", got)
	}

	tap(tb, 6060, cbAIExitBuy, false)
	if len(fake.orders) != 1 || fake.orders[0].Amount != 50 {
		t.Fatalf("invoices = %+v, want one for 50", fake.orders)
	}
	fake.payAll()
	tb.CheckPaymentOrders(time.Now())
	if !billingOf(t, "buyer@x").AIPaid {
		t.Fatal("the paid order did not unlock the AI button")
	}
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin notices = %d, want 1 about the purchase", n)
	}
	setAIExitPrice(t, "0")
	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"`+cbAIExit+`"`) {
		t.Fatalf("a client who paid lost the AI button when the price went to 0: %s", markup)
	}
}

func setAIExitPrice(t *testing.T, price string) {
	t.Helper()
	if err := database.GetDB().Model(&model.Setting{}).Where("key = ?", "tgAIExitPrice").Update("value", price).Error; err != nil {
		t.Fatal(err)
	}
}
