package tgbot

import (
	"errors"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Clients with the AI button suggest sites for the AI list; only an admin, in
// the panel or by one tap here, changes the list itself.
const (
	cbAIExitSuggest   = "ai_s"
	aiSuggestAwaiting = "awaiting_ai_domains"
	// cbAISuggestAnswer is the admin's answer; it must not start with "ai_".
	cbAISuggestAnswer = "aisg"
)

// aiSuggestions spaces a client's suggestions so a loop cannot flood the admins.
var aiSuggestions = newActionThrottle(10 * time.Minute)

func (t *Tgbot) promptAISuggest(chatID int64, actor chatUser, note string) {
	userStateMgr.set(actor, aiSuggestAwaiting)
	t.renderScreen(chatID, t.newScreen("main", t.I18nBot("tgbot.messages.aiSuggestPrompt")+note,
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.cancel", cbAIExitHome))))
}

// handleAISuggestInput takes the client's typed sites; a typo keeps the prompt open.
func (t *Tgbot) handleAISuggestInput(message *telego.Message, actor chatUser) {
	t.deleteIncoming(message)
	if message.From == nil {
		userStateMgr.clear(actor)
		return
	}
	emails := t.aiExitEmails(message.From.ID)
	if len(emails) == 0 {
		userStateMgr.clear(actor)
		return
	}
	domains, err := service.ParseSuggestedDomains(message.Text)
	if err != nil {
		bad := err.Error()
		if errors.Is(err, service.ErrNoSuggestedDomain) {
			bad = message.Text
		}
		t.promptAISuggest(actor.chatID, actor, t.I18nBot("tgbot.messages.aiSuggestBad", "Text=="+html.EscapeString(bad)))
		return
	}
	userStateMgr.clear(actor)
	if !aiSuggestions.allow(message.From.ID, time.Now()) {
		t.screenAIExitOverview(actor.chatID, emails, t.I18nBot("tgbot.messages.aiSuggestWait"))
		return
	}
	sg, err := (&service.AIDomainService{}).Suggest(message.From.ID, emails[0], domains)
	if err != nil {
		logger.Warning("AI domain suggestion:", err)
		t.screenAIExitOverview(actor.chatID, emails, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	msg := t.I18nBot("tgbot.messages.aiSuggestReported", "User=="+userLabel(*message.From),
		"Email=="+html.EscapeString(sg.Email), "Domains=="+html.EscapeString(strings.Join(domains, "\n")))
	answer := func(key, verdict string) []telego.InlineKeyboardButton {
		return tu.InlineKeyboardRow(t.btn(key, t.encodeQuery(cbAISuggestAnswer+" "+strconv.Itoa(sg.Id)+" "+verdict)))
	}
	rows := append([][]telego.InlineKeyboardButton{
		answer("tgbot.buttons.aiSuggestAdded", service.AISuggestAdded),
		answer("tgbot.buttons.aiSuggestPartial", service.AISuggestPartial),
		answer("tgbot.buttons.aiSuggestDeclined", service.AISuggestDeclined),
		answer("tgbot.buttons.aiSuggestPresent", service.AISuggestPresent),
	}, t.hideButton().InlineKeyboard...)
	t.SendMsgToTgbotAdmins(msg, tu.InlineKeyboard(rows...))
	t.screenAIExitOverview(actor.chatID, emails, t.I18nBot("tgbot.messages.aiSuggestSent"))
}

// answerAISuggestion is the admin's tap on a suggestion; the first answer wins.
func (t *Tgbot) answerAISuggestion(callbackQuery *telego.CallbackQuery, idArg, verdict string) {
	id, err := strconv.Atoi(idArg)
	if err != nil {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	sg, claimed, err := (&service.AIDomainService{}).Resolve(id, verdict)
	t.retireStaleConfirm(callbackQuery)
	if err != nil {
		logger.Warning("AI domain suggestion answer:", err)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	if !claimed {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiSuggestAlreadyAnswered"))
		return
	}
	logger.Infof("tgbot: admin %s answered AI domain suggestion %d: %s", userLabel(callbackQuery.From), sg.Id, sg.Status)
	t.NotifyAISuggestionAnswer(sg)
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.aiSuggestAnswered"))
}

var aiSuggestAnswerKeys = map[string]string{
	service.AISuggestAdded:    "tgbot.messages.aiSuggestAdded",
	service.AISuggestPartial:  "tgbot.messages.aiSuggestPartial",
	service.AISuggestDeclined: "tgbot.messages.aiSuggestDeclined",
	service.AISuggestPresent:  "tgbot.messages.aiSuggestPresent",
}

// NotifyAISuggestionAnswer tells the client what became of their suggestion.
func (t *Tgbot) NotifyAISuggestionAnswer(sg model.AIDomainSuggestion) {
	key, ok := aiSuggestAnswerKeys[sg.Status]
	if sg.TgUserId == 0 || !ok {
		return
	}
	t.sendNotice(sg.TgUserId, t.I18nBot(key,
		"Domains=="+html.EscapeString(strings.ReplaceAll(sg.Domains, ",", ", "))))
}
