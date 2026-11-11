package tgbot

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// seedAIExitWorld: the client's master inbound enters a cascade to DE (2, 3)
// and NL (4); DE node 3 has used up its 100 Mbit/s.
func seedAIExitWorld(t *testing.T) {
	t.Helper()
	rows := []any{
		&model.Node{Id: 2, Name: "de-1", Address: "2.2.2.2", Port: 1, Enable: true, Status: "online", Country: "DE"},
		&model.Node{Id: 3, Name: "de-2", Address: "3.3.3.3", Port: 1, Enable: true, Status: "online", Country: "DE", NetDown: 100e6 / 8},
		&model.Node{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Status: "online", Country: "NL"},
		&model.ServerCapacity{NodeId: 3, CapacityMbps: 100},
		&model.Cascade{Name: "eu", Enable: true, EntryNodeId: 0, Links: []model.CascadeLink{{NodeId: 2}, {NodeId: 3}, {NodeId: 4}}},
	}
	for _, r := range rows {
		if err := database.GetDB().Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func pinnedExits(t *testing.T, email string) []int {
	t.Helper()
	var ids []int
	if err := database.GetDB().Model(&model.CascadeFixedExit{}).Where("email = ?", email).Pluck("node_id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	return ids
}

func TestTrustedClientPinsAnAIExitThroughTheQuiz(t *testing.T) {
	tb, _ := newScreenTgbot(t, false)
	initReportDB(t)
	localizeWithRealBundle(t)
	setTestAdmins(t, 9001)
	seedBilledClient(t, 6060, "ai@x")
	seedAIExitWorld(t)

	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); strings.Contains(markup, `"`+cbAIExit+`"`) {
		t.Fatalf("an untrusted client is offered the AI button: %s", markup)
	}
	tap(tb, 6060, cbAIExit, false)
	if got := pinnedExits(t, "ai@x"); len(got) != 0 {
		t.Fatalf("an untrusted client pinned %v", got)
	}

	tap(tb, 9001, "trust ai@x", true)
	tb.screenClientHome(6060, 6060)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"`+cbAIExit+`"`) {
		t.Fatalf("a trusted client has no AI button: %s", markup)
	}

	tap(tb, 6060, cbAIExit, false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("after the AI button pins = %v, want NL's only server 4 pinned at once", got)
	}
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"ai_y DE"`) || !strings.Contains(markup, `"ai_n DE"`) {
		t.Fatalf("DE has two servers, so the quiz must ask about it: %s", markup)
	}

	tap(tb, 6060, "ai_y DE", false)
	if _, markup := screenMarkup(t, tb, 6060); !strings.Contains(markup, `"ai_p 2"`) || strings.Contains(markup, `"ai_p 3"`) {
		t.Fatalf("remembered-IP list must offer node 2 and hide full node 3: %s", markup)
	}
	tap(tb, 6060, "ai_p 3", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("a forged tap pinned the full server: %v", got)
	}

	tap(tb, 6060, "ai_n DE", false)
	tap(tb, 6060, "ai_a DE", false)
	if got := pinnedExits(t, "ai@x"); !reflect.DeepEqual(got, []int{2, 4}) {
		t.Fatalf("auto pick pinned %v, want DE node 2 (node 3 is full) and NL node 4", got)
	}
}
