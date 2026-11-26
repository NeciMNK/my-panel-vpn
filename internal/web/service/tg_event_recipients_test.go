package service

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The admin picks groups and single clients per event; their Telegram chats get
// it alongside the admins. Login alerts stay admin-only whatever is stored.
func TestTgEventChatsResolveGroupsAndClients(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	for _, c := range []model.ClientRecord{
		{Email: "f1", Group: "friends", TgID: 11},
		{Email: "f2", Group: "friends", TgID: 12},
		{Email: "f3", Group: "friends", TgID: 0},
		{Email: "anna", Group: "paid", TgID: 21},
		{Email: "anna-2", Group: "users", TgID: 21},
		{Email: "bob", Group: "users", TgID: 31},
		{Email: "nogroup", Group: "", TgID: 41},
	} {
		if err := database.GetDB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	var s SettingService
	err := s.SetTgEventRecipients(`{"node.down":{"groups":["friends"],"clients":["anna","anna-2","f1"]},"node.up":{"groups":["friends"]},"login.attempt":{"groups":["users"]}}`)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		event string
		want  []int64
	}{
		{"node.down", []int64{11, 12, 21}},
		{"node.up", []int64{11, 12}},
		{"xray.crash", nil},
		{"login.attempt", nil},
	} {
		got, err := s.TgEventChats(tc.event)
		slices.Sort(got)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: chats = %v, %v; want %v", tc.event, got, err, tc.want)
		}
	}
	if err := s.SetTgEventRecipients(`{"node.down":`); err == nil {
		t.Fatal("a broken recipients list was stored")
	}
}
