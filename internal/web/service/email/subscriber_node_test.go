package email

import (
	"encoding/json"
	"strings"
	"testing"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
)

// A node only the master lost still serves users: the mail must not read as an outage.
func TestNodeDownMailTellsAMasterSideLossFromAnOutage(t *testing.T) {
	bundle := goi18n.NewBundle(language.MustParse("en-US"))
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	_ = bundle.AddMessages(language.MustParse("en-US"),
		&goi18n.Message{ID: "tgbot.messages.eventNodeDown", Other: "down={{ .Name }}"},
		&goi18n.Message{ID: "tgbot.messages.eventNodeIsolated", Other: "lost={{ .Name }} seen={{ .SeenBy }}"},
		&goi18n.Message{ID: "email.statusDown", Other: "DOWN"},
		&goi18n.Message{ID: "email.statusIsolated", Other: "MASTER LOST IT"},
	)
	orig := locale.SwapBotLocalizer(goi18n.NewLocalizer(bundle, "en-US"))
	t.Cleanup(func() { locale.SwapBotLocalizer(orig) })

	for _, tc := range []struct {
		seenBy        []string
		subject, body string
	}{
		{nil, " down=de-1", "DOWN"},
		{[]string{"nl-1"}, " lost=de-1 seen=nl-1", "MASTER LOST IT"},
	} {
		subject, body := (&Subscriber{}).formatMessage(eventbus.Event{
			Type: eventbus.EventNodeDown, Source: "de-1",
			Data: &eventbus.NodeHealthData{SeenBy: tc.seenBy},
		})
		if !strings.HasSuffix(subject, tc.subject) || !strings.Contains(body, ">"+tc.body+"<") {
			t.Fatalf("seenBy %v: subject %q body %q, want subject suffix %q and status %q", tc.seenBy, subject, body, tc.subject, tc.body)
		}
	}
}
