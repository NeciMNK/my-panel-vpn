package service

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
)

const (
	PaymentMonthDays = 30
	maxPaymentMonths = 36
	dayMillis        = int64(24 * time.Hour / time.Millisecond)
)

var (
	ErrPaymentMonths = errors.New("months must be 1-36")
	ErrNoClient      = errors.New("client not found")
)

// BillingService applies client payments; the Telegram bot and the API share it
// so a payment extends the term, settles credit and books income the same way.
type BillingService struct {
	settingService SettingService
	clientService  ClientService
	inboundService InboundService
	ledgerService  LedgerService
	xrayService    XrayService
}

// PaymentPlanPrice is one payable term with this client's total in the payment currency (0 without a price).
type PaymentPlanPrice struct {
	Months          int `json:"months" example:"3"`
	DiscountPercent int `json:"discountPercent" example:"10"`
	Total           int `json:"total" example:"810"`
}

// ClientBillingView is one client's payment state for the API.
type ClientBillingView struct {
	Email         string             `json:"email" example:"alice"`
	ExpiryTime    int64              `json:"expiryTime" example:"1767225600000"`
	Trusted       bool               `json:"trusted" example:"false"`
	FreePlan      bool               `json:"freePlan" example:"false"`
	NonPaying     bool               `json:"nonPaying" example:"false"`
	GraceDays     int                `json:"graceDays" example:"0"`
	PendingMonths int                `json:"pendingMonths" example:"0"`
	Friend        bool               `json:"friend" example:"false"`
	Credit        int                `json:"credit" example:"0"`
	Currency      string             `json:"currency" example:"RUB"`
	Plans         []PaymentPlanPrice `json:"plans"`
}

// PaymentResult is what one applied payment changed.
type PaymentResult struct {
	Email      string `json:"email" example:"alice"`
	Months     int    `json:"months" example:"3"`
	ExpiryTime int64  `json:"expiryTime" example:"1774915200000"`
	Amount     int    `json:"amount" example:"810"`
	Currency   string `json:"currency" example:"RUB"`
}

// PaymentPlans reads the price, currency and discounted terms; bad discounts fall back to one month.
func (s *BillingService) PaymentPlans() (price int, currency string, plans []entity.PaymentPlan) {
	price, err := s.settingService.GetTgPaymentPrice()
	if err != nil {
		logger.Warning("Unable to read tgPaymentPrice:", err)
	}
	currency, _ = s.settingService.GetTgPaymentCurrency()
	discounts, _ := s.settingService.GetTgPaymentDiscounts()
	if plans, err = entity.ParsePaymentPlans(discounts); err != nil {
		logger.Warning("Ignoring invalid tgPaymentDiscounts:", err)
		plans = []entity.PaymentPlan{{Months: 1}}
	}
	return price, strings.TrimSpace(currency), plans
}

// PaymentAmount is the price of months: a listed term's discounted total, else months at full price.
func (s *BillingService) PaymentAmount(months int) (int, string) {
	price, currency, plans := s.PaymentPlans()
	return termTotal(price, plans, months), currency
}

func termTotal(price int, plans []entity.PaymentPlan, months int) int {
	if i := slices.IndexFunc(plans, func(p entity.PaymentPlan) bool { return p.Months == months }); i >= 0 {
		return plans[i].Total(price)
	}
	return months * price
}

// FriendDiscount is the admin-set friends percent, clamped to 0-99.
func (s *BillingService) FriendDiscount() int {
	pct, _ := s.settingService.GetTgFriendDiscount()
	return min(max(pct, 0), 99)
}

// ClientPrice is what this client owes for a term's total: friends get their
// percent off, then free-plan credit paid online comes off, never below zero.
func ClientPrice(total int, billing *model.ClientBilling, friendDiscount int) int {
	if billing.Friend && friendDiscount > 0 {
		total = (total*(100-friendDiscount) + 50) / 100
	}
	return max(total-max(billing.Credit, 0), 0)
}

// ClientPaymentAmount is what the client pays for months now, with its own discounts.
func (s *BillingService) ClientPaymentAmount(billing *model.ClientBilling, months int) (int, string) {
	total, currency := s.PaymentAmount(months)
	return ClientPrice(total, billing, s.FriendDiscount()), currency
}

func (s *BillingService) GetClientBilling(email string) (*ClientBillingView, error) {
	traffic, err := s.inboundService.GetClientTrafficByEmail(email)
	if err != nil {
		return nil, err
	}
	if traffic == nil {
		return nil, ErrNoClient
	}
	billing, err := s.clientService.GetBillingByEmail(email)
	if err != nil {
		return nil, err
	}
	price, currency, plans := s.PaymentPlans()
	friendDiscount := s.FriendDiscount()
	view := &ClientBillingView{
		Email: email, ExpiryTime: traffic.ExpiryTime, Trusted: billing.Trusted, FreePlan: billing.FreePlan,
		NonPaying: billing.NonPaying, GraceDays: billing.GraceDays, PendingMonths: billing.PendingMonths,
		Friend: billing.Friend, Credit: billing.Credit, Currency: currency, Plans: make([]PaymentPlanPrice, 0, len(plans)),
	}
	for _, plan := range plans {
		view.Plans = append(view.Plans, PaymentPlanPrice{
			Months: plan.Months, DiscountPercent: plan.DiscountPercent,
			Total: ClientPrice(plan.Total(price), billing, friendDiscount),
		})
	}
	return view, nil
}

// PaidExpiry is the expiry after paying for days: credit already granted is
// repaid first, and a lapsed subscription restarts from now, not from its end.
func PaidExpiry(expiry int64, billing *model.ClientBilling, days int, now int64) int64 {
	switch {
	case expiry > 0:
		base := max(expiry-int64(billing.GraceDays)*dayMillis, now)
		return base + int64(days)*dayMillis
	case expiry < 0:
		return expiry - int64(days)*dayMillis
	case billing.FreePlan:
		return now + int64(days)*dayMillis
	default:
		return 0
	}
}

// ApplyPayment extends the client's term by months, lifts a free plan's quota,
// settles credit, drops any pending bot report and books the client's price under note.
func (s *BillingService) ApplyPayment(email string, months int, note string, now time.Time) (*PaymentResult, error) {
	return s.applyPayment(email, months, -1, note, now)
}

// ApplyOnlinePayment is ApplyPayment for money the provider took: it books the
// amount actually charged, even if prices changed while the client paid.
func (s *BillingService) ApplyOnlinePayment(email string, months, amount int, note string, now time.Time) (*PaymentResult, error) {
	return s.applyPayment(email, months, max(amount, 0), note, now)
}

func (s *BillingService) applyPayment(email string, months, paid int, note string, now time.Time) (*PaymentResult, error) {
	if months < 1 || months > maxPaymentMonths {
		return nil, ErrPaymentMonths
	}
	traffic, err := s.inboundService.GetClientTrafficByEmail(email)
	if err != nil {
		return nil, err
	}
	if traffic == nil {
		return nil, ErrNoClient
	}
	billing, err := s.clientService.GetBillingByEmail(email)
	if err != nil {
		return nil, err
	}
	amount, currency := s.ClientPaymentAmount(billing, months)
	if paid >= 0 {
		amount = paid
	}
	expiry := PaidExpiry(traffic.ExpiryTime, billing, months*PaymentMonthDays, now.UnixMilli())
	if expiry != traffic.ExpiryTime {
		if err := s.applyClientEdit(s.clientService.ResetClientExpiryTimeByEmail(&s.inboundService, email, expiry)); err != nil {
			return nil, err
		}
	}
	if billing.FreePlan {
		if err := s.applyClientEdit(s.clientService.ResetClientTrafficLimitByEmail(&s.inboundService, email, 0)); err != nil {
			return nil, err
		}
	}
	billing.FreePlan, billing.GraceDays, billing.Credit = false, 0, 0
	if err := s.clientService.SaveBilling(billing); err != nil {
		logger.Warning("payment: saving billing for", email, "failed:", err)
	}
	if err := s.clientService.MarkPaid(email); err != nil {
		logger.Warning("payment: moving", email, "to the paid group failed:", err)
	}
	if err := database.GetDB().Model(&model.ClientBilling{}).Where("client_id = ?", billing.ClientId).
		Update("pending_months", 0).Error; err != nil {
		logger.Warning("payment: clearing the pending report for", email, "failed:", err)
	}
	result := &PaymentResult{Email: email, Months: months, ExpiryTime: expiry, Amount: amount, Currency: currency}
	if result.Amount > 0 {
		if err := s.ledgerService.RecordPayment(email, result.Amount, note, now); err != nil {
			logger.Warning("ledger: booking payment for", email, "failed:", err)
		}
	}
	return result, nil
}

func (s *BillingService) applyClientEdit(needRestart bool, err error) error {
	if needRestart {
		s.xrayService.SetToNeedRestart()
	}
	return err
}
