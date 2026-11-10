package tgbot

import (
	"errors"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
)

func TestParseLedgerServer(t *testing.T) {
	cases := []struct {
		text    string
		want    model.LedgerServer
		wantErr error
	}{
		{"Hetzner DE 500", model.LedgerServer{Name: "Hetzner DE", MonthlyCost: 500, StartMonth: "2026-11"}, nil},
		{"Hetzner DE 500 2026-09", model.LedgerServer{Name: "Hetzner DE", MonthlyCost: 500, StartMonth: "2026-09"}, nil},
		{"500", model.LedgerServer{}, errLedgerInput},
		{"Hetzner cheap", model.LedgerServer{}, errLedgerInput},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got, err := parseLedgerServer(tc.text, "2026-11")
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Errorf("parseLedgerServer(%q) = %+v, %v; want %+v, %v", tc.text, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func ledgerReport(t *testing.T, month string) *service.LedgerReport {
	t.Helper()
	report, err := (&service.LedgerService{}).Report(month)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return report
}

func adminTypes(tb *Tgbot, adminID int64, text string) {
	actor := chatUser{chatID: adminID, userID: adminID}
	state, _ := userStateMgr.get(actor)
	kind, month, ok := parseLedgerState(state)
	if !ok {
		return
	}
	tb.handleLedgerInput(&telego.Message{
		Text: text, From: &telego.User{ID: adminID}, Chat: telego.Chat{ID: adminID},
	}, actor, kind, month)
}

// A confirmed payment must land in the ledger as that term's discounted total,
// or the month's profit ignores the money that actually came in.
func TestConfirmPaymentBooksIncome(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedSettings(t, map[string]string{"tgPaymentPrice": "300", "tgPaymentDiscounts": "3:10"})
	seedBilledClient(t, 5757, "payer@x")

	tap(tb, 9001, "pay_ok payer@x 3", true)

	report := ledgerReport(t, currentLedgerMonth())
	if len(report.Entries) != 1 {
		t.Fatalf("entries = %+v, want one booked payment", report.Entries)
	}
	got := report.Entries[0]
	if got.Kind != service.LedgerIncome || got.Amount != 810 || got.Email != "payer@x" {
		t.Errorf("entry = %+v, want income 810 from payer@x", got)
	}
}

// The admin's typed lines must become a server cost and an income tied to the
// client; a line that does not parse must keep the prompt open instead of booking zero.
func TestAdminBooksLedgerFromTheBot(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 5858, "alice")

	tap(tb, 9001, "ledger_add server 2026-11", true)
	adminTypes(tb, 9001, "Hetzner DE 500")
	tap(tb, 9001, "ledger_add income 2026-11", true)
	adminTypes(tb, 9001, "lots of money")
	if state, _ := userStateMgr.get(chatUser{chatID: 9001, userID: 9001}); state != ledgerState(ledgerAddIncome, "2026-11") {
		t.Fatalf("after bad input state = %q, want the income prompt still open", state)
	}
	adminTypes(tb, 9001, "300 alice for November")

	report := ledgerReport(t, "2026-11")
	if report.ServerCosts != 500 || report.Income != 300 || report.Balance != -200 {
		t.Errorf("servers %d, income %d, balance %d; want 500, 300, -200", report.ServerCosts, report.Income, report.Balance)
	}
	if len(report.Entries) != 1 || report.Entries[0].Email != "alice" || report.Entries[0].Note != "for November" {
		t.Errorf("entries = %+v, want one from alice noted \"for November\"", report.Entries)
	}
	body, _ := screenMarkup(t, tb, 9001)
	if !strings.Contains(body, "-200") {
		t.Errorf("ledger screen does not show the balance -200:\n%s", body)
	}
}

// Tapping a group twice must mark it non-paying and then paying again; the
// name is everything after the month, so a group with spaces survives the split.
func TestAdminTogglesNonPayingGroup(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	if err := database.GetDB().Create(&model.ClientGroup{Name: "my family"}).Error; err != nil {
		t.Fatal(err)
	}

	tap(tb, 9001, "ledger_grp 2026-11 my family", true)
	if got := ledgerReport(t, "2026-11").FreeGroups; len(got) != 1 || got[0] != "my family" {
		t.Fatalf("free groups = %v, want [my family]", got)
	}
	tap(tb, 9001, "ledger_grp 2026-11 my family", true)
	if got := ledgerReport(t, "2026-11").FreeGroups; len(got) != 0 {
		t.Errorf("free groups = %v, want none after the second tap", got)
	}
}
