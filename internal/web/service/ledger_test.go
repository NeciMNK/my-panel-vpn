package service

import (
	"errors"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedLedgerClient(t *testing.T, email, group string, enable bool) model.ClientRecord {
	t.Helper()
	rec := model.ClientRecord{Email: email, SubID: email, Group: group, Enable: true}
	if err := database.GetDB().Create(&rec).Error; err != nil {
		t.Fatalf("seed client %s: %v", email, err)
	}
	if !enable {
		if err := database.GetDB().Model(&rec).Update("enable", false).Error; err != nil {
			t.Fatalf("disable %s: %v", email, err)
		}
	}
	return rec
}

// The month's balance must charge only servers running that month and only that
// month's entries, and keep admin, family and free-plan clients out of the expected income.
func TestLedgerReportMonth(t *testing.T) {
	setupBulkDB(t)
	settings := &SettingService{}
	if err := settings.setString("tgPaymentPrice", "300"); err != nil {
		t.Fatal(err)
	}
	if err := settings.setString("tgPaymentCurrency", "RUB"); err != nil {
		t.Fatal(err)
	}
	ledger := &LedgerService{}
	clients := &ClientService{}

	seedLedgerClient(t, "alice", "", true)
	seedLedgerClient(t, "bob", "friends", true)
	seedLedgerClient(t, "admin", "", true)
	seedLedgerClient(t, "mom", "family", true)
	free := seedLedgerClient(t, "stranger", "", true)
	seedLedgerClient(t, "gone", "", false)
	if err := ledger.SetClientNonPaying("admin", true); err != nil {
		t.Fatalf("SetClientNonPaying: %v", err)
	}
	if err := ledger.SetGroupNonPaying("family", true); err != nil {
		t.Fatalf("SetGroupNonPaying: %v", err)
	}
	if err := clients.SaveBilling(&model.ClientBilling{ClientId: free.Id, FreePlan: true}); err != nil {
		t.Fatal(err)
	}

	for _, server := range []model.LedgerServer{
		{Name: "DE", MonthlyCost: 500, StartMonth: "2026-01"},
		{Name: "NL", MonthlyCost: 400, StartMonth: "2026-10", EndMonth: "2026-11"},
		{Name: "next", MonthlyCost: 900, StartMonth: "2026-12"},
		{Name: "old", MonthlyCost: 700, StartMonth: "2025-01", EndMonth: "2026-10"},
	} {
		if err := ledger.SaveServer(&server); err != nil {
			t.Fatalf("SaveServer %s: %v", server.Name, err)
		}
	}
	nov := time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local)
	if err := ledger.RecordPayment("alice", 300, "1 mo", nov); err != nil {
		t.Fatalf("RecordPayment: %v", err)
	}
	if err := ledger.RecordPayment("bob", 810, "3 mo", nov.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := ledger.AddEntry(&model.LedgerEntry{Month: "2026-11", Kind: LedgerExpense, Amount: 150, Note: "domain"}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := ledger.Report("2026-11")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if got.Income != 300 || got.ServerCosts != 900 || got.OtherExpenses != 150 || got.Expenses != 1050 || got.Balance != -750 {
		t.Errorf("money = income %d, servers %d, other %d, expenses %d, balance %d; want 300, 900, 150, 1050, -750",
			got.Income, got.ServerCosts, got.OtherExpenses, got.Expenses, got.Balance)
	}
	if got.PayingClients != 2 || got.ExpectedIncome != 600 || got.Currency != "RUB" {
		t.Errorf("paying = %d, expected %d %s; want 2, 600 RUB", got.PayingClients, got.ExpectedIncome, got.Currency)
	}
	wantNonPaying := []LedgerNonPaying{
		{Email: "admin", Reason: NonPayingClient},
		{Email: "mom", Reason: NonPayingGroup, Group: "family"},
		{Email: "stranger", Reason: NonPayingFreePlan},
	}
	if len(got.NonPaying) != len(wantNonPaying) {
		t.Fatalf("nonPaying = %+v, want %+v", got.NonPaying, wantNonPaying)
	}
	for i := range wantNonPaying {
		if got.NonPaying[i] != wantNonPaying[i] {
			t.Errorf("nonPaying[%d] = %+v, want %+v", i, got.NonPaying[i], wantNonPaying[i])
		}
	}
}

func TestLedgerRejectsBadInput(t *testing.T) {
	setupBulkDB(t)
	ledger := &LedgerService{}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"server without name", func() error {
			return ledger.SaveServer(&model.LedgerServer{Name: " ", MonthlyCost: 1, StartMonth: "2026-11"})
		}, ErrLedgerName},
		{"server ends before it starts", func() error {
			return ledger.SaveServer(&model.LedgerServer{Name: "x", MonthlyCost: 1, StartMonth: "2026-11", EndMonth: "2026-10"})
		}, ErrLedgerRange},
		{"server month 13", func() error {
			return ledger.SaveServer(&model.LedgerServer{Name: "x", MonthlyCost: 1, StartMonth: "2026-13"})
		}, ErrLedgerMonth},
		{"zero entry", func() error {
			return ledger.AddEntry(&model.LedgerEntry{Month: "2026-11", Kind: LedgerIncome})
		}, ErrLedgerAmount},
		{"unknown kind", func() error {
			return ledger.AddEntry(&model.LedgerEntry{Month: "2026-11", Kind: "refund", Amount: 1})
		}, ErrLedgerKind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Renaming a family group must keep it non-paying; deleting it must drop the mark
// so a new group reusing the name starts as paying.
func TestLedgerFreeGroupFollowsRenameAndDelete(t *testing.T) {
	setupBulkDB(t)
	ledger := &LedgerService{}
	clients := &ClientService{}
	seedLedgerClient(t, "mom", "family", true)
	if err := ledger.SetGroupNonPaying("family", true); err != nil {
		t.Fatal(err)
	}

	if _, err := clients.RenameGroup("family", "relatives"); err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}
	report, err := ledger.Report("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FreeGroups) != 1 || report.FreeGroups[0] != "relatives" || report.PayingClients != 0 {
		t.Fatalf("after rename: free groups %v, paying %d; want [relatives], 0", report.FreeGroups, report.PayingClients)
	}

	if _, err := clients.DeleteGroup("relatives"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	report, err = ledger.Report("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FreeGroups) != 0 || report.PayingClients != 1 {
		t.Errorf("after delete: free groups %v, paying %d; want [], 1", report.FreeGroups, report.PayingClients)
	}
}

// Prices now live on the Finance page: a save must reach what the bot bills by,
// and a bad discount list must be refused before any of the four values change.
func TestLedgerSavePayment(t *testing.T) {
	setupBulkDB(t)
	ledger := &LedgerService{}
	want := LedgerPayment{Price: 300, Currency: "RUB", Discounts: "3:10, 6:15", Details: "Card 2200"}
	if err := ledger.SavePayment(want); err != nil {
		t.Fatalf("SavePayment: %v", err)
	}
	report, err := ledger.Report("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if report.Payment != want {
		t.Errorf("report payment = %+v, want %+v", report.Payment, want)
	}
	if amount, _ := (&BillingService{}).PaymentAmount(3); amount != 810 {
		t.Errorf("bot bills 3 months at %d, want 810", amount)
	}

	for _, bad := range []LedgerPayment{
		{Price: 500, Currency: "USD", Discounts: "3:150"},
		{Price: -1, Currency: "USD"},
	} {
		if err := ledger.SavePayment(bad); err == nil {
			t.Errorf("SavePayment(%+v) accepted", bad)
		}
	}
	report, _ = ledger.Report("2026-11")
	if report.Payment != want {
		t.Errorf("after rejected saves payment = %+v, want %+v unchanged", report.Payment, want)
	}
}
