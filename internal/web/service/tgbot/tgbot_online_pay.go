package tgbot

import (
	"context"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	cbPayOnline   = "pay_card"
	cbPayCheck    = "pay_chk"
	cbFreeBuy     = "free_buy"
	invoiceWindow = 30 * time.Second
)

// invoiceOpens spaces invoice creation per user: each one is a provider API call.
var invoiceOpens = newActionThrottle(5 * time.Second)

// answerOnlinePayment handles the card-payment taps of clients and admins alike;
// false means data is not one of them. Each tap proves it is the caller's own.
func (t *Tgbot) answerOnlinePayment(query *telego.CallbackQuery, data string) bool {
	parts := strings.Fields(data)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case cbPayOnline:
		t.answerSilent(query.ID)
		t.onlinePayStep(query, parts[1:])
	case cbPayCheck:
		t.checkOnlinePayment(query, parts[1:])
	default:
		return false
	}
	return true
}

// onlinePayStep walks subscription -> term -> invoice: "pay_card", "pay_card <email>", "pay_card <email> <months>".
func (t *Tgbot) onlinePayStep(query *telego.CallbackQuery, args []string) {
	chatID, userID := query.Message.GetChat().ID, query.From.ID
	traffics, err := t.inboundService.GetClientTrafficTgBot(userID)
	if err != nil || len(traffics) == 0 {
		return
	}
	if len(args) == 0 && len(traffics) == 1 {
		args = []string{traffics[0].Email}
	}
	if len(args) == 0 {
		rows := make([][]telego.InlineKeyboardButton, 0, len(traffics))
		for _, traffic := range traffics {
			rows = append(rows, tu.InlineKeyboardRow(t.btn2(traffic.Email, t.encodeQuery(cbPayOnline+" "+traffic.Email))))
		}
		t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.payment.chooseSubscription"), rows...))
		return
	}
	email := args[0]
	if !slices.ContainsFunc(traffics, func(c *xray.ClientTraffic) bool { return c.Email == email }) {
		return
	}
	info := t.loadPaymentInfo()
	if len(args) == 1 {
		billing := t.billingFor(email)
		rows := make([][]telego.InlineKeyboardButton, 0, len(info.plans))
		for _, plan := range info.plans {
			label := t.I18nBot("tgbot.buttons.planOption", "Months=="+strconv.Itoa(plan.Months)) + " · " + info.planTotal(plan, billing)
			rows = append(rows, tu.InlineKeyboardRow(t.btn2(label,
				t.encodeQuery(cbPayOnline+" "+email+" "+strconv.Itoa(plan.Months)))))
		}
		t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.payment.chooseOnlinePlan", "Email=="+html.EscapeString(email)), rows...))
		return
	}
	months, err := strconv.Atoi(args[1])
	if _, listed := info.plan(months); err != nil || !listed {
		return
	}
	if !invoiceOpens.allow(userID, time.Now()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), invoiceWindow)
	defer cancel()
	desc := t.I18nBot("tgbot.payment.orderDescription", "Email=="+email, "Months=="+strconv.Itoa(months))
	order, err := t.paymentOrderService.CreatePlanOrder(ctx, userID, email, months, desc, t.paymentReturnURL())
	if err != nil {
		logger.Warning("online payment: opening an invoice for", email, "failed:", err)
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.paymentUnavailable"))
		return
	}
	text := t.I18nBot("tgbot.payment.invoice", "Email=="+html.EscapeString(email),
		"Months=="+strconv.Itoa(months), "Total=="+orderTotal(order))
	t.renderScreen(chatID, t.newScreen("pay", text, t.invoiceRows(order)...))
}

func orderTotal(order *model.PaymentOrder) string {
	return strconv.Itoa(order.Amount) + " " + order.Currency
}

func (t *Tgbot) invoiceRows(order *model.PaymentOrder) [][]telego.InlineKeyboardButton {
	return [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(
			t.I18nBot("tgbot.buttons.payNow", "Total=="+orderTotal(order))).WithURL(order.PayURL)),
		tu.InlineKeyboardRow(t.btn("tgbot.buttons.checkPayment", cbPayCheck+" "+strconv.Itoa(order.Id))),
	}
}

// paymentReturnURL brings the customer back to the bot after paying.
func (t *Tgbot) paymentReturnURL() string {
	if name := botUsername(); name != "" {
		return "https://t.me/" + name
	}
	return "https://t.me/"
}

// checkOnlinePayment is the customer's "I paid" on an invoice: it asks the
// provider now instead of waiting for the next poll.
func (t *Tgbot) checkOnlinePayment(query *telego.CallbackQuery, args []string) {
	id := 0
	if len(args) == 1 {
		id, _ = strconv.Atoi(args[0])
	}
	order, err := t.paymentOrderService.GetOrder(id)
	if err != nil || order.TgID != query.From.ID {
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), invoiceWindow)
	defer cancel()
	out := t.paymentOrderService.CheckOrder(ctx, order, time.Now())
	if out.Fulfilled {
		t.sendCallbackAnswerTgBot(query.ID, "")
		t.announceOrder(out)
		return
	}
	switch order.Status {
	case service.OrderPending:
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.answers.paymentPending"))
	case service.OrderDone, service.OrderPaid:
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.answers.paymentAlreadyApplied"))
	default:
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.answers.paymentCanceled"))
	}
}

// CheckPaymentOrders polls every open invoice; the cron job runs it so a client
// who paid and never came back to the bot still gets the term.
func (t *Tgbot) CheckPaymentOrders(now time.Time) {
	orders, err := t.paymentOrderService.PendingOrders(now)
	if err != nil || len(orders) == 0 || !t.paymentOrderService.Online() {
		return
	}
	for i := range orders {
		ctx, cancel := context.WithTimeout(context.Background(), invoiceWindow)
		out := t.paymentOrderService.CheckOrder(ctx, &orders[i], now)
		cancel()
		if out.Fulfilled {
			t.announceOrder(out)
		} else if out.Err != nil {
			logger.Warning("online payment: checking order", orders[i].Id, "failed:", out.Err)
		}
	}
}

// announceOrder tells the customer and the admins about an order this caller
// fulfilled; a paid order that could not be applied is left to the admins.
func (t *Tgbot) announceOrder(out service.OrderOutcome) {
	order := out.Order
	total := orderTotal(order)
	user := t.tgUserLabel(order.TgID)
	if out.Err != nil {
		logger.Warning("online payment: order", order.Id, "was paid but not applied:", out.Err)
		t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.onlinePaymentFailed", "Id=="+strconv.Itoa(order.Id),
			"User=="+user, "Total=="+total, "Error=="+html.EscapeString(out.Err.Error())), t.hideButton())
		t.sendNotice(order.TgID, t.I18nBot("tgbot.messages.onlinePaymentDelayed"))
		return
	}
	email := html.EscapeString(order.Email)
	if order.Kind == service.OrderAI {
		logger.Infof("tgbot: %s bought the AI button (%s) for %s", user, total, order.Email)
		t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.aiExitBoughtAdmin", "User=="+user, "Email=="+email, "Total=="+total), t.hideButton())
		t.sendNotice(order.TgID, t.I18nBot("tgbot.messages.aiExitBought"))
		t.screenClientHome(order.TgID, order.TgID, screenSendNew)
		return
	}
	if order.Kind == service.OrderFree {
		logger.Infof("tgbot: %s bought the free plan online (%s) as %s", user, total, order.Email)
		t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.onlineFreeAdmin", "User=="+user, "Email=="+email, "Total=="+total), t.hideButton())
		t.sendNotice(order.TgID, t.I18nBot("tgbot.messages.freePaid", "GB=="+strconv.Itoa(t.freePlanGB())))
		t.screenClientHome(order.TgID, order.TgID, screenSendNew)
		return
	}
	until := t.I18nBot("tgbot.unlimited")
	if out.Result != nil && out.Result.ExpiryTime > 0 {
		until = time.UnixMilli(out.Result.ExpiryTime).Format(paymentDate)
	}
	logger.Infof("tgbot: %s paid %d mo (%s) online for %s, expiry now %s", user, order.Months, total, order.Email, until)
	paymentReports.forget(order.TgID)
	t.closePaymentNotices(order.Email, t.I18nBot("tgbot.messages.paidOnlineLine", "Email=="+email, "Date=="+until))
	t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.onlinePaymentAdmin", "User=="+user, "Email=="+email,
		"Months=="+strconv.Itoa(order.Months), "Total=="+total), t.hideButton())
	t.sendNotice(order.TgID, t.I18nBot("tgbot.messages.paymentConfirmed", "Email=="+email, "Date=="+until))
}

// tgUserLabel names a Telegram user by id; the job has no message to read a username from.
func (t *Tgbot) tgUserLabel(tgID int64) string {
	freeRequestNames.Lock()
	username := freeRequestNames.byID[tgID]
	freeRequestNames.Unlock()
	if username != "" {
		return "@" + username + " (" + strconv.FormatInt(tgID, 10) + ")"
	}
	return strconv.FormatInt(tgID, 10)
}

// buyFreePlan is a stranger's tap on the paid free plan: it opens the invoice;
// the client is created once the provider reports the payment.
func (t *Tgbot) buyFreePlan(query *telego.CallbackQuery) {
	from := query.From
	if !t.freePlanEnabled() || t.paymentOrderService.FreePlanPrice() <= 0 {
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	if !invoiceOpens.allow(from.ID, time.Now()) {
		t.sendCallbackAnswerTgBot(query.ID, "")
		return
	}
	t.answerSilent(query.ID)
	freeRequestNames.Lock()
	freeRequestNames.byID[from.ID] = from.Username
	freeRequestNames.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), invoiceWindow)
	defer cancel()
	gb := strconv.Itoa(t.freePlanGB())
	order, err := t.paymentOrderService.CreateFreePlanOrder(ctx, from.ID, freeEmail(from.Username, from.ID),
		t.I18nBot("tgbot.payment.freeDescription", "GB=="+gb), t.paymentReturnURL())
	chatID := query.Message.GetChat().ID
	if err != nil {
		logger.Warning("online payment: opening the free plan invoice for", from.ID, "failed:", err)
		t.sendNotice(chatID, t.I18nBot("tgbot.answers.paymentUnavailable"))
		return
	}
	text := t.I18nBot("tgbot.payment.freeInvoice", "GB=="+gb, "Total=="+orderTotal(order))
	t.SendMsgToTgbot(chatID, text, tu.InlineKeyboard(t.invoiceRows(order)...))
}
