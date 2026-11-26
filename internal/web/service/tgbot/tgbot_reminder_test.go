package tgbot

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// The client sets its own reminders from the bot: the days it taps are the
// days it is reminded on, and "off" means no reminder at all.
func TestClientChoosesItsPaymentReminders(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t)
	seedClientForTgUser(t, 6006, "me@x")
	setClientExpiry(t, "me@x", time.Now().Add(6*24*time.Hour+time.Hour))
	var reminders service.PaymentReminderService

	tap(tb, 6006, cbReminders, false)
	if _, markup := screenMarkup(t, tb, 6006); !strings.Contains(markup, `"`+cbReminderDay+`7"`) {
		t.Fatalf("the reminder screen offers no 7-day choice: %s", markup)
	}
	tap(tb, 6006, cbReminderDay+"7", false)
	if prefs, _ := reminders.Prefs(6006); !slices.Equal(prefs.Days, []int{7, 3}) {
		t.Fatalf("days after tapping 7 = %v, want [7 3]", prefs.Days)
	}
	tb.SendPaymentReminders(time.Now())
	if n := reminderCount(calls(), 6006); n != 1 {
		t.Fatalf("reminders 7 days ahead = %d, want 1", n)
	}

	tap(tb, 6006, cbReminderToggle, false)
	if prefs, _ := reminders.Prefs(6006); !prefs.Disabled {
		t.Fatal("reminders still on after tapping the switch")
	}
	setClientExpiry(t, "me@x", time.Now().Add(12*time.Hour))
	tb.SendPaymentReminders(time.Now())
	if n := reminderCount(calls(), 6006); n != 1 {
		t.Fatalf("a client that turned reminders off got %d reminders, want still 1", n)
	}
}

func reminderCount(calls []apiCall, chatID int64) int {
	n := 0
	for _, c := range adminMessages(calls, chatID) {
		if strings.Contains(c.text(), "⏰") {
			n++
		}
	}
	return n
}
