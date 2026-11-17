package tgbot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func messagesTo(calls []apiCall, chatID int64) []string {
	var out []string
	for _, c := range calls {
		if id, ok := c.Payload["chat_id"].(float64); ok && int64(id) == chatID {
			out = append(out, c.text())
		}
	}
	return out
}

func setNodeStatus(t *testing.T, nodeID int, status string) {
	t.Helper()
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", nodeID).Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
}

// A pinned client used to learn of a dead server only when ChatGPT stopped
// opening; now the bot asks them what to do and remembers the answer.
func TestPinnedClientChoosesWhatHappensWhileTheirServerIsDown(t *testing.T) {
	tb, calls := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 6060, "ai@x")
	seedBilledClient(t, 7070, "plain@x")
	seedAIExitWorld(t)
	if err := database.GetDB().Create(&model.CascadeFixedExit{Email: "ai@x", NodeId: 2}).Error; err != nil {
		t.Fatal(err)
	}
	down := eventbus.Event{Type: eventbus.EventNodeDown, Source: "de-1", Data: &eventbus.NodeHealthData{NodeId: 2}}

	setNodeStatus(t, 2, "offline")
	tb.HandleEvent(down)
	body, markup := screenMarkup(t, tb, 6060)
	if !strings.Contains(body, "de-1 with your pinned IP is down") || !strings.Contains(body, "Blocked until it is back: ChatGPT, Claude") {
		t.Fatalf("outage notice = %q", body)
	}
	if !strings.Contains(markup, `"`+cbAIOutageOther+`"`) || !strings.Contains(markup, `"`+cbAIOutageNoUse+`"`) {
		t.Fatalf("outage notice offers no choice: %s", markup)
	}
	if got := messagesTo(calls(), 7070); len(got) != 0 {
		t.Fatalf("a client pinned to nothing was told: %v", got)
	}

	tap(tb, 6060, cbAIOutageOther, false)
	if body, _ := screenMarkup(t, tb, 6060); !strings.Contains(body, "its traffic goes via de-2") {
		t.Fatalf("after another IP: %q, want de-2 named", body)
	}

	tap(tb, 6060, cbAIOutageNoUse, false)
	tap(tb, 6060, cbAIOutageBlock, false)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, cbAIOutageToggle+" ChatGPT") {
		t.Fatalf("no AI service to tick: %s", markup)
	}
	tap(tb, 6060, cbAIOutageToggle+" ChatGPT", false)
	choice := (&service.CascadeService{}).AIOutageChoiceOf([]string{"ai@x"})
	all := service.AIServiceNames()
	if choice.Action != service.AIOutageBlock || !reflect.DeepEqual(choice.Services, all[1:]) {
		t.Fatalf("choice = %+v, want every service but ChatGPT %v", choice, all[1:])
	}

	tb.HandleEvent(eventbus.Event{Type: eventbus.EventNodeUp, Source: "de-1", Data: &eventbus.NodeHealthData{NodeId: 2}})
	got := messagesTo(calls(), 6060)
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "de-1 is back") {
		t.Fatalf("no notice when the server came back: %v", got)
	}
}

func TestToggleAIServiceNeverLeavesNoneBlocked(t *testing.T) {
	initReportDB(t)
	all := service.AIServiceNames()
	got, ok := toggleAIService(nil, "Claude")
	if !ok || len(got) != len(all)-1 || reflect.DeepEqual(got, all) {
		t.Fatalf("untick Claude from all = %v %v", got, ok)
	}
	if got, ok := toggleAIService(got, "Claude"); !ok || got != nil {
		t.Fatalf("tick Claude back = %v %v, want nil (all)", got, ok)
	}
	if _, ok := toggleAIService([]string{"Claude"}, "Claude"); ok {
		t.Fatal("unticking the last service was allowed")
	}
}
