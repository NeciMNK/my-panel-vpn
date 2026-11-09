package tgbot

import (
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	cbClientPaid = "client_paid"
	// paymentReportWindow keeps a client's repeated "I paid" taps from paging every admin.
	paymentReportWindow = 10 * time.Minute
	paymentDateLayout   = "2006-01-02 15:04"
)

type paymentInfo struct {
	amount  string
	details string
}

// billed is false while the admin has set neither field: the panel then bills nobody.
func (p paymentInfo) billed() bool {
	return p.amount != "" || p.details != ""
}

func (t *Tgbot) loadPaymentInfo() paymentInfo {
	amount, err := t.settingService.GetTgPaymentAmount()
	if err != nil {
		logger.Warning("Unable to read tgPaymentAmount:", err)
	}
	details, err := t.settingService.GetTgPaymentDetails()
	if err != nil {
		logger.Warning("Unable to read tgPaymentDetails:", err)
	}
	return paymentInfo{amount: strings.TrimSpace(amount), details: strings.TrimSpace(details)}
}

// nextPaymentDue is the earliest fixed expiry among the subscriptions; a
// countdown that has not started (negative) or no expiry at all sets no date.
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
	if due, ok := nextPaymentDue(traffics); ok {
		if due.Before(time.Now()) {
			b.WriteString(t.I18nBot("tgbot.payment.overdue", "Date=="+due.Format(paymentDateLayout)))
		} else {
			b.WriteString(t.I18nBot("tgbot.payment.dueOn", "Date=="+due.Format(paymentDateLayout)))
		}
	}
	if info.amount != "" {
		b.WriteString(t.I18nBot("tgbot.payment.amount", "Amount=="+info.amount))
	}
	if info.details != "" {
		b.WriteString(t.I18nBot("tgbot.payment.details", "Details=="+info.details))
	}
	return b.String()
}

func (t *Tgbot) paidButtonRow() []telego.InlineKeyboardButton {
	return tu.InlineKeyboardRow(t.btn("tgbot.buttons.iPaid", cbClientPaid))
}

// sendExpiryReminder is the client's pre-expiry notice; when billed it says
// where to pay and lets the client report the payment from the notice itself.
func (t *Tgbot) sendExpiryReminder(chatID int64, text string, due []*xray.ClientTraffic) {
	info := t.loadPaymentInfo()
	if !info.billed() {
		t.sendNotice(chatID, text)
		return
	}
	rows := [][]telego.InlineKeyboardButton{t.paidButtonRow()}
	rows = append(rows, t.hideButton().InlineKeyboard...)
	t.SendMsgToTgbot(chatID, text+"\r\n"+t.paymentBlock(info, due), tu.InlineKeyboard(rows...))
}

type paymentReportStore struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

var paymentReports = &paymentReportStore{last: map[int64]time.Time{}}

// allow records a report from tgUserID unless one already landed inside the window.
func (s *paymentReportStore) allow(tgUserID int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.last[tgUserID]; ok && now.Sub(prev) < paymentReportWindow {
		return false
	}
	s.last[tgUserID] = now
	return true
}

func payerLabel(user telego.User) string {
	id := strconv.FormatInt(user.ID, 10)
	if user.Username != "" {
		return "@" + user.Username + " (" + id + ")"
	}
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name + " (" + id + ")"
	}
	return id
}

// reportClientPayment pages every admin with a renew button per subscription
// the client owns, so confirming a payment is one tap from the notice.
func (t *Tgbot) reportClientPayment(callbackQuery *telego.CallbackQuery) {
	from := callbackQuery.From
	traffics, err := t.inboundService.GetClientTrafficTgBot(from.ID)
	if err != nil || len(traffics) == 0 {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
		return
	}
	if !paymentReports.allow(from.ID, time.Now()) {
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentAlreadyReported"))
		return
	}
	emails := make([]string, 0, len(traffics))
	rows := make([][]telego.InlineKeyboardButton, 0, len(traffics)+1)
	for _, traffic := range traffics {
		emails = append(emails, html.EscapeString(traffic.Email))
		rows = append(rows, tu.InlineKeyboardRow(t.btn2(
			t.I18nBot("tgbot.buttons.renewClient", "Email=="+traffic.Email),
			t.encodeQuery("reset_exp "+traffic.Email))))
	}
	rows = append(rows, t.hideButton().InlineKeyboard...)
	msg := t.I18nBot("tgbot.messages.paymentReported", "User=="+payerLabel(from)) + strings.Join(emails, ", ")
	t.SendMsgToTgbotAdmins(msg, tu.InlineKeyboard(rows...))
	t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.paymentReported"))
}
