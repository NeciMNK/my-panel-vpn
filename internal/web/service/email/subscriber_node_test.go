package email

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// A node's overload must name the node in the subject, not the master's host.
func TestNodeLoadSubjectNamesTheNode(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	s := &Subscriber{}
	if err := s.settingService.SetSmtpMemory(80); err != nil {
		t.Fatal(err)
	}
	subject, _ := s.formatMessage(eventbus.Event{
		Type:   eventbus.EventMemoryHigh,
		Source: "de-1",
		Data:   &eventbus.SystemMetricData{Percent: 95, Peak: 97},
	})
	if !strings.HasPrefix(subject, "de-1 ") {
		t.Fatalf("subject = %q, want it to start with the node name", subject)
	}
}
