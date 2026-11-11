package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/payment"
)

type fakeProvider struct {
	mu       sync.Mutex
	created  []payment.Order
	payments map[string]payment.Payment
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Verify(context.Context) (payment.Account, error) {
	return payment.Account{}, nil
}

func (f *fakeProvider) CreateInvoice(_ context.Context, order payment.Order) (payment.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, order)
	id := "inv" + strconv.Itoa(len(f.created))
	return payment.Invoice{ID: id, URL: "https://pay.example/" + id}, nil
}

func (f *fakeProvider) GetPayment(_ context.Context, id string) (payment.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.payments[id]; ok {
		return p, nil
	}
	return payment.Payment{Status: payment.StatusPending}, nil
}

func (f *fakeProvider) pay(id string, p payment.Payment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payments[id] = p
}

func useFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	fake := &fakeProvider{payments: map[string]payment.Payment{}}
	prev := NewPaymentProvider
	NewPaymentProvider = func(payment.Config) (payment.Provider, error) { return fake, nil }
	t.Cleanup(func() { NewPaymentProvider = prev })
	return fake
}

func setSettings(t *testing.T, values map[string]string) {
	t.Helper()
	s := &SettingService{}
	for key, value := range values {
		if err := s.setString(key, value); err != nil {
			t.Fatal(err)
		}
	}
}

// The job and the client's "check" button race on the same paid order: the
// term must be extended and booked once, and only one of them may announce it.
func TestPaidPlanOrderIsAppliedOnceUnderConcurrentChecks(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	due := now.Add(48 * time.Hour).UnixMilli()
	seedPayingClient(t, "buyer@x", due)
	fake := useFakeProvider(t)
	orders := &PaymentOrderService{}
	order, err := orders.CreatePlanOrder(context.Background(), 77, "buyer@x", 3, "VPN 3 mo", "https://t.me/bot")
	if err != nil {
		t.Fatalf("CreatePlanOrder: %v", err)
	}
	if order.Amount != 810 || fake.created[0].Amount != 810 || fake.created[0].Currency != "RUB" {
		t.Fatalf("order %+v / invoice %+v, want 810 RUB", order, fake.created[0])
	}
	if out := orders.CheckOrder(context.Background(), order, now); out.Fulfilled {
		t.Fatal("an unpaid order was fulfilled")
	}
	fake.pay(order.InvoiceId, payment.Payment{Status: payment.StatusPaid, AmountMinor: 81000, Currency: "RUB"})

	copies := make([]*model.PaymentOrder, 4)
	for i := range copies {
		if copies[i], err = orders.GetOrder(order.Id); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	fulfilled := make(chan OrderOutcome, len(copies))
	for _, seen := range copies {
		wg.Go(func() {
			if out := orders.CheckOrder(context.Background(), seen, now); out.Fulfilled {
				fulfilled <- out
			}
		})
	}
	wg.Wait()
	close(fulfilled)
	if len(fulfilled) != 1 {
		t.Fatalf("fulfilled %d times, want once", len(fulfilled))
	}
	if out := <-fulfilled; out.Err != nil || out.Result.ExpiryTime != due+90*dayMillis {
		t.Errorf("outcome = %+v, want expiry +90 days", out)
	}
	report, _ := (&LedgerService{}).Report(LedgerMonthOf(now))
	if len(report.Entries) != 1 || report.Entries[0].Amount != 810 {
		t.Errorf("ledger = %+v, want a single 810 entry", report.Entries)
	}
	if got, _ := orders.GetOrder(order.Id); got.Status != OrderDone {
		t.Errorf("status = %s, want done", got.Status)
	}
}

// A provider answer for a different sum (a tampered or edited payment) must not extend anything.
func TestPaidOrderWithWrongAmountIsNotApplied(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	due := now.Add(48 * time.Hour).UnixMilli()
	seedPayingClient(t, "cheap@x", due)
	fake := useFakeProvider(t)
	orders := &PaymentOrderService{}
	order, err := orders.CreatePlanOrder(context.Background(), 78, "cheap@x", 1, "VPN", "https://t.me/bot")
	if err != nil {
		t.Fatal(err)
	}
	fake.pay(order.InvoiceId, payment.Payment{Status: payment.StatusPaid, AmountMinor: 100, Currency: "RUB"})
	out := orders.CheckOrder(context.Background(), order, now)
	if out.Fulfilled || out.Err == nil || order.Status != OrderFailed {
		t.Errorf("outcome = %+v, status %s; want refused and failed", out, order.Status)
	}
	if traffic, _ := (&InboundService{}).GetClientTrafficByEmail("cheap@x"); traffic.ExpiryTime != due {
		t.Errorf("expiry moved to %d", traffic.ExpiryTime)
	}
}

// Paying for the free plan online creates the client with no admin in the loop,
// and the first paid plan is cheaper by exactly what the free plan cost.
func TestPaidFreePlanCreatesClientAndCreditsFirstPlan(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	seedPayingClient(t, "someone@x", 0)
	var inbound model.Inbound
	if err := database.GetDB().Where("tag = ?", "pay-in").First(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	fake := useFakeProvider(t)
	setSettings(t, map[string]string{
		"tgFreePlanPrice": "5", "tgFreePlanGB": "4", "tgFreePlanInbounds": strconv.Itoa(inbound.Id),
	})
	orders := &PaymentOrderService{}
	order, err := orders.CreateFreePlanOrder(context.Background(), 4242, "newbie", "Free 4 GB", "https://t.me/bot")
	if err != nil {
		t.Fatalf("CreateFreePlanOrder: %v", err)
	}
	again, err := orders.CreateFreePlanOrder(context.Background(), 4242, "newbie", "Free 4 GB", "https://t.me/bot")
	if err != nil || again.Id != order.Id || len(fake.created) != 1 {
		t.Errorf("a second tap opened another invoice: %+v, %v (%d invoices)", again, err, len(fake.created))
	}
	fake.pay(order.InvoiceId, payment.Payment{Status: payment.StatusPaid, AmountMinor: 500, Currency: "RUB"})
	out := orders.CheckOrder(context.Background(), order, now)
	if !out.Fulfilled || out.Err != nil || out.Result.Email != "newbie" {
		t.Fatalf("outcome = %+v", out)
	}
	_, client, err := (&InboundService{}).GetClientByEmail("newbie")
	if err != nil || client.TgID != 4242 || client.TotalGB != 4<<30 {
		t.Fatalf("client = %+v, %v; want tg 4242 with 4 GB", client, err)
	}
	billing, _ := (&ClientService{}).GetBillingByEmail("newbie")
	if !billing.FreePlan || billing.Credit != 5 {
		t.Errorf("billing = %+v, want free plan with 5 credit", billing)
	}
	plan, err := orders.CreatePlanOrder(context.Background(), 4242, "newbie", 1, "VPN", "https://t.me/bot")
	if err != nil || plan.Amount != 295 {
		t.Errorf("first plan = %+v, %v; want 295", plan, err)
	}
}

func TestCanceledOrderIsClosed(t *testing.T) {
	seedPayingClient(t, "quit@x", 0)
	fake := useFakeProvider(t)
	orders := &PaymentOrderService{}
	order, err := orders.CreatePlanOrder(context.Background(), 79, "quit@x", 1, "VPN", "https://t.me/bot")
	if err != nil {
		t.Fatal(err)
	}
	fake.pay(order.InvoiceId, payment.Payment{Status: payment.StatusCanceled})
	if out := orders.CheckOrder(context.Background(), order, time.Now()); out.Fulfilled || order.Status != OrderCanceled {
		t.Errorf("outcome %+v, status %s; want canceled", out, order.Status)
	}
	pending, _ := orders.PendingOrders(time.Now())
	if len(pending) != 0 {
		t.Errorf("canceled order still polled: %+v", pending)
	}
}

// The AI button is bought once: the paid order unlocks it (also on a client with a
// saved billing row, which SaveBilling never updates for it) and books the income.
func TestPaidAIExitOrderUnlocksTheButtonAndBooksIt(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	seedPayingClient(t, "ai@x", 0)
	billing, err := (&ClientService{}).GetBillingByEmail("ai@x")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&ClientService{}).SaveBilling(billing); err != nil {
		t.Fatal(err)
	}
	fake := useFakeProvider(t)
	orders := &PaymentOrderService{}
	if _, err = orders.CreateAIExitOrder(context.Background(), 79, "ai@x", "AI", "https://t.me/bot"); !errors.Is(err, ErrNothingToPay) {
		t.Fatalf("at price 0 err = %v, want ErrNothingToPay", err)
	}
	setSettings(t, map[string]string{"tgAIExitPrice": "50"})
	order, err := orders.CreateAIExitOrder(context.Background(), 79, "ai@x", "AI", "https://t.me/bot")
	if err != nil || order.Amount != 50 {
		t.Fatalf("order = %+v, %v; want 50", order, err)
	}
	fake.pay(order.InvoiceId, payment.Payment{Status: payment.StatusPaid, AmountMinor: 5000, Currency: "RUB"})
	if out := orders.CheckOrder(context.Background(), order, now); !out.Fulfilled || out.Err != nil {
		t.Fatalf("outcome = %+v", out)
	}
	if billing, _ := (&ClientService{}).GetBillingByEmail("ai@x"); !billing.AIPaid {
		t.Error("the paid order did not unlock the AI button")
	}
	report, _ := (&LedgerService{}).Report(LedgerMonthOf(now))
	if len(report.Entries) != 1 || report.Entries[0].Amount != 50 || report.Entries[0].Email != "ai@x" {
		t.Errorf("ledger = %+v, want one 50 entry for ai@x", report.Entries)
	}
}
