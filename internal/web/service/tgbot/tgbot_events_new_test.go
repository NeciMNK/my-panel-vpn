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
