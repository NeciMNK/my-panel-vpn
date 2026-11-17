package tgbot

import (
	"strings"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// A node over its CPU threshold alerts once under its own name, stays quiet
// while the load hovers around the threshold, and says once when it is back.
func TestNodeLoadAlertAndRecovery(t *testing.T) {
	srv, calls := noticeServer(t)
	swapTestBot(t, srv.URL)
	defer srv.Close()
	setTestAdmins(t, 1)
	notices.reset()
	resetLoadAlerts()
	tb := initReportDB(t)
	if err := tb.settingService.SetTgEnabledEvents("cpu.high"); err != nil {
		t.Fatal(err)
	}
	draftLocalizer(t,
		&i18n.Message{ID: "tgbot.messages.cpuThreshold", Other: "high {{ .Percent }}>{{ .Threshold }}"},
		&i18n.Message{ID: "tgbot.messages.cpuNormal", Other: "normal {{ .Percent }}<{{ .Threshold }}"},
	)
	send := func(low, peak float64) {
		tb.HandleEvent(eventbus.Event{Type: eventbus.EventCPUHigh, Source: "de-1", Data: &eventbus.SystemMetricData{Percent: low, Peak: peak}})
	}

	send(95, 98)
	send(70, 90)
	send(40, 60)
	send(40, 60)

	var texts []string
	for _, c := range calls() {
		texts = append(texts, c.Method+": "+c.Payload["text"].(string))
	}
	want := []string{
		"sendMessage: <b>📡 de-1</b>\n🔴 high 95.00>80",
		"sendMessage: <b>📡 de-1</b>\n🟢 normal 60.00<80",
	}
	if strings.Join(texts, "\n|") != strings.Join(want, "\n|") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
}

// A node's own threshold replaces the bot's: a node expected to run hot at 90%
// must not page at the panel-wide 80%.
func TestNodeOwnThresholdReplacesTheBotThreshold(t *testing.T) {
	srv, calls := noticeServer(t)
	swapTestBot(t, srv.URL)
	defer srv.Close()
	setTestAdmins(t, 1)
	notices.reset()
	resetLoadAlerts()
	tb := initReportDB(t)
	if err := tb.settingService.SetTgEnabledEvents("cpu.high"); err != nil {
		t.Fatal(err)
	}
	draftLocalizer(t, &i18n.Message{ID: "tgbot.messages.cpuThreshold", Other: "high {{ .Percent }}>{{ .Threshold }}"})
	send := func(low float64) {
		tb.HandleEvent(eventbus.Event{Type: eventbus.EventCPUHigh, Source: "hot-1", Data: &eventbus.SystemMetricData{Percent: low, Peak: low, Threshold: 95}})
	}

	send(90)
	if n := len(calls()); n != 0 {
		t.Fatalf("90%% on a node with its own 95%% threshold sent %d messages", n)
	}
	send(97)
	if got := calls(); len(got) != 1 || !strings.HasSuffix(got[0].Payload["text"].(string), "high 97.00>95") {
		t.Fatalf("calls = %+v, want one alert against the node's 95%%", got)
	}
}
