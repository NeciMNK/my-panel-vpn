package tgbot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/mymmrac/telego"
)

func seedPaymentSettings(t *testing.T, amount, details string) {
	t.Helper()
	for key, value := range map[string]string{"tgPaymentAmount": amount, "tgPaymentDetails": details} {
		if err := database.GetDB().Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatalf("seed setting %s: %v", key, err)
		}
	}
}

func setClientExpiry(t *testing.T, email string, expiry time.Time) {
	t.Helper()
	if err := database.GetDB().Model(&xray.ClientTraffic{}).Where("email = ?", email).
		Update("expiry_time", expiry.UnixMilli()).Error; err != nil {
		t.Fatalf("set expiry for %s: %v", email, err)
	}
}

func screenMarkup(t *testing.T, tb *Tgbot, chatID int64) (body string, markup string) {
	t.Helper()
	sc, ok := tb.screens().get(chatID)
	if !ok {
		t.Fatal("no screen was tracked")
	}
	raw, _ := json.Marshal(sc.markup)
	return sc.body, string(raw)
}

// A client must learn from its own screen when, how much and where to pay;
// the admin's text is HTML-escaped, or Telegram rejects the whole message.
func TestClientHomeShowsWhenHowMuchAndWhereToPay(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 5151, "payer@x")
	due := time.Now().Add(5 * 24 * time.Hour)
	setClientExpiry(t, "payer@x", due)
	seedPaymentSettings(t, "300 RUB / month", "Card 2200 0000 & SBP <phone>")

	tb.screenClientHome(777, 5151)

	body, markup := screenMarkup(t, tb, 777)
	for _, want := range []string{
		due.Format("2006-01-02"),
		"300 RUB / month",
		"Card 2200 0000 &amp; SBP &lt;phone&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("client home lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(markup, `"`+cbClientPaid+`"`) {
		t.Errorf("client home offers no way to report a payment: %s", markup)
	}
}

// A panel that bills nobody must not grow an empty payment block or a dead button.
func TestClientHomeWithoutPaymentSettingsShowsNoPayment(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 5252, "free@x")

	tb.screenClientHome(777, 5252)

	body, markup := screenMarkup(t, tb, 777)
	if strings.Contains(markup, cbClientPaid) {
		t.Errorf("an unbilled client is offered a payment button: %s", markup)
	}
	if strings.Contains(body, "Amount") {
		t.Errorf("an unbilled client sees a payment block:\n%s", body)
	}
}

// An expired subscription is not "due in the past": the client must be told
// payment is due now, with the date it lapsed.
func TestPaymentBlockMarksExpiredSubscriptionOverdue(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 5353, "late@x")
	lapsed := time.Now().Add(-48 * time.Hour)
	setClientExpiry(t, "late@x", lapsed)
	seedPaymentSettings(t, "300", "card")

	tb.screenClientHome(777, 5353)

	body, _ := screenMarkup(t, tb, 777)
	if !strings.Contains(body, "expired on "+lapsed.Format("2006-01-02")) {
		t.Errorf("an expired client is not told payment is overdue:\n%s", body)
	}
}

func tapClientPaid(tb *Tgbot, tgUserID int64) {
	tb.answerCallback(&telego.CallbackQuery{
		ID:      "q-paid",
		From:    telego.User{ID: tgUserID, Username: "payer"},
		Data:    cbClientPaid,
		Message: &telego.Message{Chat: telego.Chat{ID: tgUserID}},
	}, false)
}

func adminMessages(calls []apiCall, adminID int64) []apiCall {
	var out []apiCall
	for _, c := range calls {
		if c.Method != "sendMessage" {
			continue
		}
		if id, ok := c.Payload["chat_id"].(float64); ok && int64(id) == adminID {
			out = append(out, c)
		}
	}
	return out
}

// "I paid" must reach every admin with a one-tap renew for the client's own
// subscription, and a second tap must not spam them again.
func TestClientPaidNotifiesAdminsOnceWithRenewButton(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetPaymentReports(t)
	seedClientForTgUser(t, 5454, "reporter@x")
	seedPaymentSettings(t, "300", "card")

	tapClientPaid(tb, 5454)

	sent := adminMessages(calls(), 9001)
	if len(sent) != 1 {
		t.Fatalf("admin messages = %d, want 1 (calls: %v)", len(sent), methods(calls()))
	}
	if !strings.Contains(sent[0].text(), "reporter@x") || !strings.Contains(sent[0].text(), "@payer") {
		t.Errorf("the admin cannot tell who paid: %q", sent[0].text())
	}
	markup, _ := json.Marshal(sent[0].Payload["reply_markup"])
	if !strings.Contains(string(markup), `"reset_exp reporter@x"`) {
		t.Errorf("the admin notice has no renew button for the client: %s", markup)
	}

	tapClientPaid(tb, 5454)
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin messages after a repeat tap = %d, want 1: the report is not throttled", n)
	}
}

// The reminder a client gets before expiry is the moment it needs to know
// where to pay, so it must carry the payment details and the report button.
func TestExpiryReminderCarriesPaymentDetails(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 5555, "soon@x")
	setClientExpiry(t, "soon@x", time.Now().Add(24*time.Hour))
	seedPaymentSettings(t, "300 RUB", "Card 2200")
	if err := database.GetDB().Create(&model.Setting{Key: "expireDiff", Value: "3"}).Error; err != nil {
		t.Fatal(err)
	}

	tb.notifyExhausted()

	reminders := adminMessages(calls(), 5555)
	if len(reminders) != 1 {
		t.Fatalf("reminders to the client = %d, want 1 (calls: %v)", len(reminders), methods(calls()))
	}
	if text := reminders[0].text(); !strings.Contains(text, "300 RUB") || !strings.Contains(text, "Card 2200") {
		t.Errorf("the expiry reminder does not say how much or where to pay: %q", text)
	}
	markup, _ := json.Marshal(reminders[0].Payload["reply_markup"])
	if !strings.Contains(string(markup), `"`+cbClientPaid+`"`) {
		t.Errorf("the expiry reminder offers no way to report a payment: %s", markup)
	}
}

func resetPaymentReports(t *testing.T) {
	t.Helper()
	paymentReports.mu.Lock()
	paymentReports.last = map[int64]time.Time{}
	paymentReports.mu.Unlock()
}
