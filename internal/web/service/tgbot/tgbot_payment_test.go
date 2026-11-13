package tgbot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
)

func seedSettings(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		if err := database.GetDB().Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatalf("seed setting %s: %v", key, err)
		}
	}
}

func seedPaymentSettings(t *testing.T, price, details string) {
	t.Helper()
	seedSettings(t, map[string]string{"tgPaymentPrice": price, "tgPaymentDetails": details})
}

// seedBilledClient is seedClientForTgUser with a VLESS id, which the panel's
// expiry and quota edits require to rewrite the inbound's client entry.
func seedBilledClient(t *testing.T, tgUserID int64, email string) {
	t.Helper()
	seedClientForTgUser(t, tgUserID, email)
	settings := fmt.Sprintf(`{"clients":[{"id":%q,"email":%q,"tgId":%d,"subId":"sub-%s","enable":true}]}`,
		uuid.NewSHA1(uuid.NameSpaceOID, []byte(email)).String(), email, tgUserID, email)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "tg-"+email).
		Update("settings", settings).Error; err != nil {
		t.Fatalf("give %s an id: %v", email, err)
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

// The payment block sat under the subscriptions on the home screen and spilled onto
// a second caption page; it belongs behind the home screen's own "Payment" button.
func TestPayButtonShowsWhenHowMuchAndWhereToPay(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 5151, "payer@x")
	due := time.Now().Add(5 * 24 * time.Hour)
	setClientExpiry(t, "payer@x", due)
	seedPaymentSettings(t, "300", "Card 2200 0000 & SBP <phone>")
	seedSettings(t, map[string]string{"tgPaymentCurrency": "RUB", "tgPaymentDiscounts": "3:10"})

	tb.screenClientHome(5151, 5151)
	home, homeMarkup := screenMarkup(t, tb, 5151)
	if !strings.Contains(homeMarkup, `"`+cbClientPay+`"`) {
		t.Fatalf("client home has no payment button: %s", homeMarkup)
	}
	if strings.Contains(home, "1 mo — 300 RUB") || strings.Contains(home, "Card 2200") {
		t.Errorf("client home still carries the payment block:\n%s", home)
	}
	if strings.Contains(homeMarkup, `"`+cbClientPaid+`"`) {
		t.Errorf("client home reports a payment before the client saw where to pay: %s", homeMarkup)
	}

	tap(tb, 5151, cbClientPay, false)
	body, markup := screenMarkup(t, tb, 5151)
	for _, want := range []string{
		due.Format("2006-01-02"),
		"1 mo — 300 RUB",
		"3 mo — 810 RUB (−10%)",
		"Card 2200 0000 &amp; SBP &lt;phone&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("payment screen lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(markup, `"`+cbClientPaid+`"`) {
		t.Errorf("payment screen offers no way to report a payment: %s", markup)
	}
	if sc, _ := tb.screens().get(5151); sc.pageMax != 1 {
		t.Errorf("payment screen spans %d pages, want 1", sc.pageMax)
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
	if strings.Contains(markup, cbClientPay) || strings.Contains(markup, cbClientPaid) {
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

	tap(tb, 5353, cbClientPay, false)

	body, _ := screenMarkup(t, tb, 5353)
	if !strings.Contains(body, "expired on "+lapsed.Format("2006-01-02")) {
		t.Errorf("an expired client is not told payment is overdue:\n%s", body)
	}
}

func tap(tb *Tgbot, tgUserID int64, data string, isAdmin bool) {
	tb.answerCallback(&telego.CallbackQuery{
		ID:      "q",
		From:    telego.User{ID: tgUserID, Username: "payer"},
		Data:    data,
		Message: &telego.Message{Chat: telego.Chat{ID: tgUserID}},
	}, isAdmin)
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

// "I paid" must ask for the term, then reach every admin with a one-tap
// confirmation for exactly that term; a second report must not spam them.
func TestClientPaidReportsChosenTermToAdmins(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	seedClientForTgUser(t, 5454, "reporter@x")
	seedPaymentSettings(t, "300", "card")
	seedSettings(t, map[string]string{"tgPaymentDiscounts": "3:10"})

	tap(tb, 5454, cbClientPaid, false)
	_, markup := screenMarkup(t, tb, 5454)
	if !strings.Contains(markup, `"client_paid 3"`) {
		t.Fatalf("the term picker does not offer the 3-month term: %s", markup)
	}

	tap(tb, 5454, "client_paid 3", false)
	sent := adminMessages(calls(), 9001)
	if len(sent) != 1 {
		t.Fatalf("admin messages = %d, want 1 (calls: %v)", len(sent), methods(calls()))
	}
	for _, want := range []string{"reporter@x", "@payer", "810"} {
		if !strings.Contains(sent[0].text(), want) {
			t.Errorf("the admin notice lacks %q: %q", want, sent[0].text())
		}
	}
	adminMarkup, _ := json.Marshal(sent[0].Payload["reply_markup"])
	if !strings.Contains(string(adminMarkup), `"pay_ok reporter@x 3"`) {
		t.Errorf("the admin notice has no confirm button for 3 months: %s", adminMarkup)
	}

	tap(tb, 5454, "client_paid 3", false)
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin messages after a repeat report = %d, want 1: the report is not throttled", n)
	}
}

// A term the panel does not sell (a forged or stale button) must not page anyone.
func TestClientPaidRefusesUnknownTerm(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	seedClientForTgUser(t, 5455, "forger@x")
	seedPaymentSettings(t, "300", "card")

	tap(tb, 5455, "client_paid 7", false)

	if n := len(adminMessages(calls(), 9001)); n != 0 {
		t.Errorf("admin messages = %d, want 0 for a term that is not on sale", n)
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
	seedPaymentSettings(t, "300", "Card 2200")
	seedSettings(t, map[string]string{"tgPaymentCurrency": "RUB"})
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

func resetThrottles(t *testing.T) {
	t.Helper()
	for _, th := range []*actionThrottle{paymentReports, freeRequests, invoiceOpens, aiExitReports, aiSuggestions} {
		th.mu.Lock()
		th.last = map[int64]time.Time{}
		th.mu.Unlock()
	}
	paymentNotices.mu.Lock()
	paymentNotices.byEmail = map[string]*paymentReport{}
	paymentNotices.mu.Unlock()
}

func billingOf(t *testing.T, email string) *model.ClientBilling {
	t.Helper()
	billing, err := (&service.ClientService{}).GetBillingByEmail(email)
	if err != nil {
		t.Fatalf("billing for %s: %v", email, err)
	}
	return billing
}

func saveBilling(t *testing.T, billing *model.ClientBilling) {
	t.Helper()
	if err := (&service.ClientService{}).SaveBilling(billing); err != nil {
		t.Fatalf("save billing: %v", err)
	}
}

func trafficOf(t *testing.T, email string) *xray.ClientTraffic {
	t.Helper()
	traffic, err := (&service.InboundService{}).GetClientTrafficByEmail(email)
	if err != nil || traffic == nil {
		t.Fatalf("traffic for %s: %v", email, err)
	}
	return traffic
}

// Confirming a free client's payment must give it the paid term, lift the
// 10 GB quota and tell the client; otherwise "pay to remove the limit" is a lie.
func TestConfirmPaymentLiftsFreePlanAndNotifiesClient(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 5656, "freebie@x")
	freeSettings := fmt.Sprintf(`{"clients":[{"id":%q,"email":"freebie@x","tgId":5656,"subId":"sub-freebie@x","enable":true,"totalGB":%d}]}`,
		uuid.NewSHA1(uuid.NameSpaceOID, []byte("freebie@x")).String(), int64(defaultFreePlanGB)<<30)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "tg-freebie@x").
		Update("settings", freeSettings).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&xray.ClientTraffic{}).Where("email = ?", "freebie@x").
		Update("total", int64(defaultFreePlanGB)<<30).Error; err != nil {
		t.Fatal(err)
	}
	billing := billingOf(t, "freebie@x")
	billing.FreePlan = true
	saveBilling(t, billing)

	resetThrottles(t)
	tap(tb, 5656, "client_paid 1", false)
	before := time.Now()
	tap(tb, 9001, "pay_ok freebie@x 1", true)

	traffic := trafficOf(t, "freebie@x")
	if traffic.Total != 0 {
		t.Errorf("total = %d, want 0: the free quota was not lifted", traffic.Total)
	}
	if lo := before.Add(29 * 24 * time.Hour).UnixMilli(); traffic.ExpiryTime < lo {
		t.Errorf("expiry = %v, want about a month from now", time.UnixMilli(traffic.ExpiryTime))
	}
	if billingOf(t, "freebie@x").FreePlan {
		t.Error("the client is still marked as free after paying")
	}
	if len(adminMessages(calls(), 5656)) != 1 {
		t.Errorf("the client was not told its payment was confirmed (calls: %v)", methods(calls()))
	}
}

// A trusted client may stay connected graceDays on credit; an untrusted one is
// not offered it, and the next payment repays the credit.
func TestTrustedClientDefersPayment(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 5757, "trusted@x")
	seedPaymentSettings(t, "300", "card")
	due := time.Now().Add(2 * 24 * time.Hour).Truncate(time.Second)
	setClientExpiry(t, "trusted@x", due)

	tap(tb, 5757, cbClientPay, false)
	if _, markup := screenMarkup(t, tb, 5757); strings.Contains(markup, cbClientDefer) {
		t.Fatalf("an untrusted client is offered a deferral: %s", markup)
	}

	tap(tb, 9001, "trust trusted@x", true)
	if !billingOf(t, "trusted@x").Trusted {
		t.Fatal("the admin's trust toggle did not mark the client trusted")
	}
	tap(tb, 5757, cbClientPay, false)
	if _, markup := screenMarkup(t, tb, 5757); !strings.Contains(markup, `"`+cbClientDefer+`"`) {
		t.Fatalf("a trusted client near expiry is not offered a deferral: %s", markup)
	}

	tap(tb, 5757, cbClientDefer, false)
	deferred := trafficOf(t, "trusted@x").ExpiryTime
	if want := due.Add(graceDays * 24 * time.Hour).UnixMilli(); deferred != want {
		t.Fatalf("expiry after deferral = %v, want %v", time.UnixMilli(deferred), time.UnixMilli(want))
	}
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin messages = %d, want 1: admins must hear about a deferral", n)
	}

	// Within the cooldown a second deferral is refused even when the end is near again.
	setClientExpiry(t, "trusted@x", time.UnixMilli(deferred).Add(-14*24*time.Hour))
	nearAgain := trafficOf(t, "trusted@x").ExpiryTime
	tap(tb, 5757, cbClientDefer, false)
	if got := trafficOf(t, "trusted@x").ExpiryTime; got != nearAgain {
		t.Fatalf("a second deferral in the same month moved the expiry to %v", time.UnixMilli(got))
	}
	setClientExpiry(t, "trusted@x", time.UnixMilli(deferred))

	resetThrottles(t)
	tap(tb, 5757, "client_paid 1", false)
	tap(tb, 9001, "pay_ok trusted@x 1", true)
	if want := due.Add(service.PaymentMonthDays * 24 * time.Hour).UnixMilli(); trafficOf(t, "trusted@x").ExpiryTime != want {
		t.Errorf("expiry after paying = %v, want %v: the credit was not repaid",
			time.UnixMilli(trafficOf(t, "trusted@x").ExpiryTime), time.UnixMilli(want))
	}
}

// Deferral is limited to once per graceCooldownDays, not by count: a recent one
// blocks the next, an older one does not, and open credit adds up.
func TestDeferralOncePerMonth(t *testing.T) {
	cases := []struct {
		name      string
		lastAgo   time.Duration
		wantGrant bool
	}{
		{name: "deferred 10 days ago", lastAgo: 10 * 24 * time.Hour, wantGrant: false},
		{name: "deferred 31 days ago", lastAgo: 31 * 24 * time.Hour, wantGrant: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb, _ := newScreenTgbot(t, false)
			initReportDB(t)
			localizeWithRealBundle(t)
			setTestAdmins(t, 9001)
			tgID := int64(5858 + i)
			email := fmt.Sprintf("debtor%d@x", i)
			seedBilledClient(t, tgID, email)
			seedPaymentSettings(t, "300", "card")
			nearEnd := time.Now().Add(3 * 24 * time.Hour).Truncate(time.Second)
			setClientExpiry(t, email, nearEnd)
			saveBilling(t, &model.ClientBilling{
				ClientId: billingOf(t, email).ClientId, Trusted: true, GraceDays: graceDays,
				LastGraceAt: time.Now().Add(-tc.lastAgo).UnixMilli(),
			})

			tap(tb, tgID, cbClientDefer, false)

			got := trafficOf(t, email).ExpiryTime
			wantExpiry, wantDebt := nearEnd.UnixMilli(), graceDays
			if tc.wantGrant {
				wantExpiry, wantDebt = nearEnd.Add(graceDays*24*time.Hour).UnixMilli(), 2*graceDays
			}
			if got != wantExpiry {
				t.Errorf("expiry = %v, want %v", time.UnixMilli(got), time.UnixMilli(wantExpiry))
			}
			if debt := billingOf(t, email).GraceDays; debt != wantDebt {
				t.Errorf("credit = %d days, want %d", debt, wantDebt)
			}
		})
	}
}

// A stranger's only reachable button is the free-plan request; it must page
// admins with approve/decline when enabled, and do nothing when the plan is off.
func TestStrangerRequestsFreePlan(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			tb, calls := newScreenTgbot(t, false)
			initReportDB(t)
			localizeWithRealBundle(t)
			setTestAdmins(t, 9001)
			resetThrottles(t)
			seedSettings(t, map[string]string{"tgFreePlanEnable": map[bool]string{false: "false", true: "true"}[enabled]})

			isAdmin, ok := tb.gateCallback(&telego.CallbackQuery{
				ID: "q", From: telego.User{ID: 6060, Username: "Newbie"}, Data: cbFreeRequest,
				Message: &telego.Message{Chat: telego.Chat{ID: 6060}},
			})
			if isAdmin || ok {
				t.Fatalf("gateCallback = (%v, %v), want the stranger kept out of the router", isAdmin, ok)
			}
			sent := adminMessages(calls(), 9001)
			if !enabled {
				if len(sent) != 0 {
					t.Fatalf("admins were paged for a disabled free plan: %v", methods(calls()))
				}
				return
			}
			if len(sent) != 1 {
				t.Fatalf("admin messages = %d, want 1", len(sent))
			}
			markup, _ := json.Marshal(sent[0].Payload["reply_markup"])
			for _, want := range []string{`"free_ok 6060"`, `"free_no 6060"`} {
				if !strings.Contains(string(markup), want) {
					t.Errorf("the request lacks %s: %s", want, markup)
				}
			}
		})
	}
}

// Approving opens the wizard prefilled for the free plan; submitting it must
// create a client with the admin-set quota (not the 10 GB default), reset monthly.
func TestApprovedFreePlanCreatesLimitedClient(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedSettings(t, map[string]string{"tgFreePlanGB": "25"})
	inbound := model.Inbound{
		UserId: 1, Tag: "free-in", Remark: "free", Port: 8443,
		Protocol: model.VLESS, Enable: true, Settings: `{"clients":[]}`,
	}
	if err := database.GetDB().Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	freeRequestNames.Lock()
	freeRequestNames.byID[6161] = "New.User"
	freeRequestNames.Unlock()

	tap(tb, 9001, "free_ok 6161", true)
	actor := chatUser{chatID: 9001, userID: 9001}
	draft := addClientDrafts.forActor(actor)
	draft.Lock()
	if draft.email != "new.user" || draft.tgID != "6161" || !draft.freePlan {
		t.Fatalf("draft = email %q tg %q free %v, want new.user / 6161 / true", draft.email, draft.tgID, draft.freePlan)
	}
	draft.receiverInboundIDs = []int{inbound.Id}
	draft.Unlock()

	tap(tb, 9001, "add_client_submit_enable", true)

	traffic := trafficOf(t, "new.user")
	if traffic.Total != 25<<30 {
		t.Errorf("total = %d, want 25 GB", traffic.Total)
	}
	rec, err := (&service.ClientService{}).GetRecordByEmail(nil, "new.user")
	if err != nil {
		t.Fatal(err)
	}
	if rec.TrafficReset != "monthly" {
		t.Errorf("traffic reset = %q, want monthly", rec.TrafficReset)
	}
	if !billingOf(t, "new.user").FreePlan {
		t.Error("the new client is not marked as free, so a payment could not lift its quota")
	}
	greeted := false
	for _, msg := range adminMessages(calls(), 6161) {
		text, _ := msg.Payload["text"].(string)
		greeted = greeted || strings.Contains(text, "25")
	}
	if !greeted {
		t.Errorf("the approved user was not told about the 25 GB quota (calls: %v)", methods(calls()))
	}
}

// Every admin gets a confirm button per report: a second tap, by the same admin
// or another one, must not extend the term again.
func TestConfirmPaymentAppliesOnceForOneReport(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001, 9002)
	resetThrottles(t)
	seedBilledClient(t, 5757, "twice@x")
	seedPaymentSettings(t, "300", "card")
	due := time.Now().Add(5 * 24 * time.Hour)
	setClientExpiry(t, "twice@x", due)

	tap(tb, 5757, "client_paid 1", false)
	tap(tb, 9001, "pay_ok twice@x 1", true)
	once := trafficOf(t, "twice@x").ExpiryTime
	if want := due.Add(service.PaymentMonthDays * 24 * time.Hour).UnixMilli(); once != want {
		t.Fatalf("expiry after the first confirm = %v, want %v", time.UnixMilli(once), time.UnixMilli(want))
	}
	tap(tb, 9002, "pay_ok twice@x 1", true)
	if got := trafficOf(t, "twice@x").ExpiryTime; got != once {
		t.Errorf("expiry after a second confirm = %v, want %v unchanged", time.UnixMilli(got), time.UnixMilli(once))
	}
	if n := len(adminMessages(calls(), 5757)); n != 1 {
		t.Errorf("client confirmations = %d, want 1", n)
	}
}

// Every admin's copy kept its live "Confirm" button after one admin confirmed,
// so the report read as unhandled; all copies must say who confirmed and drop it.
func TestConfirmPaymentClosesEveryAdminsNotice(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001, 9002)
	resetThrottles(t)
	seedBilledClient(t, 5858, "closed@x")
	seedPaymentSettings(t, "300", "card")
	setClientExpiry(t, "closed@x", time.Now().Add(5*24*time.Hour))

	tap(tb, 5858, "client_paid 1", false)
	notices := map[int64]bool{}
	for _, admin := range []int64{9001, 9002} {
		if len(adminMessages(calls(), admin)) != 1 {
			t.Fatalf("admin %d got %d reports, want 1", admin, len(adminMessages(calls(), admin)))
		}
		notices[admin] = true
	}
	tb.answerCallback(&telego.CallbackQuery{
		ID: "q", From: telego.User{ID: 9001, Username: "boss"}, Data: "pay_ok closed@x 1",
		Message: &telego.Message{Chat: telego.Chat{ID: 9001}},
	}, true)

	until := time.UnixMilli(trafficOf(t, "closed@x").ExpiryTime).Format(paymentDate)
	edited := map[int64]bool{}
	for _, c := range calls() {
		if c.Method != "editMessageText" {
			continue
		}
		chat := int64(c.Payload["chat_id"].(float64))
		if !notices[chat] || !strings.Contains(c.text(), "@boss") || !strings.Contains(c.text(), until) {
			continue
		}
		if strings.Contains(fmt.Sprint(c.Payload["reply_markup"]), "pay_ok") {
			t.Errorf("admin %d's notice still offers a confirm button: %v", chat, c.Payload["reply_markup"])
		}
		edited[chat] = true
	}
	if !edited[9001] || !edited[9002] {
		t.Errorf("notices rewritten with the confirmer and new expiry = %v, want both admins", edited)
	}

	tap(tb, 9002, "pay_ok closed@x 1", true)
	last := ""
	for _, c := range calls() {
		if c.Method == "answerCallbackQuery" {
			last, _ = c.Payload["text"].(string)
		}
	}
	if want := tb.I18nBot("tgbot.answers.paymentAlreadyConfirmed", "Email==closed@x"); last != want {
		t.Errorf("repeat confirm answered %q, want %q", last, want)
	}
}

// A notice sent before a panel restart is not tracked, so its button stayed live
// after the payment was settled; a tap must at least strip that button.
func TestRepeatConfirmOnAStaleNoticeStripsItsButton(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	seedBilledClient(t, 5959, "stale@x")
	seedPaymentSettings(t, "300", "card")

	tb.answerCallback(&telego.CallbackQuery{
		ID: "q", From: telego.User{ID: 9001}, Data: "pay_ok stale@x 1",
		Message: &telego.Message{Chat: telego.Chat{ID: 9001}, MessageID: 77},
	}, true)
	for _, c := range calls() {
		if c.Method == "editMessageReplyMarkup" && c.Payload["message_id"] == float64(77) {
			if strings.Contains(fmt.Sprint(c.Payload["reply_markup"]), "pay_ok") {
				t.Errorf("stale notice kept its confirm button: %v", c.Payload["reply_markup"])
			}
			return
		}
	}
	t.Error("the stale notice's confirm button was left untouched")
}

// The admin's report had only "+N mo" and "Hide": a transfer that never arrived could
// not be refused, so the client never learnt and its report stayed confirmable.
func TestPaymentNotReceivedTellsClientAndClosesReport(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001, 9002)
	resetThrottles(t)
	seedBilledClient(t, 6262, "nomoney@x")
	seedPaymentSettings(t, "300", "card")
	due := time.Now().Add(5 * 24 * time.Hour)
	setClientExpiry(t, "nomoney@x", due)

	tap(tb, 6262, "client_paid 1", false)
	sent := adminMessages(calls(), 9001)
	if len(sent) != 1 {
		t.Fatalf("admin reports = %d, want 1", len(sent))
	}
	reportMarkup, _ := json.Marshal(sent[0].Payload["reply_markup"])
	if !strings.Contains(string(reportMarkup), `"pay_no nomoney@x 1"`) {
		t.Fatalf("the admin report has no 'not received' button: %s", reportMarkup)
	}

	tb.answerCallback(&telego.CallbackQuery{
		ID: "q", From: telego.User{ID: 9001, Username: "boss"}, Data: "pay_no nomoney@x 1",
		Message: &telego.Message{Chat: telego.Chat{ID: 9001}},
	}, true)

	told := adminMessages(calls(), 6262)
	if len(told) != 1 || !strings.Contains(told[0].text(), tb.I18nBot("tgbot.messages.paymentNotReceived", "Months==1")) {
		t.Fatalf("client messages = %v, want one 'payment not received' notice", told)
	}
	edited := map[int64]bool{}
	for _, c := range calls() {
		if c.Method != "editMessageText" || !strings.Contains(c.text(), "@boss") {
			continue
		}
		if markup := fmt.Sprint(c.Payload["reply_markup"]); strings.Contains(markup, "pay_ok") || strings.Contains(markup, "pay_no") {
			t.Errorf("a refused report still offers its buttons: %v", markup)
		}
		edited[int64(c.Payload["chat_id"].(float64))] = true
	}
	if !edited[9001] || !edited[9002] {
		t.Errorf("notices rewritten with who refused = %v, want both admins", edited)
	}

	tap(tb, 9002, "pay_ok nomoney@x 1", true)
	if got := trafficOf(t, "nomoney@x").ExpiryTime; got != due.UnixMilli() {
		t.Errorf("a refused report was still confirmed: expiry %v, want %v", time.UnixMilli(got), due)
	}

	tap(tb, 6262, "client_paid 1", false)
	if n := len(adminMessages(calls(), 9001)); n != 2 {
		t.Errorf("admin reports after the client reported again = %d, want 2: the refusal kept the throttle", n)
	}
}
