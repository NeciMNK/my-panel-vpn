package tgbot

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/payment"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
)

type botFakeProvider struct {
	mu       sync.Mutex
	orders   []payment.Order
	payments map[string]payment.Payment
}

func (f *botFakeProvider) Name() string { return "fake" }

func (f *botFakeProvider) Verify(context.Context) (payment.Account, error) {
	return payment.Account{}, nil
}

func (f *botFakeProvider) CreateInvoice(_ context.Context, order payment.Order) (payment.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders = append(f.orders, order)
	id := "inv" + strconv.Itoa(len(f.orders))
	return payment.Invoice{ID: id, URL: "https://pay.example/" + id}, nil
}

func (f *botFakeProvider) GetPayment(_ context.Context, id string) (payment.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.payments[id]; ok {
		return p, nil
	}
	return payment.Payment{Status: payment.StatusPending}, nil
}

func (f *botFakeProvider) payAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, o := range f.orders {
		f.payments["inv"+strconv.Itoa(i+1)] = payment.Payment{
			Status: payment.StatusPaid, AmountMinor: int64(o.Amount) * 100, Currency: o.Currency,
		}
	}
}

func useBotFakeProvider(t *testing.T) *botFakeProvider {
	t.Helper()
	fake := &botFakeProvider{payments: map[string]payment.Payment{}}
	prev := service.NewPaymentProvider
	service.NewPaymentProvider = func(payment.Config) (payment.Provider, error) { return fake, nil }
	t.Cleanup(func() { service.NewPaymentProvider = prev })
	return fake
}

func lastOrder(t *testing.T) model.PaymentOrder {
	t.Helper()
	var order model.PaymentOrder
	if err := database.GetDB().Order("id DESC").First(&order).Error; err != nil {
		t.Fatalf("no payment order: %v", err)
	}
	return order
}

// With a provider set, a friend pays by card at the friends price; the "I paid"
// tap on the invoice extends the term at once, and only the payer may check it.
func TestFriendPaysByCardAndIsExtendedAutomatically(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	fake := useBotFakeProvider(t)
	seedBilledClient(t, 7070, "pal@x")
	due := time.Now().Add(3 * 24 * time.Hour)
	setClientExpiry(t, "pal@x", due)
	seedPaymentSettings(t, "300", "")
	seedSettings(t, map[string]string{"tgPaymentCurrency": "RUB", "tgFriendDiscount": "50"})
	billing := billingOf(t, "pal@x")
	billing.Friend = true
	if err := (&service.ClientService{}).SaveBilling(billing); err != nil {
		t.Fatal(err)
	}

	tap(tb, 7070, cbClientPay, false)
	body, markup := screenMarkup(t, tb, 7070)
	if !strings.Contains(markup, `"`+cbPayOnline+`"`) || strings.Contains(markup, `"`+cbClientPaid+`"`) {
		t.Errorf("payment screen should offer card payment and no manual report without details: %s", markup)
	}
	if !strings.Contains(body, "150 RUB") || !strings.Contains(body, "50%") {
		t.Errorf("payment screen does not show the friends price: %q", body)
	}

	tap(tb, 7070, cbPayOnline, false)
	if _, markup := screenMarkup(t, tb, 7070); !strings.Contains(markup, `"pay_card pal@x 1"`) {
		t.Fatalf("the term picker lacks the 1-month term: %s", markup)
	}
	tap(tb, 7070, "pay_card pal@x 1", false)
	if len(fake.orders) != 1 || fake.orders[0].Amount != 150 {
		t.Fatalf("invoices = %+v, want one for 150", fake.orders)
	}
	order := lastOrder(t)
	if _, markup := screenMarkup(t, tb, 7070); !strings.Contains(markup, "https://pay.example/inv1") ||
		!strings.Contains(markup, `"pay_chk `+strconv.Itoa(order.Id)+`"`) {
		t.Fatalf("the invoice screen lacks the pay link or the check button: %s", markup)
	}

	fake.payAll()
	tap(tb, 7171, "pay_chk "+strconv.Itoa(order.Id), false)
	if got := trafficOf(t, "pal@x").ExpiryTime; got != due.UnixMilli() {
		t.Fatalf("another user's check applied the order: expiry %v", time.UnixMilli(got))
	}
	tap(tb, 7070, "pay_chk "+strconv.Itoa(order.Id), false)
	if got, want := trafficOf(t, "pal@x").ExpiryTime, due.Add(30*24*time.Hour).UnixMilli(); got != want {
		t.Errorf("expiry = %v, want %v", time.UnixMilli(got), time.UnixMilli(want))
	}
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin notices = %d, want 1", n)
	}
	tb.CheckPaymentOrders(time.Now())
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("the job re-announced an applied order: %d admin notices", n)
	}
}

// A stranger buys the free plan by card: no admin approves anything, the job
// sees the payment and the stranger becomes a client with the free quota.
func TestStrangerBuysFreePlanOnline(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	resetThrottles(t)
	fake := useBotFakeProvider(t)
	inbound := model.Inbound{
		UserId: 1, Tag: "free-in", Remark: "free", Port: 8443,
		Protocol: model.VLESS, Enable: true, Settings: `{"clients":[]}`,
	}
	if err := database.GetDB().Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	seedSettings(t, map[string]string{
		"tgFreePlanEnable": "true", "tgFreePlanGB": "4", "tgFreePlanPrice": "5",
		"tgFreePlanInbounds": strconv.Itoa(inbound.Id), "tgPaymentCurrency": "RUB",
	})

	tb.sendStrangerWelcome(8080, 8080)
	welcome := adminMessages(calls(), 8080)
	if len(welcome) != 1 {
		t.Fatalf("welcome messages = %d", len(welcome))
	}
	if markup, _ := json.Marshal(welcome[0].Payload["reply_markup"]); !strings.Contains(string(markup), `"`+cbFreeBuy+`"`) {
		t.Fatalf("the welcome does not sell the free plan: %s", markup)
	}

	_, ok := tb.gateCallback(&telego.CallbackQuery{
		ID: "q", From: telego.User{ID: 8080, Username: "Fresh"}, Data: cbFreeBuy,
		Message: &telego.Message{Chat: telego.Chat{ID: 8080}},
	})
	if ok || len(fake.orders) != 1 || fake.orders[0].Amount != 5 {
		t.Fatalf("free plan invoice = %+v (router reached: %v), want one for 5", fake.orders, ok)
	}
	if n := len(adminMessages(calls(), 9001)); n != 0 {
		t.Errorf("admins were paged before the payment: %d", n)
	}

	fake.payAll()
	tb.CheckPaymentOrders(time.Now())

	_, client, err := tb.inboundService.GetClientByEmail("fresh")
	if err != nil || client.TgID != 8080 || client.TotalGB != 4<<30 {
		t.Fatalf("client = %+v, %v; want tg 8080 with 4 GB", client, err)
	}
	if b := billingOf(t, "fresh"); !b.FreePlan || b.Credit != 5 {
		t.Errorf("billing = %+v, want free plan with 5 credit", b)
	}
	if tb.levelOf(8080) != levelClient {
		t.Error("the buyer is still a stranger")
	}
	if n := len(adminMessages(calls(), 9001)); n != 1 {
		t.Errorf("admin notices = %d, want 1 about the purchase", n)
	}
}
