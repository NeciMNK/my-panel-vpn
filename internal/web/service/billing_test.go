package service

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestPaidExpiry(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC).UnixMilli()
	day := dayMillis
	cases := []struct {
		name    string
		expiry  int64
		billing model.ClientBilling
		days    int
		want    int64
	}{
		{name: "running term is extended from its end", expiry: now + 5*day, days: 30, want: now + 35*day},
		{name: "lapsed term restarts from now", expiry: now - 5*day, days: 30, want: now + 30*day},
		{name: "credit is repaid first", expiry: now + 20*day, billing: model.ClientBilling{GraceDays: 15}, days: 30, want: now + 35*day},
		{name: "countdown not started grows", expiry: -10 * day, days: 30, want: -40 * day},
		{name: "free plan gets a dated term", expiry: 0, billing: model.ClientBilling{FreePlan: true}, days: 90, want: now + 90*day},
		{name: "unlimited paid client stays unlimited", expiry: 0, days: 30, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PaidExpiry(tc.expiry, &tc.billing, tc.days, now); got != tc.want {
				t.Errorf("PaidExpiry = %d, want %d (diff %d days)", got, tc.want, (got-tc.want)/day)
			}
		})
	}
}

func seedPayingClient(t *testing.T, email string, expiry int64) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	settings, _ := json.Marshal(map[string]any{"clients": []map[string]any{{
		"email": email, "id": "ce8d33df-3a64-4f10-8f9b-91c3a8e0c222", "enable": true, "expiryTime": expiry, "subId": "sub-" + email,
	}}})
	ib := &model.Inbound{
		Tag: "pay-in", Enable: true, Port: 50011, Protocol: model.VLESS,
		StreamSettings: `{"network":"tcp","security":"reality"}`, Settings: string(settings),
	}
	db := database.GetDB()
	if err := db.Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	inbounds := InboundService{}
	clients, err := inbounds.GetClients(ib)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&ClientService{}).SyncInbound(nil, ib.Id, clients); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: ib.Id, Email: email, Enable: true, ExpiryTime: expiry}).Error; err != nil {
		t.Fatal(err)
	}
	s := &SettingService{}
	for key, value := range map[string]string{"tgPaymentPrice": "300", "tgPaymentCurrency": "RUB", "tgPaymentDiscounts": "3:10"} {
		if err := s.setString(key, value); err != nil {
			t.Fatal(err)
		}
	}
}

// A payment made outside the bot (API) must also consume the client's pending bot
// report, or an admin tapping "confirm" afterwards extends the same term twice.
func TestApplyPaymentSettlesPendingReportAndBooksIncome(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	due := now.Add(5 * 24 * time.Hour).UnixMilli()
	seedPayingClient(t, "api@x", due)
	clients := &ClientService{}
	billing, err := clients.GetBillingByEmail("api@x")
	if err != nil {
		t.Fatal(err)
	}
	if err := clients.SetPendingPayment(billing.ClientId, 3); err != nil {
		t.Fatal(err)
	}

	result, err := (&BillingService{}).ApplyPayment("api@x", 3, "3 mo", now)
	if err != nil {
		t.Fatalf("ApplyPayment: %v", err)
	}
	wantExpiry := due + 90*dayMillis
	if result.ExpiryTime != wantExpiry || result.Amount != 810 || result.Currency != "RUB" {
		t.Errorf("result = %+v, want expiry %d, 810 RUB", result, wantExpiry)
	}
	traffic, err := (&InboundService{}).GetClientTrafficByEmail("api@x")
	if err != nil || traffic.ExpiryTime != wantExpiry {
		t.Errorf("stored expiry = %v (%v), want %d", traffic, err, wantExpiry)
	}
	if claimed, _ := clients.ClaimPendingPayment(billing.ClientId, 3); claimed {
		t.Error("the bot report was still pending after the payment was applied")
	}
	report, err := (&LedgerService{}).Report(LedgerMonthOf(now))
	if err != nil || len(report.Entries) != 1 || report.Entries[0].Amount != 810 || report.Entries[0].Email != "api@x" {
		t.Errorf("ledger entries = %+v (%v), want one 810 income from api@x", report, err)
	}
}

func TestApplyPaymentRejectsBadInput(t *testing.T) {
	seedPayingClient(t, "api@x", 0)
	cases := []struct {
		name   string
		email  string
		months int
		want   error
	}{
		{name: "zero months", email: "api@x", months: 0, want: ErrPaymentMonths},
		{name: "beyond the longest term", email: "api@x", months: 37, want: ErrPaymentMonths},
		{name: "unknown client", email: "ghost@x", months: 1, want: ErrNoClient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := (&BillingService{}).ApplyPayment(tc.email, tc.months, "", time.Now()); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
