package tgbot

import (
	"reflect"
	"strings"
	"testing"
)

// An admin who is also a client walks their own AI quiz; their admin branch
// used to answer every "<action> <arg>" it did not know with errorOperation.
func TestAdminPinsOwnAIExitLikeAClient(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 9001, "admin@x")
	seedAIExitWorld(t)
	tap(tb, 9001, "friend admin@x", true)

	tap(tb, 9001, cbAIExit, true)
	tap(tb, 9001, "ai_k DE", true)
	if got := pinnedExits(t, "admin@x"); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("admin's AI only in DE pinned %v, want DE node 2", got)
	}

	tap(tb, 9001, "ai_p 3", true)
	if got := pinnedExits(t, "admin@x"); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("an admin's own quiz pinned the full server 3: %v", got)
	}
}

// "I paid → N months" from an admin's own subscription reaches the admins.
func TestAdminReportsOwnPaymentLikeAClient(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001, 9002)
	resetThrottles(t)
	seedClientForTgUser(t, 9001, "admin@x")
	seedPaymentSettings(t, "300", "card")
	seedSettings(t, map[string]string{"tgPaymentDiscounts": "3:10"})

	tap(tb, 9001, "client_paid 3", true)
	sent := adminMessages(calls(), 9002)
	if len(sent) != 1 || !strings.Contains(sent[0].text(), "admin@x") {
		t.Fatalf("the other admin got %d notices, want 1 naming admin@x (calls: %v)", len(sent), calls())
	}
}
