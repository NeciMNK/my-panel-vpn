package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A client created without a group joined none, so the clients list had no way to
// tell regular users from the panel's own cascade/home links.
func TestNewClientsJoinTheirDefaultGroup(t *testing.T) {
	setupBulkDB(t)
	clients := []model.Client{
		{Email: "alice", ID: "11111111-1111-1111-1111-111111111111", SubID: "alice", Enable: true},
		{Email: "mp-cascade-1-n1", ID: "22222222-2222-2222-2222-222222222222", SubID: "mp1", Enable: true},
		{Email: "carol", ID: "33333333-3333-3333-3333-333333333333", SubID: "carol", Enable: true, Group: "family"},
	}
	ib := mkInbound(t, 52201, model.VLESS, clientsSettings(t, clients))
	if err := (&ClientService{}).SyncInbound(nil, ib.Id, clients); err != nil {
		t.Fatalf("SyncInbound: %v", err)
	}
	for email, want := range map[string]string{"alice": "users", "mp-cascade-1-n1": "tech", "carol": "family"} {
		if got := lookupClientRecord(t, email).Group; got != want {
			t.Errorf("%s group = %q, want %q", email, got, want)
		}
	}
}

// The admin may take an existing client out of every group; a later sync of the
// group-less inbound settings must not put it back into "users".
func TestDefaultGroupOnlyForNewClients(t *testing.T) {
	setupBulkDB(t)
	c := model.Client{Email: "dave", ID: "44444444-4444-4444-4444-444444444444", SubID: "dave", Enable: true}
	ib := mkInbound(t, 52202, model.VLESS, clientsSettings(t, []model.Client{c}))
	svc := &ClientService{}
	if err := svc.SyncInbound(nil, ib.Id, []model.Client{c}); err != nil {
		t.Fatalf("SyncInbound: %v", err)
	}
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "dave").
		UpdateColumn("group_name", "").Error; err != nil {
		t.Fatalf("clear group: %v", err)
	}
	if err := svc.SyncInbound(nil, ib.Id, []model.Client{c}); err != nil {
		t.Fatalf("second SyncInbound: %v", err)
	}
	if got := lookupClientRecord(t, "dave").Group; got != "" {
		t.Fatalf("group after resync = %q, want it left empty", got)
	}
}

// Marking a client as a friend in the bot only changed the price; the group has to
// follow, and unmarking returns a friend to "users" but never moves a custom group.
func TestSetFriendMovesTheClientGroup(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	seedBillingClient(t, "erin", 52203)

	steps := []struct {
		name       string
		startGroup string
		friend     bool
		wantGroup  string
	}{
		{"mark friend", "users", true, "friends"},
		{"unmark friend", "friends", false, "users"},
		{"mark from custom group", "family", true, "friends"},
		{"unmark keeps a group set by hand", "family", false, "family"},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "erin").
				UpdateColumn("group_name", st.startGroup).Error; err != nil {
				t.Fatalf("set start group: %v", err)
			}
			if err := svc.SetFriend("erin", st.friend); err != nil {
				t.Fatalf("SetFriend: %v", err)
			}
			billing, err := svc.GetBillingByEmail("erin")
			if err != nil {
				t.Fatalf("GetBillingByEmail: %v", err)
			}
			if billing.Friend != st.friend {
				t.Errorf("friend = %v, want %v", billing.Friend, st.friend)
			}
			if got := lookupClientRecord(t, "erin").Group; got != st.wantGroup {
				t.Errorf("group = %q, want %q", got, st.wantGroup)
			}
		})
	}
}

// A confirmed payment makes a free user a paying one, so the client moves from users
// to paid; a friend or a client the admin grouped by hand keeps its group.
func TestPaymentMovesUsersToPaid(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, email, startGroup, wantGroup string
	}{
		{"free user pays", "pay-a@x", "users", "paid"},
		{"friend pays", "pay-b@x", "friends", "friends"},
		{"custom group pays", "pay-c@x", "family", "family"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedPayingClient(t, tc.email, now.UnixMilli())
			if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", tc.email).
				UpdateColumn("group_name", tc.startGroup).Error; err != nil {
				t.Fatalf("set start group: %v", err)
			}
			if _, err := (&BillingService{}).ApplyPayment(tc.email, 1, "1 mo", now); err != nil {
				t.Fatalf("ApplyPayment: %v", err)
			}
			if got := lookupClientRecord(t, tc.email).Group; got != tc.wantGroup {
				t.Errorf("group = %q, want %q", got, tc.wantGroup)
			}
		})
	}
}

// Unmarking a friend who has paid returns them to paid, not to the free users.
func TestUnfriendReturnsAPayerToPaid(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	seedPayingClient(t, "pal@x", now.UnixMilli())
	svc := &ClientService{}
	if _, err := (&BillingService{}).ApplyPayment("pal@x", 1, "1 mo", now); err != nil {
		t.Fatalf("ApplyPayment: %v", err)
	}
	if err := svc.SetFriend("pal@x", true); err != nil {
		t.Fatalf("SetFriend on: %v", err)
	}
	if err := svc.SetFriend("pal@x", false); err != nil {
		t.Fatalf("SetFriend off: %v", err)
	}
	if got := lookupClientRecord(t, "pal@x").Group; got != "paid" {
		t.Fatalf("group = %q, want paid", got)
	}
}
