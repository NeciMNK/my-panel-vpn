package tgbot

import (
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	cbClientPaid  = "client_paid"
	cbClientDefer = "client_defer"
	cbFreeRequest = "free_req"

	// graceDays is how far one deferral moves the expiry; at most one per graceCooldownDays.
	graceDays         = 15
	graceCooldownDays = 30
	freePlanGB        = 10
	monthDays         = 30
	dayMillis         = int64(24 * time.Hour / time.Millisecond)
	paymentDate       = "2006-01-02 15:04"
	freeEmailMax      = 32
)

var (
	paymentReports = newActionThrottle(10 * time.Minute)
	freeRequests   = newActionThrottle(time.Hour)
	freeEmailChars = regexp.MustCompile(`[^a-z0-9_.-]`)
)

type paymentInfo struct {
	price    int
	currency string
	plans    []entity.PaymentPlan
	details  string
}

// billed is false while the admin set neither a price nor details: the panel then bills nobody.
func (p paymentInfo) billed() bool {
	return p.price > 0 || p.details != ""
}

func (p paymentInfo) money(amount int) string {
	return strings.TrimSpace(strconv.Itoa(amount) + " " + p.currency)
}

func (p paymentInfo) plan(months int) (entity.PaymentPlan, bool) {
	i := slices.IndexFunc(p.plans, func(plan entity.PaymentPlan) bool { return plan.Months == months })
	if i < 0 {
		return entity.PaymentPlan{}, false
	}
	return p.plans[i], true
}

// planTotal is the plan's price as text, or "" when no price is set.
func (p paymentInfo) planTotal(plan entity.PaymentPlan) string {
	if p.price <= 0 {
		return ""
	}
	return p.money(plan.Total(p.price))
}

func (t *Tgbot) loadPaymentInfo() paymentInfo {
	info := paymentInfo{}
	var err error
	if info.price, err = t.settingService.GetTgPaymentPrice(); err != nil {
		logger.Warning("Unable to read tgPaymentPrice:", err)
	}
	currency, _ := t.settingService.GetTgPaymentCurrency()
	details, _ := t.settingService.GetTgPaymentDetails()
	discounts, _ := t.settingService.GetTgPaymentDiscounts()
	info.currency = strings.TrimSpace(currency)
	info.details = strings.TrimSpace(details)
	if info.plans, err = entity.ParsePaymentPlans(discounts); err != nil {
		logger.Warning("Ignoring invalid tgPaymentDiscounts:", err)
		info.plans = []entity.PaymentPlan{{Months: 1}}
	}
	return info
}

// billingFor never fails the screen it feeds: a missing row reads as an ordinary client.
func (t *Tgbot) billingFor(email string) *model.ClientBilling {
	billing, err := t.clientService.GetBillingByEmail(email)
	if err != nil {
		logger.Warning("Unable to read billing for", email, err)
		return &model.ClientBilling{}
	}
	return billing
}

// nextPaymentDue is the earliest fixed expiry; a countdown not yet started
// (negative) or no expiry at all sets no date.
func nextPaymentDue(traffics []*xray.ClientTraffic) (time.Time, bool) {
	var earliest int64
	for _, traffic := range traffics {
		if traffic.ExpiryTime > 0 && (earliest == 0 || traffic.ExpiryTime < earliest) {
			earliest = traffic.ExpiryTime
		}
	}
	if earliest == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(earliest), true
}

// paymentBlock tells a client when, how much and where to pay; empty when unbilled.
func (t *Tgbot) paymentBlock(info paymentInfo, traffics []*xray.ClientTraffic) string {
	if !info.billed() {
		return ""
	}
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.payment.title"))
	free, deferred := false, false
	for _, traffic := range traffics {
		billing := t.billingFor(traffic.Email)
		free = free || billing.FreePlan
		deferred = deferred || billing.GraceDays > 0
	}
	if free {
		b.WriteString(t.I18nBot("tgbot.payment.freePlan", "GB=="+strconv.Itoa(freePlanGB)))
	}
	if due, ok := nextPaymentDue(traffics); ok {
		switch {
		case due.Before(time.Now()):
			b.WriteString(t.I18nBot("tgbot.payment.overdue", "Date=="+due.Format(paymentDate)))
		case deferred:
			b.WriteString(t.I18nBot("tgbot.payment.deferredUntil", "Date=="+due.Format(paymentDate)))
		default:
			b.WriteString(t.I18nBot("tgbot.payment.dueOn", "Date=="+due.Format(paymentDate)))
		}
	}
	if info.price > 0 {
		b.WriteString(t.I18nBot("tgbot.payment.plansTitle"))
		for _, plan := range info.plans {
			b.WriteString(t.planLine(info, plan))
		}
	}
	if info.details != "" {
		b.WriteString(t.I18nBot("tgbot.payment.details", "Details=="+info.details))
	}
	return b.String()
}

func (t *Tgbot) planLine(info paymentInfo, plan entity.PaymentPlan) string {
	params := []string{"Months==" + strconv.Itoa(plan.Months), "Total==" + info.planTotal(plan)}
	if plan.DiscountPercent > 0 {
		return t.I18nBot("tgbot.payment.planDiscounted", append(params, "Percent=="+strconv.Itoa(plan.DiscountPercent))...)
	}
	return t.I18nBot("tgbot.payment.plan", params...)
}

// deferrable lists the subscriptions a trusted client may put on credit now:
// ending within graceDays (or ended) and not deferred in the last graceCooldownDays.
func (t *Tgbot) deferrable(traffics []*xray.ClientTraffic) []*xray.ClientTraffic {
	now := time.Now().UnixMilli()
	var out []*xray.ClientTraffic
	for _, traffic := range traffics {
		if traffic.ExpiryTime <= 0 || traffic.ExpiryTime-now > graceDays*dayMillis {
			continue
		}
		billing := t.billingFor(traffic.Email)
		if billing.Trusted && now-billing.LastGraceAt >= graceCooldownDays*dayMillis {
			out = append(out, traffic)
		}
	}
	return out
}

// paymentRows are the client's own payment actions under its screen or reminder.
func (t *Tgbot) paymentRows(traffics []*xray.ClientTraffic) [][]telego.InlineKeyboardButton {
	rows := [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(t.btn("tgbot.buttons.iPaid", cbClientPaid))}
	if len(t.deferrable(traffics)) > 0 {
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(
			t.I18nBot("tgbot.buttons.payLater", "Days=="+strconv.Itoa(graceDays)), cbClientDefer)))
	}
	return rows
}

// sendExpiryReminder is the client's pre-expiry notice; when billed it says
// where to pay and lets the client act on the notice itself.
func (t *Tgbot) sendExpiryReminder(chatID int64, text string, due []*xray.ClientTraffic) {
	info := t.loadPaymentInfo()
	if !info.billed() {
		t.sendNotice(chatID, text)
		return
	}
	rows := append(t.paymentRows(due), t.hideButton().InlineKeyboard...)
	t.SendMsgToTgbot(chatID, text+"\r\n"+t.paymentBlock(info, due), tu.InlineKeyboard(rows...))
}

type actionThrottle struct {
	window time.Duration
	mu     sync.Mutex
	last   map[int64]time.Time
}

func newActionThrottle(window time.Duration) *actionThrottle {
	return &actionThrottle{window: window, last: map[int64]time.Time{}}
}

// allow records an action by tgUserID unless one already landed inside the window.
func (s *actionThrottle) allow(tgUserID int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.last[tgUserID]; ok && now.Sub(prev) < s.window {
		return false
	}
	s.last[tgUserID] = now
	return true
}

func (s *actionThrottle) forget(tgUserID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.last, tgUserID)
}

func userLabel(user telego.User) string {
	id := strconv.FormatInt(user.ID, 10)
	if user.Username != "" {
		return "@" + user.Username + " (" + id + ")"
	}
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name + " (" + id + ")"
	}
	return id
}

// screenPlanPicker asks which term the client paid for, so the admin confirms
// exactly that term instead of guessing it from the amount received.
func (t *Tgbot) screenPlanPicker(chatID int64) {
	info := t.loadPaymentInfo()
	rows := make([][]telego.InlineKeyboardButton, 0, len(info.plans))
	for _, plan := range info.plans {
		label := t.I18nBot("tgbot.buttons.planOption", "Months=="+strconv.Itoa(plan.Months))
		if total := info.planTotal(plan); total != "" {
			label += " · " + total
		}
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(label, cbClientPaid+" "+strconv.Itoa(plan.Months))))
	}
	t.renderScreen(chatID, t.newScreen("pay", t.I18nBot("tgbot.payment.choosePlan"), rows...))
}

// reportClientPayment pages every admin with a one-tap confirmation per
// subscription the client owns, for the term the client says it paid.
func (t *Tgbot) reportClientPayment(callbackQuery *telego.CallbackQuery, monthsText string) {
	from := callbackQuery.From
	info := t.loadPaymentInfo()
	months, _ := strconv.Atoi(monthsText)
	plan, known := info.plan(months)
	traffics, err := t.inboundService.GetClientTrafficTgBot(from.ID)
	if !known || err != nil || len(traffics) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	if !paymentReports.allow(from.ID, time.Now()) {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentAlreadyReported"))
		return
	}
	total := info.planTotal(plan)
	if total == "" {
		total = "—"
	}
	emails := make([]string, 0, len(traffics))
	rows := make([][]telego.InlineKeyboardButton, 0, len(traffics)+1)
	for _, traffic := range traffics {
		if err := t.clientService.SetPendingPayment(t.billingFor(traffic.Email).ClientId, months); err != nil {
			logger.Warning("reportClientPayment: saving the pending report failed:", err)
			continue
		}
		emails = append(emails, html.EscapeString(traffic.Email))
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(
			t.I18nBot("tgbot.buttons.confirmPayment", "Email=="+traffic.Email, "Months=="+monthsText),
			t.encodeQuery("pay_ok "+traffic.Email+" "+monthsText))))
	}
	rows = append(rows, t.hideButton().InlineKeyboard...)
	msg := t.I18nBot("tgbot.messages.paymentReported",
		"User=="+userLabel(from), "Months=="+monthsText, "Total=="+total) + strings.Join(emails, ", ")
	t.SendMsgToTgbotAdmins(msg, tu.InlineKeyboard(rows...))
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentReported"))
	t.screenClientHome(callbackQuery.Message.GetChat().ID, from.ID)
}

// paidExpiry is the expiry after paying for days: credit already granted is
// repaid first, and a lapsed subscription restarts from now, not from its end.
func paidExpiry(expiry int64, billing *model.ClientBilling, days int, now int64) int64 {
	switch {
	case expiry > 0:
		base := expiry - int64(billing.GraceDays)*dayMillis
		if base < now {
			base = now
		}
		return base + int64(days)*dayMillis
	case expiry < 0:
		return expiry - int64(days)*dayMillis
	case billing.FreePlan:
		return now + int64(days)*dayMillis
	default:
		return 0
	}
}

// confirmPayment is the admin's tap on a payment report: it extends the term,
// lifts a free plan's quota and settles any credit, then tells the client.
func (t *Tgbot) confirmPayment(callbackQuery *telego.CallbackQuery, email, monthsText string) {
	chatID := callbackQuery.Message.GetChat().ID
	months, err := strconv.Atoi(monthsText)
	traffic, trafficErr := t.inboundService.GetClientTrafficByEmail(email)
	billing, billingErr := t.clientService.GetBillingByEmail(email)
	if err != nil || months <= 0 || trafficErr != nil || traffic == nil || billingErr != nil {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	claimed, err := t.clientService.ClaimPendingPayment(billing.ClientId, months)
	if err != nil || !claimed {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentAlreadyConfirmed", "Email=="+email))
		return
	}
	expiry := paidExpiry(traffic.ExpiryTime, billing, months*monthDays, time.Now().UnixMilli())
	if expiry != traffic.ExpiryTime {
		needRestart, err := t.clientService.ResetClientExpiryTimeByEmail(&t.inboundService, email, expiry)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			logger.Warning("confirmPayment: expiry update failed:", err)
			t.restorePendingPayment(billing.ClientId, months)
			t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
			return
		}
	}
	if billing.FreePlan {
		needRestart, err := t.clientService.ResetClientTrafficLimitByEmail(&t.inboundService, email, 0)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			logger.Warning("confirmPayment: lifting the free quota failed:", err)
			t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
			return
		}
	}
	billing.FreePlan, billing.GraceDays = false, 0
	if err := t.clientService.SaveBilling(billing); err != nil {
		logger.Warning("confirmPayment: saving billing failed:", err)
	}
	tgID := t.clientTgID(email)
	paymentReports.forget(tgID)
	if tgID != 0 {
		until := t.I18nBot("tgbot.unlimited")
		if expiry > 0 {
			until = time.UnixMilli(expiry).Format(paymentDate)
		}
		t.sendNotice(tgID, t.I18nBot("tgbot.messages.paymentConfirmed", "Email=="+email, "Date=="+until))
	}
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentConfirmed", "Email=="+email))
	t.searchClientScreen(chatID, email)
}

// restorePendingPayment re-arms a claimed report whose term was not applied, so an admin can retry it.
func (t *Tgbot) restorePendingPayment(clientID, months int) {
	if err := t.clientService.SetPendingPayment(clientID, months); err != nil {
		logger.Warning("confirmPayment: restoring the pending report failed:", err)
	}
}

func (t *Tgbot) clientTgID(email string) int64 {
	_, client, err := t.inboundService.GetClientByEmail(email)
	if err != nil || client == nil {
		return 0
	}
	return client.TgID
}

// deferPayment puts a trusted client's ending subscriptions on graceDays of
// credit; the next confirmed payment repays it, and admins hear of it now.
func (t *Tgbot) deferPayment(callbackQuery *telego.CallbackQuery) {
	from := callbackQuery.From
	traffics, err := t.inboundService.GetClientTrafficTgBot(from.ID)
	eligible := t.deferrable(traffics)
	if err != nil || len(eligible) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.graceUnavailable"))
		return
	}
	now := time.Now().UnixMilli()
	var granted []string
	var until int64
	for _, traffic := range eligible {
		expiry := traffic.ExpiryTime + graceDays*dayMillis
		if traffic.ExpiryTime < now {
			expiry = now + graceDays*dayMillis
		}
		needRestart, err := t.clientService.ResetClientExpiryTimeByEmail(&t.inboundService, traffic.Email, expiry)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			logger.Warning("deferPayment: expiry update failed for", traffic.Email, err)
			continue
		}
		billing := t.billingFor(traffic.Email)
		billing.GraceDays += graceDays
		billing.LastGraceAt = now
		if err := t.clientService.SaveBilling(billing); err != nil {
			logger.Warning("deferPayment: saving billing failed:", err)
		}
		granted = append(granted, html.EscapeString(traffic.Email))
		if until == 0 || expiry < until {
			until = expiry
		}
	}
	if len(granted) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.graceTaken",
		"User=="+userLabel(from), "Days=="+strconv.Itoa(graceDays))+strings.Join(granted, ", "), t.hideButton())
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.graceGranted",
		"Days=="+strconv.Itoa(graceDays), "Date=="+time.UnixMilli(until).Format(paymentDate)))
	t.screenClientHome(callbackQuery.Message.GetChat().ID, from.ID)
}

// trustedRow is the client card's switch for deferred payment.
func (t *Tgbot) trustedRow(email string) []telego.InlineKeyboardButton {
	state := t.I18nBot("tgbot.messages.no")
	if t.billingFor(email).Trusted {
		state = t.I18nBot("tgbot.messages.yes")
	}
	return tu.InlineKeyboardRow(t.btn2(
		t.I18nBot("tgbot.buttons.trusted", "State=="+strings.TrimSpace(state)), t.encodeQuery("trust "+email)))
}

func (t *Tgbot) toggleTrusted(callbackQuery *telego.CallbackQuery, email string) {
	billing, err := t.clientService.GetBillingByEmail(email)
	if err == nil {
		billing.Trusted = !billing.Trusted
		err = t.clientService.SaveBilling(billing)
	}
	if err != nil {
		logger.Warning("toggleTrusted:", err)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
	t.searchClientScreen(callbackQuery.Message.GetChat().ID, email)
}

func (t *Tgbot) freePlanEnabled() bool {
	enabled, err := t.settingService.GetTgFreePlanEnable()
	return err == nil && enabled
}

// sendStrangerWelcome gives an unbound account its ChatID and, when the panel
// offers a free plan, a way to ask an admin for one.
func (t *Tgbot) sendStrangerWelcome(chatID, userID int64) {
	text := t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(userID, 10))
	if !t.freePlanEnabled() {
		t.sendNotice(chatID, text)
		return
	}
	rows := append([][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(t.btn2(t.I18nBot("tgbot.buttons.requestFree", "GB=="+strconv.Itoa(freePlanGB)), cbFreeRequest)),
	}, t.hideButton().InlineKeyboard...)
	t.SendMsgToTgbot(chatID, text, tu.InlineKeyboard(rows...))
}

var freeRequestNames = struct {
	sync.Mutex
	byID map[int64]string
}{byID: map[int64]string{}}

// requestFreePlan is the one tap a stranger may make: it pages the admins,
// who approve by creating the client, so nothing is granted here.
func (t *Tgbot) requestFreePlan(callbackQuery *telego.CallbackQuery) {
	from := callbackQuery.From
	if !t.freePlanEnabled() {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	if !freeRequests.allow(from.ID, time.Now()) {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.freeAlreadyRequested"))
		return
	}
	freeRequestNames.Lock()
	freeRequestNames.byID[from.ID] = from.Username
	freeRequestNames.Unlock()
	id := strconv.FormatInt(from.ID, 10)
	rows := append([][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(
		t.btn("tgbot.buttons.approve", "free_ok "+id),
		t.btn("tgbot.buttons.decline", "free_no "+id),
	)}, t.hideButton().InlineKeyboard...)
	t.SendMsgToTgbotAdmins(t.I18nBot("tgbot.messages.freeRequested",
		"User=="+userLabel(from), "GB=="+strconv.Itoa(freePlanGB)), tu.InlineKeyboard(rows...))
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.freeRequested"))
}

// freeEmail names the new client after the Telegram username when it makes a
// valid email-like id, else after the numeric id, which is always unique.
func freeEmail(username string, tgID int64) string {
	name := freeEmailChars.ReplaceAllString(strings.ToLower(username), "")
	if name == "" || len(name) > freeEmailMax {
		return "tg" + strconv.FormatInt(tgID, 10)
	}
	return name
}

// approveFreePlan opens the add-client wizard prefilled for the free plan: the
// admin still picks the inbounds, and the plan is applied on submit.
func (t *Tgbot) approveFreePlan(chatID int64, actor chatUser, tgIDText string) bool {
	tgID, err := strconv.ParseInt(tgIDText, 10, 64)
	if err != nil || tgID <= 0 {
		return false
	}
	freeRequestNames.Lock()
	username := freeRequestNames.byID[tgID]
	freeRequestNames.Unlock()
	addClientDrafts.reset(actor)
	draft := addClientDrafts.forActor(actor)
	draft.Lock()
	defer draft.Unlock()
	t.resetDraft(draft)
	draft.email = freeEmail(username, tgID)
	draft.tgID = tgIDText
	draft.totalGB = freePlanGB * 1024 * 1024 * 1024
	draft.freePlan = true
	t.addClient(chatID, draft, t.BuildClientDraftMessage(draft))
	return true
}

func (t *Tgbot) declineFreePlan(callbackQuery *telego.CallbackQuery, tgIDText string) {
	tgID, err := strconv.ParseInt(tgIDText, 10, 64)
	if err != nil || tgID <= 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	freeRequestNames.Lock()
	delete(freeRequestNames.byID, tgID)
	freeRequestNames.Unlock()
	t.sendNotice(tgID, t.I18nBot("tgbot.messages.freeDeclined"))
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
}

// markFreePlan runs after the wizard created a free client: the billing row is
// what later lets a payment lift the quota.
func (t *Tgbot) markFreePlan(email string) error {
	billing, err := t.clientService.GetBillingByEmail(email)
	if err != nil {
		return fmt.Errorf("free plan billing for %s: %w", email, err)
	}
	billing.FreePlan = true
	return t.clientService.SaveBilling(billing)
}
