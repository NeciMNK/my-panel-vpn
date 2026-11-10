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

// PaymentPlanPrice is one payable term with its total in the payment currency (0 without a price).
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
	if i := slices.IndexFunc(plans, func(p entity.PaymentPlan) bool { return p.Months == months }); i >= 0 {
		return plans[i].Total(price), currency
	}
	return months * price, currency
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
	view := &ClientBillingView{
		Email: email, ExpiryTime: traffic.ExpiryTime, Trusted: billing.Trusted, FreePlan: billing.FreePlan,
		NonPaying: billing.NonPaying, GraceDays: billing.GraceDays, PendingMonths: billing.PendingMonths,
		Currency: currency, Plans: make([]PaymentPlanPrice, 0, len(plans)),
	}
	for _, plan := range plans {
		view.Plans = append(view.Plans, PaymentPlanPrice{Months: plan.Months, DiscountPercent: plan.DiscountPercent, Total: plan.Total(price)})
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
// settles credit, drops any pending bot report and books the income under note.
func (s *BillingService) ApplyPayment(email string, months int, note string, now time.Time) (*PaymentResult, error) {
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
	billing.FreePlan, billing.GraceDays = false, 0
	if err := s.clientService.SaveBilling(billing); err != nil {
		logger.Warning("payment: saving billing for", email, "failed:", err)
	}
	if err := database.GetDB().Model(&model.ClientBilling{}).Where("client_id = ?", billing.ClientId).
		Update("pending_months", 0).Error; err != nil {
		logger.Warning("payment: clearing the pending report for", email, "failed:", err)
	}
	result := &PaymentResult{Email: email, Months: months, ExpiryTime: expiry}
	result.Amount, result.Currency = s.PaymentAmount(months)
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
