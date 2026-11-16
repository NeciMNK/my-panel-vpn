package tgbot

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const (
	cbReminders      = "rem"
	cbReminderToggle = "rem_tog"
	cbReminderDay    = "rem_d"
)

// SendPaymentReminders reminds every client chat whose nearest due date reached
// one of its chosen days; each day reminds once per due date.
func (t *Tgbot) SendPaymentReminders(now time.Time) {
	if !t.IsRunning() {
		return
	}
	var chats []int64
	if err := database.GetDB().Table("clients").Where("tg_id > 0").Distinct().Pluck("tg_id", &chats).Error; err != nil {
		logger.Warning("payment reminders: list chats:", err)
		return
	}
	for _, chatID := range chats {
		if checkAdmin(chatID) {
			continue
		}
		traffics, err := t.inboundService.GetClientTrafficTgBot(chatID)
		if err != nil {
			logger.Warningf("payment reminders: traffic of %d: %v", chatID, err)
			continue
		}
		var running []*xray.ClientTraffic
		for _, traffic := range traffics {
			if traffic.Enable && traffic.ExpiryTime > 0 {
				running = append(running, traffic)
			}
		}
		due, ok := nextPaymentDue(running)
		if !ok {
			continue
		}
		day, ok := t.reminderService.Due(chatID, due, now)
		if !ok {
			continue
		}
		text := t.I18nBot("tgbot.reminders.message", "Days=="+strconv.Itoa(day), "Date=="+due.Format(paymentDate))
		t.sendExpiryReminder(chatID, text, running)
		if err := t.reminderService.MarkSent(chatID, due, day); err != nil {
			logger.Warningf("payment reminders: mark %d: %v", chatID, err)
		}
	}
}

// answerReminderCallback handles the client's own reminder screen; any user may
// reach it since it only ever touches the caller's own choice.
func (t *Tgbot) answerReminderCallback(query *telego.CallbackQuery, data string) bool {
	chatID, userID := query.Message.GetChat().ID, query.From.ID
	switch {
	case data == cbReminders:
	case data == cbReminderToggle:
		prefs, err := t.reminderService.Prefs(userID)
		if err == nil && prefs.Disabled && len(prefs.Days) == 0 {
			err = t.reminderService.ToggleDay(userID, slices.Min(prefs.Choices))
		} else if err == nil {
			err = t.reminderService.SetDisabled(userID, !prefs.Disabled)
		}
		if err != nil {
			logger.Warning("reminders: toggle:", err)
		}
	case strings.HasPrefix(data, cbReminderDay):
		day, err := strconv.Atoi(strings.TrimPrefix(data, cbReminderDay))
		if err != nil {
			return false
		}
		if prefs, err := t.reminderService.Prefs(userID); err != nil || !slices.Contains(prefs.Choices, day) {
			return false
		}
		if err := t.reminderService.ToggleDay(userID, day); err != nil {
			logger.Warning("reminders: toggle day:", err)
		}
	default:
		return false
	}
	t.answerSilent(query.ID)
	t.screenReminders(chatID, userID)
	return true
}

func (t *Tgbot) screenReminders(chatID, userID int64) {
	prefs, err := t.reminderService.Prefs(userID)
	if err != nil {
		logger.Warning("reminders: load:", err)
		return
	}
	status := t.I18nBot("tgbot.reminders.on")
	switchKey := "tgbot.reminders.turnOff"
	if prefs.Disabled {
		status, switchKey = t.I18nBot("tgbot.reminders.off"), "tgbot.reminders.turnOn"
	}
	days := make([]string, len(prefs.Days))
	for i, d := range prefs.Days {
		days[i] = strconv.Itoa(d)
	}
	body := t.I18nBot("tgbot.reminders.screen", "Status=="+status, "Days=="+strings.Join(days, ", "))
	var dayButtons []telego.InlineKeyboardButton
	for _, d := range prefs.Choices {
		mark := "▫️ "
		if slices.Contains(prefs.Days, d) && !prefs.Disabled {
			mark = "✅ "
		}
		dayButtons = append(dayButtons, t.btn2(mark+t.I18nBot("tgbot.reminders.day", "Days=="+strconv.Itoa(d)), cbReminderDay+strconv.Itoa(d)))
	}
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(t.btn(switchKey, cbReminderToggle)),
		tu.InlineKeyboardRow(dayButtons...),
		t.backTo(t.clientHomeCallback(userID)),
	}
	t.renderScreen(chatID, t.newScreen("reminders", body, rows...))
}
