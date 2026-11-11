package tgbot

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/mymmrac/telego"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// clientDraftTestChatID is a chat id no other test drives, so the draft this
// test fills cannot leak into them.
const clientDraftTestChatID = -9001

// Regression test: the draft is sent with ParseMode HTML, so Markdown markers
// were rendered literally and an unescaped value could break the whole message.
func TestClientDraftMessageRendersHTML(t *testing.T) {
	localizeWithRealBundle(t)
	draft := addClientDrafts.forActor(chatUser{chatID: clientDraftTestChatID, userID: 1})
	t.Cleanup(func() { addClientDrafts.reset(chatUser{chatID: clientDraftTestChatID, userID: 1}) })

	draft.email = "a@b.c"
	draft.comment = "<b>promo</b> & <10 GB>"
	draft.tgID = "42"
	draft.totalGB, draft.limitIP, draft.expiryTime = 0, 0, 0
	draft.receiverInboundIDs = nil

	out := (&Tgbot{}).BuildClientDraftMessage(draft)

	if !strings.Contains(out, "<b>New client draft</b>") {
		t.Errorf("draft title is not HTML markup: %q", out)
	}
	if strings.Contains(out, "*New client draft*") || strings.Contains(out, "`") {
		t.Errorf("draft still carries Markdown markers: %q", out)
	}
	if strings.Contains(out, "<b>promo</b>") {
		t.Errorf("raw comment markup reached the message: %q", out)
	}
	if !strings.Contains(out, html.EscapeString(draft.comment)) {
		t.Errorf("comment is not HTML-escaped: %q", out)
	}
}

// draftLocalizer registers only the messages a wizard test drives, with the
// templates the translation files carry; without it I18n returns the bare key.
func draftLocalizer(t *testing.T, msgs ...*i18n.Message) {
	t.Helper()
	bundle := i18n.NewBundle(language.MustParse("en-US"))
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	_ = bundle.AddMessages(language.MustParse("en-US"), msgs...)
	orig := locale.SwapBotLocalizer(i18n.NewLocalizer(bundle, "en-US"))
	t.Cleanup(func() { locale.SwapBotLocalizer(orig) })
}

// Regression test: the wizard's own prompts are HTML-parsed as well, so the
// draft value they echo has to be escaped exactly like the draft card.
func TestAddClientPromptsEscapeDraftValues(t *testing.T) {
	draftLocalizer(t,
		&i18n.Message{ID: "tgbot.messages.email_prompt", Other: "📧 Default Email: {{ .ClientEmail }}\n\nEnter your email."},
		&i18n.Message{ID: "tgbot.messages.comment_prompt", Other: "💬 Default Comment: {{ .ClientComment }}\n\nEnter your comment."},
	)
	url, texts := draftTexts(t)
	swapTestBot(t, url)

	draft := addClientDrafts.forActor(chatUser{chatID: 1, userID: 1})
	origRunning := isRunning
	t.Cleanup(func() {
		addClientDrafts.reset(chatUser{chatID: 1, userID: 1})
		isRunning = origRunning
	})
	isRunning = true

	cases := []struct {
		name  string
		data  string
		value string
	}{
		{"email prompt", "add_client_ch_default_email", "long<name>@example.com"},
		{"comment prompt", "add_client_ch_default_comment", "promo <b>tag</b>"},
	}
	tb := &Tgbot{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft.email, draft.comment = tc.value, tc.value

			tb.answerCallback(&telego.CallbackQuery{
				ID:      "q1",
				From:    telego.User{ID: 1},
				Data:    tc.data,
				Message: &telego.Message{Chat: telego.Chat{ID: 1}},
			}, true) // admin

			sent := texts(1)
			if len(sent) == 0 {
				t.Fatalf("no prompt was sent for %s", tc.data)
			}
			got := sent[len(sent)-1]
			if strings.Contains(got, tc.value) {
				t.Errorf("prompt = %q, want the draft value escaped", got)
			}
			if !strings.Contains(got, html.EscapeString(tc.value)) {
				t.Errorf("prompt = %q, want it to contain %q", got, html.EscapeString(tc.value))
			}
		})
	}
}

// The ru-RU draft and client cards printed "Attached:", "Traffic:", "🔗 Inbounds:"
// from Go literals, and labelled upload and download both "Загрузка".
func TestRussianClientCardsCarryNoEnglishOrRepeatedLabels(t *testing.T) {
	tb := initReportDB(t)
	localizeWithShippedLocale(t, "ru-RU")
	seedReportClients(t, "Вход", []string{"ru-card@x"})
	var traffic xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", "ru-card@x").First(&traffic).Error; err != nil {
		t.Fatalf("read traffic: %v", err)
	}
	actor := chatUser{chatID: clientDraftTestChatID, userID: 2}
	draft := addClientDrafts.forActor(actor)
	t.Cleanup(func() { addClientDrafts.reset(actor) })
	draft.email = "ru-card@x"

	cards := map[string]string{
		"draft":  tb.BuildClientDraftMessage(draft),
		"client": tb.clientInfoMsg(&traffic, true, true, true, true, true, false),
	}
	english := []string{"New client draft", "Attached", "Traffic:", "Expire:", "IP limit", "TG user", "Comment:", "Unlimited", "Inbounds:"}
	for name, card := range cards {
		for _, word := range english {
			if strings.Contains(card, word) {
				t.Errorf("%s card carries English %q:\n%s", name, word, card)
			}
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.ReplaceAll(card, "\r\n", "\n"), "\n") {
			label, _, found := strings.Cut(line, ":")
			if !found || label == "" {
				continue
			}
			if seen[label] {
				t.Errorf("%s card repeats the label %q:\n%s", name, label, card)
			}
			seen[label] = true
		}
	}
}
