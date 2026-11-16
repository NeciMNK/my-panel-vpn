package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

// Each chosen day reminds exactly once per due date: the old daily report
// repeated the same reminder every day and could not be turned off.
func TestPaymentReminderFiresEachChosenDayOnce(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	var settings SettingService
	if err := settings.setInt("expireDiff", 3); err != nil {
		t.Fatal(err)
	}
	var s PaymentReminderService
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	due := now.Add(5 * 24 * time.Hour)
	at := func(left time.Duration) time.Time { return due.Add(-left) }
	const day = 24 * time.Hour

	step := func(tgID int64, when time.Time, dueAt time.Time, want int) {
		t.Helper()
		got, ok := s.Due(tgID, dueAt, when)
		if want == 0 && ok {
			t.Fatalf("at %v before due: reminder for %d days, want none", dueAt.Sub(when), got)
		}
		if want != 0 {
			if !ok || got != want {
				t.Fatalf("at %v before due: (%d, %v), want %d days", dueAt.Sub(when), got, ok, want)
			}
			if err := s.MarkSent(tgID, dueAt, got); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Never configured: the panel's expireDiff (3 days) is the only reminder.
	step(1, at(4*day), due, 0)
	step(1, at(2*day+12*time.Hour), due, 3)
	step(1, at(2*day), due, 0)
	step(1, at(12*time.Hour), due, 0)
	step(1, at(-time.Hour), due, 0)

	// Chose 3 and 1: one each, and a renewal starts over for the new due date.
	if err := s.SetDays(2, []int{3, 1}); err != nil {
		t.Fatal(err)
	}
	step(2, at(2*day+12*time.Hour), due, 3)
	step(2, at(2*day), due, 0)
	step(2, at(12*time.Hour), due, 1)
	step(2, at(6*time.Hour), due, 0)
	renewed := due.Add(30 * day)
	step(2, renewed.Add(-2*day), renewed, 3)

	// Turned off: nothing at all.
	if err := s.SetDisabled(3, true); err != nil {
		t.Fatal(err)
	}
	step(3, at(12*time.Hour), due, 0)
}
