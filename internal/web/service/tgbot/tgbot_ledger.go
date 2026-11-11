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

const (
	cbLedger          = "ldg"
	ledgerStatePrefix = "ledger:"

	ledgerAddServer  = "server"
	ledgerAddIncome  = service.LedgerIncome
	ledgerAddExpense = service.LedgerExpense
)

var errLedgerInput = errors.New("ledger input not understood")

// ledgerState is the awaited-text state for one add form, bound to the month it books into.
func ledgerState(kind, month string) string { return ledgerStatePrefix + kind + ":" + month }

func parseLedgerState(state string) (kind, month string, ok bool) {
	rest, found := strings.CutPrefix(state, ledgerStatePrefix)
	if !found {
		return "", "", false
	}
	kind, month, ok = strings.Cut(rest, ":")
	return kind, month, ok
}

// parseLedgerServer reads "<name> <cost> [day] [YYYY-MM]"; a missing day is the 1st, a missing month is month.
func parseLedgerServer(text, month string) (model.LedgerServer, error) {
	fields := strings.Fields(text)
	start := month
	if n := len(fields); n >= 3 && strings.Count(fields[n-1], "-") == 1 {
		start, fields = fields[n-1], fields[:n-1]
	}
	day := 1
	if n := len(fields); n >= 3 {
		last, lastErr := strconv.Atoi(fields[n-1])
		_, costErr := strconv.Atoi(fields[n-2])
		if lastErr == nil && costErr == nil && last >= 1 && last <= 31 {
			day, fields = last, fields[:n-1]
		}
	}
	if len(fields) < 2 {
		return model.LedgerServer{}, errLedgerInput
	}
	cost, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return model.LedgerServer{}, errLedgerInput
	}
	return model.LedgerServer{
		Name: strings.Join(fields[:len(fields)-1], " "), MonthlyCost: cost, StartMonth: start, BillingDay: day,
	}, nil
}

// parseLedgerAmount reads "<amount> [rest...]".
func parseLedgerAmount(text string) (int, string, error) {
	head, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
	amount, err := strconv.Atoi(head)
	if err != nil {
		return 0, "", errLedgerInput
	}
	return amount, strings.TrimSpace(rest), nil
}

func (t *Tgbot) ledgerMoney(report *service.LedgerReport, amount int) string {
	return strings.TrimSpace(strconv.Itoa(amount) + " " + report.Currency)
}

func (t *Tgbot) ledgerBody(report *service.LedgerReport) string {
	var b strings.Builder
	sign := "📈"
	if report.Balance < 0 {
		sign = "📉"
	}
	b.WriteString(t.I18nBot("tgbot.ledger.summary",
		"Month=="+report.Month,
		"Income=="+t.ledgerMoney(report, report.Income),
		"Expenses=="+t.ledgerMoney(report, report.Expenses),
		"Servers=="+t.ledgerMoney(report, report.ServerCosts),
		"Sign=="+sign,
		"Balance=="+t.ledgerMoney(report, report.Balance),
		"Paying=="+strconv.Itoa(report.PayingClients),
		"Expected=="+t.ledgerMoney(report, report.ExpectedIncome)))
	var servers []string
	for _, server := range report.Servers {
		if server.StartMonth <= report.Month && (server.EndMonth == "" || report.Month <= server.EndMonth) {
			servers = append(servers, "• "+html.EscapeString(server.Name)+" — "+t.ledgerMoney(report, server.MonthlyCost)+
				t.I18nBot("tgbot.ledger.billingDay", "Day=="+strconv.Itoa(server.BillingDay)))
		}
	}
	if len(servers) > 0 {
		b.WriteString(t.I18nBot("tgbot.ledger.serversTitle") + strings.Join(servers, "\r\n") + "\r\n")
	}
	if len(report.Entries) > 0 {
		b.WriteString(t.I18nBot("tgbot.ledger.entriesTitle"))
		for _, entry := range report.Entries {
			b.WriteString(t.ledgerEntryLine(report, entry) + "\r\n")
		}
	}
	if len(report.NonPaying) > 0 {
		names := make([]string, 0, len(report.NonPaying))
		for _, row := range report.NonPaying {
			names = append(names, html.EscapeString(row.Email))
		}
		b.WriteString(t.I18nBot("tgbot.ledger.nonPayingTitle") + strings.Join(names, ", ") + "\r\n")
	}
	return b.String()
}

func (t *Tgbot) ledgerEntryLine(report *service.LedgerReport, entry model.LedgerEntry) string {
	mark := "➕"
	if entry.Kind == service.LedgerExpense {
		mark = "➖"
	}
	line := mark + " " + t.ledgerMoney(report, entry.Amount)
	for _, part := range []string{entry.Email, entry.Note} {
		if part != "" {
			line += " · " + html.EscapeString(part)
		}
	}
	return line
}

// screenLedger is the admin's month of profit and loss with every ledger action one tap away.
func (t *Tgbot) screenLedger(chatID int64, month string) {
	report, err := t.ledgerService.Report(month)
	if err != nil {
		logger.Warning("ledger report:", err)
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	prev, next := service.ShiftLedgerMonth(month, -1), service.ShiftLedgerMonth(month, 1)
	prevLabel, nextLabel := "◀ "+prev, next+" ▶"
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(
			t.btn2(prevLabel, t.encodeQuery("ledger "+prev)),
			t.btn2(nextLabel, t.encodeQuery("ledger "+next)),
		),
		tu.InlineKeyboardRow(
			t.btn("tgbot.buttons.ledgerAddIncome", t.encodeQuery("ledger_add "+ledgerAddIncome+" "+month)),
			t.btn("tgbot.buttons.ledgerAddExpense", t.encodeQuery("ledger_add "+ledgerAddExpense+" "+month)),
		),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.ledgerAddServer", t.encodeQuery("ledger_add "+ledgerAddServer+" "+month))),
		tu.InlineKeyboardRow(
			t.btn("tgbot.buttons.ledgerDelete", t.encodeQuery("ledger_del "+month)),
			t.btn("tgbot.buttons.ledgerFreeGroups", t.encodeQuery("ledger_groups "+month)),
		),
		t.backRow(),
	}
	t.renderScreen(chatID, t.newScreen("pay", t.ledgerBody(report), rows...))
}

func (t *Tgbot) promptLedgerInput(chatID int64, actor chatUser, kind, month string) {
	userStateMgr.set(actor, ledgerState(kind, month))
	back := tu.InlineKeyboardRow(t.btn("tgbot.buttons.cancel", t.encodeQuery("ledger "+month)))
	t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.ledger.prompt_"+kind, "Month=="+month), back))
}

// handleLedgerInput books the admin's typed line; bad input keeps the prompt open for another try.
func (t *Tgbot) handleLedgerInput(message *telego.Message, actor chatUser, kind, month string) {
	t.deleteIncoming(message)
	if !checkAdmin(message.From.ID) {
		userStateMgr.clear(actor)
		return
	}
	var err error
	switch kind {
	case ledgerAddServer:
		var server model.LedgerServer
		if server, err = parseLedgerServer(message.Text, month); err == nil {
			err = t.ledgerService.SaveServer(&server)
		}
	case ledgerAddIncome, ledgerAddExpense:
		entry := model.LedgerEntry{Month: month, Kind: kind}
		var rest string
		if entry.Amount, rest, err = parseLedgerAmount(message.Text); err == nil {
			entry.Note = rest
			if first, note, _ := strings.Cut(rest, " "); kind == ledgerAddIncome && first != "" {
				if _, lookupErr := t.clientService.GetRecordByEmail(nil, first); lookupErr == nil {
					entry.Email, entry.Note = first, strings.TrimSpace(note)
				}
			}
			err = t.ledgerService.AddEntry(&entry)
		}
	default:
		err = errLedgerInput
	}
	if err != nil {
		t.sendNotice(actor.chatID, t.I18nBot("tgbot.ledger.badInput", "Error=="+err.Error()))
		return
	}
	userStateMgr.clear(actor)
	t.screenLedger(actor.chatID, month)
}

// screenLedgerDelete lists the month's entries and every server as delete buttons.
func (t *Tgbot) screenLedgerDelete(chatID int64, month string) {
	report, err := t.ledgerService.Report(month)
	if err != nil {
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	rows := make([][]telego.InlineKeyboardButton, 0, len(report.Entries)+len(report.Servers)+1)
	for _, entry := range report.Entries {
		label := "🗑 " + html.UnescapeString(t.ledgerEntryLine(report, entry))
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(label, t.encodeQuery("ledger_rm e "+strconv.Itoa(entry.Id)+" "+month))))
	}
	for _, server := range report.Servers {
		label := "🗑 🖥 " + server.Name + " · " + t.ledgerMoney(report, server.MonthlyCost)
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(label,
			t.encodeQuery("ledger_rm s "+strconv.Itoa(server.Id)+" "+month))))
	}
	rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.back", t.encodeQuery("ledger "+month))))
	t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.ledger.deleteTitle", "Month=="+month), rows...))
}

func (t *Tgbot) deleteLedgerRow(callbackQuery *telego.CallbackQuery, what, idText, month string) {
	id, err := strconv.Atoi(idText)
	if err == nil {
		if what == "s" {
			err = t.ledgerService.DeleteServer(id)
		} else {
			err = t.ledgerService.DeleteEntry(id)
		}
	}
	if err != nil {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
	t.screenLedgerDelete(callbackQuery.Message.GetChat().ID, month)
}

// screenLedgerGroups toggles which client groups (e.g. family) never pay.
func (t *Tgbot) screenLedgerGroups(chatID int64, month string) {
	report, err := t.ledgerService.Report(month)
	groups, groupsErr := t.clientService.ListGroups()
	if err != nil || groupsErr != nil {
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	rows := make([][]telego.InlineKeyboardButton, 0, len(groups)+1)
	for _, group := range groups {
		mark := "💰 "
		for _, free := range report.FreeGroups {
			if free == group.Name {
				mark = "🆓 "
			}
		}
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(mark+group.Name, t.encodeQuery("ledger_grp "+month+" "+group.Name))))
	}
	rows = append(rows, tu.InlineKeyboardRow(t.btn("tgbot.buttons.back", t.encodeQuery("ledger "+month))))
	t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.ledger.groupsTitle"), rows...))
}

func (t *Tgbot) toggleLedgerGroup(callbackQuery *telego.CallbackQuery, month, group string) {
	report, err := t.ledgerService.Report(month)
	if err == nil {
		free := false
		for _, name := range report.FreeGroups {
			free = free || name == group
		}
		err = t.ledgerService.SetGroupNonPaying(group, !free)
	}
	if err != nil {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.answerSilent(callbackQuery.ID)
	t.screenLedgerGroups(callbackQuery.Message.GetChat().ID, month)
}

// nonPayingRow is the client card's switch that keeps e.g. the admin out of expected income.
func (t *Tgbot) nonPayingRow(email string) []telego.InlineKeyboardButton {
	state := t.I18nBot("tgbot.messages.no")
	if t.billingFor(email).NonPaying {
		state = t.I18nBot("tgbot.messages.yes")
	}
	return tu.InlineKeyboardRow(t.btn2(
		t.I18nBot("tgbot.buttons.nonPaying", "State=="+strings.TrimSpace(state)), t.encodeQuery("nonpay "+email)))
}

func (t *Tgbot) toggleNonPaying(callbackQuery *telego.CallbackQuery, email string) {
	err := t.ledgerService.SetClientNonPaying(email, !t.billingFor(email).NonPaying)
	if err != nil {
		logger.Warning("toggleNonPaying:", err)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
	t.searchClientScreen(callbackQuery.Message.GetChat().ID, email)
}

func currentLedgerMonth() string { return service.LedgerMonthOf(time.Now()) }

// RemindServerBills tells admins about server bills due by tomorrow, once per bill.
func (t *Tgbot) RemindServerBills(now time.Time) {
	if !t.IsRunning() {
		return
	}
	due, err := t.ledgerService.ServersToRemind(now, 1)
	if err != nil || len(due) == 0 {
		return
	}
	currency, _ := t.settingService.GetTgPaymentCurrency()
	lines := make([]string, 0, len(due))
	for _, bill := range due {
		lines = append(lines, "• "+html.EscapeString(bill.Name)+" — "+
			strings.TrimSpace(strconv.Itoa(bill.Amount)+" "+html.EscapeString(currency))+" · "+bill.Date)
	}
	t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.ledger.reminder")+strings.Join(lines, "\r\n"), t.hideButton())
	for _, bill := range due {
		if err := t.ledgerService.MarkReminded(bill); err != nil {
			logger.Warning("ledger: marking reminder for", bill.Name, "failed:", err)
		}
	}
}
