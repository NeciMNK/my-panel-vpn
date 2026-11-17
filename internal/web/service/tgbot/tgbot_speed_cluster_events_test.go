package tgbot

import (
	"strings"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// The speed alert must name the starved server, its free speed, the minimum it
// misses and who misses it, or the admin cannot tell which server to grow.
func TestSpeedLowAlertNamesServerSpeedsAndUsers(t *testing.T) {
	draftLocalizer(t, &i18n.Message{
		ID:    "tgbot.messages.speedLow",
		Other: "{{ .Server }}|{{ .Free }}|{{ .Min }}|{{ .Users }}",
	})
	got := (&Tgbot{}).formatEventMessage(eventbus.Event{
		Type:   eventbus.EventSpeedLow,
		Source: "de-1\nfake line",
		Data:   &eventbus.SpeedLowData{FreePerUserMbps: 12.25, MinMbps: 50, Users: []string{"anna", "bob"}},
	})
	if want := "🐢 de-1 fake line|12.2|50|anna, bob"; !strings.HasSuffix(got, want) {
		t.Fatalf("message = %q, want it to end with %q", got, want)
	}
}

// A failover alert says who took over and, unless the cluster just started,
// whom it replaced.
func TestMasterLeaderAlertNamesTheMasterItReplaced(t *testing.T) {
	draftLocalizer(t,
		&i18n.Message{ID: "tgbot.messages.masterLeader", Other: "lead={{ .Master }}"},
		&i18n.Message{ID: "tgbot.messages.masterLeaderPrevious", Other: "was={{ .Previous }}"},
	)
	for _, tc := range []struct{ previous, want string }{
		{"master-1", "👑 lead=master-2\nwas=master-1"},
		{"", "👑 lead=master-2"},
	} {
		got := (&Tgbot{}).formatEventMessage(eventbus.Event{
			Type: eventbus.EventMasterLeader, Source: "master-2",
			Data: &eventbus.MasterLeaderData{Previous: tc.previous},
		})
		if !strings.HasSuffix(got, tc.want) {
			t.Fatalf("previous %q: message = %q, want suffix %q", tc.previous, got, tc.want)
		}
	}
}

// A node only the master lost still serves users: the alert must say so and name
// who reaches it, not read as an outage.
func TestNodeDownAlertTellsAMasterSideLossFromAnOutage(t *testing.T) {
	draftLocalizer(t,
		&i18n.Message{ID: "tgbot.messages.eventNodeDown", Other: "down={{ .Name }}"},
		&i18n.Message{ID: "tgbot.messages.eventNodeIsolated", Other: "lost={{ .Name }} seen={{ .SeenBy }}"},
	)
	for _, tc := range []struct {
		seenBy []string
		want   string
	}{
		{nil, "🔴 down=de-1"},
		{[]string{"nl-1", "fi-1"}, "🟠 lost=de-1 seen=nl-1, fi-1"},
	} {
		got := (&Tgbot{}).formatEventMessage(eventbus.Event{
			Type: eventbus.EventNodeDown, Source: "de-1",
			Data: &eventbus.NodeHealthData{SeenBy: tc.seenBy},
		})
		if !strings.HasSuffix(got, tc.want) {
			t.Fatalf("seenBy %v: message = %q, want suffix %q", tc.seenBy, got, tc.want)
		}
	}
}
