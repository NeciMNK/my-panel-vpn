package tgbot

import (
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// An event the admin shared with a group reaches that group's chats as well as
// the admins; a login alert never leaves the admins.
func TestEventReachesTheChosenGroupsChats(t *testing.T) {
	srv, calls := noticeServer(t)
	swapTestBot(t, srv.URL)
	defer srv.Close()
	setTestAdmins(t, 1)
	notices.reset()
	tb := initReportDB(t)
	if err := database.GetDB().Create(&model.ClientRecord{Email: "fr", Group: "friends", TgID: 11}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tb.settingService.SetTgEnabledEvents("node.down,login.attempt"); err != nil {
		t.Fatal(err)
	}
	if err := tb.settingService.SetTgEventRecipients(`{"node.down":{"groups":["friends"]}}`); err != nil {
		t.Fatal(err)
	}
	// Stored by hand: SetTgEventRecipients itself already drops admin-only events.
	if err := database.GetDB().Model(&model.Setting{}).Where("key = ?", "tgEventRecipients").
		Update("value", `{"node.down":{"groups":["friends"]},"login.attempt":{"groups":["friends"]}}`).Error; err != nil {
		t.Fatal(err)
	}

	chatsOf := func() []int64 {
		var out []int64
		for _, c := range calls() {
			if c.Method == "sendMessage" {
				out = append(out, int64(c.Payload["chat_id"].(float64)))
			}
		}
		slices.Sort(out)
		return out
	}
	tb.HandleEvent(eventbus.Event{Type: eventbus.EventNodeDown, Source: "de-1"})
	if got := chatsOf(); !slices.Equal(got, []int64{1, 11}) {
		t.Fatalf("node.down went to %v, want the admin and the friend [1 11]", got)
	}
	tb.HandleEvent(eventbus.Event{Type: eventbus.EventLoginAttempt, Source: "1.2.3.4", Data: &eventbus.LoginEventData{Status: "fail"}})
	if got := chatsOf(); !slices.Equal(got, []int64{1, 1, 11}) {
		t.Fatalf("after a login alert chats = %v, want only the admin added", got)
	}
}
