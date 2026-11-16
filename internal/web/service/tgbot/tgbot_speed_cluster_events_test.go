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

// The confirmed-drop alert names the server, its usual speed and all three
// low results, so the admin sees it was not a one-off.
func TestSpeedDropAlertShowsAverageAndEveryLowResult(t *testing.T) {
	draftLocalizer(t, &i18n.Message{
		ID:    "tgbot.messages.speedDrop",
		Other: "{{ .Server }}|{{ .AvgDown }}/{{ .AvgUp }}|{{ .Results }}",
	})
	got := (&Tgbot{}).formatEventMessage(eventbus.Event{
		Type:   eventbus.EventSpeedDrop,
		Source: "de-1",
		Data: &eventbus.SpeedDropData{
			AvgDownloadMbps: 902.55, AvgUploadMbps: 880,
			DownloadMbps: []float64{480, 510.26, 495}, UploadMbps: []float64{870, 860, 875.5},
		},
	})
	if want := "📉 de-1|902.5/880.0|↓480.0 ↑870.0, ↓510.3 ↑860.0, ↓495.0 ↑875.5"; !strings.HasSuffix(got, want) {
		t.Fatalf("message = %q, want it to end with %q", got, want)
	}
}
