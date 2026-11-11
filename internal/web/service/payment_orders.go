package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/payment"
	"github.com/mhsanaei/3x-ui/v3/internal/util/random"

	"gorm.io/gorm"
)

const (
	OrderPlan = "plan"
	OrderFree = "free"

	OrderPending  = "pending"
	OrderPaid     = "paid"
	OrderDone     = "done"
	OrderCanceled = "canceled"
	OrderFailed   = "failed"

	// orderLifetime is how long an unpaid invoice is polled before it is dropped.
	orderLifetime   = 24 * time.Hour
	defaultCurrency = "RUB"
)

var (
	ErrNothingToPay    = errors.New("nothing to pay: the price is zero")
	ErrFreePlanOffline = errors.New("the paid free plan needs a payment provider, a price and inbounds")
)

// NewPaymentProvider builds the configured provider; tests swap it for a fake.
var NewPaymentProvider = payment.New

// PaymentOrderService sells plans and the paid free plan through the acquiring
// provider and applies them once the provider reports the money arrived.
type PaymentOrderService struct {
	settingService SettingService
	clientService  ClientService
	inboundService InboundService
	billingService BillingService
	ledgerService  LedgerService
}

func (s *PaymentOrderService) provider() (payment.Provider, error) {
	return NewPaymentProvider(s.settingService.GetPaymentConfig())
}

// Online reports whether payments go through a provider instead of an admin's confirm.
func (s *PaymentOrderService) Online() bool {
	_, err := s.provider()
	return err == nil
}

// FreePlanPrice is the online free plan's price, or 0 when it cannot be sold online
// (no provider, no price or no inbounds to create the client on).
func (s *PaymentOrderService) FreePlanPrice() int {
	price, _ := s.settingService.GetTgFreePlanPrice()
	if price <= 0 || len(s.settingService.GetTgFreePlanInbounds()) == 0 || !s.Online() {
		return 0
	}
	return price
}

// VerifyProvider checks the saved provider credentials against the provider.
func (s *PaymentOrderService) VerifyProvider(ctx context.Context) (payment.Account, error) {
	provider, err := s.provider()
	if err != nil {
		return payment.Account{}, err
	}
	return provider.Verify(ctx)
}

func (s *PaymentOrderService) currency() string {
	currency, _ := s.settingService.GetTgPaymentCurrency()
	if currency = strings.TrimSpace(currency); currency == "" {
		return defaultCurrency
	}
	return currency
}

// CreatePlanOrder opens a payment page for months of the client's subscription,
// priced for that client (friends discount, free-plan credit).
func (s *PaymentOrderService) CreatePlanOrder(ctx context.Context, tgID int64, email string, months int, description, returnURL string) (*model.PaymentOrder, error) {
	if months < 1 || months > maxPaymentMonths {
		return nil, ErrPaymentMonths
	}
	billing, err := s.clientService.GetBillingByEmail(email)
	if err != nil {
		return nil, err
	}
	amount, _ := s.billingService.ClientPaymentAmount(billing, months)
	order := &model.PaymentOrder{Kind: OrderPlan, TgID: tgID, Email: email, Months: months, Amount: amount}
	return s.open(ctx, order, description, returnURL)
}

// CreateFreePlanOrder opens the payment page for the paid free plan; email is the
// name the client gets once paid. An unpaid order of the same user is reused.
func (s *PaymentOrderService) CreateFreePlanOrder(ctx context.Context, tgID int64, email, description, returnURL string) (*model.PaymentOrder, error) {
	price := s.FreePlanPrice()
	if price <= 0 {
		return nil, ErrFreePlanOffline
	}
	var open model.PaymentOrder
	err := database.GetDB().Where("kind = ? AND tg_id = ? AND status = ? AND amount = ? AND created_at > ?",
		OrderFree, tgID, OrderPending, price, time.Now().Add(-orderLifetime).UnixMilli()).
		Order("id DESC").Limit(1).Find(&open).Error
	if err == nil && open.Id > 0 && open.PayURL != "" {
		return &open, nil
	}
	order := &model.PaymentOrder{Kind: OrderFree, TgID: tgID, Email: email, Amount: price}
	return s.open(ctx, order, description, returnURL)
}

func (s *PaymentOrderService) open(ctx context.Context, order *model.PaymentOrder, description, returnURL string) (*model.PaymentOrder, error) {
	provider, err := s.provider()
	if err != nil {
		return nil, err
	}
	if order.Amount <= 0 {
		return nil, ErrNothingToPay
	}
	order.Provider, order.Currency, order.Status = provider.Name(), s.currency(), OrderPending
	db := database.GetDB()
	if err := db.Create(order).Error; err != nil {
		return nil, err
	}
	invoice, err := provider.CreateInvoice(ctx, payment.Order{
		Amount: order.Amount, Currency: order.Currency, Description: description, ReturnURL: returnURL,
		IdempotenceKey: "xui-order-" + strconv.Itoa(order.Id) + "-" + strconv.FormatInt(order.CreatedAt, 10),
	})
	if err != nil {
		db.Model(order).Update("status", OrderFailed)
		return nil, err
	}
	order.InvoiceId, order.PayURL = invoice.ID, invoice.URL
	if err := db.Model(order).Updates(map[string]any{"invoice_id": invoice.ID, "pay_url": invoice.URL}).Error; err != nil {
		return nil, err
	}
	return order, nil
}

func (s *PaymentOrderService) GetOrder(id int) (*model.PaymentOrder, error) {
	order := &model.PaymentOrder{}
	if err := database.GetDB().First(order, id).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// PendingOrders are the unpaid orders still worth polling; older ones are canceled here.
func (s *PaymentOrderService) PendingOrders(now time.Time) ([]model.PaymentOrder, error) {
	db := database.GetDB()
	cutoff := now.Add(-orderLifetime).UnixMilli()
	if err := db.Model(&model.PaymentOrder{}).Where("status = ? AND created_at <= ?", OrderPending, cutoff).
		Update("status", OrderCanceled).Error; err != nil {
		return nil, err
	}
	var orders []model.PaymentOrder
	err := db.Where("status = ? AND invoice_id <> ''", OrderPending).Order("id").Find(&orders).Error
	return orders, err
}

// OrderOutcome is what one check did; Fulfilled is true only for the one caller
// that applied the order, so only it tells the customer and admins.
type OrderOutcome struct {
	Order     *model.PaymentOrder
	Fulfilled bool
	Result    *PaymentResult
	Err       error
}

// CheckOrder asks the provider about a pending order and, when it is paid,
// claims and applies it exactly once.
func (s *PaymentOrderService) CheckOrder(ctx context.Context, order *model.PaymentOrder, now time.Time) OrderOutcome {
	out := OrderOutcome{Order: order}
	if order.Status != OrderPending || order.InvoiceId == "" {
		return out
	}
	provider, err := s.provider()
	if err != nil {
		out.Err = err
		return out
	}
	paid, err := provider.GetPayment(ctx, order.InvoiceId)
	if err != nil {
		out.Err = err
		return out
	}
	switch paid.Status {
	case payment.StatusPending:
		return out
	case payment.StatusCanceled:
		s.move(order, OrderPending, OrderCanceled)
		return out
	}
	if !paid.Matches(order.Amount, order.Currency) {
		s.move(order, OrderPending, OrderFailed)
		out.Err = fmt.Errorf("order %d: provider charged %d.%02d %s, expected %d %s", order.Id,
			paid.AmountMinor/100, paid.AmountMinor%100, paid.Currency, order.Amount, order.Currency)
		return out
	}
	if !s.move(order, OrderPending, OrderPaid) {
		return out
	}
	out.Fulfilled = true
	if out.Result, out.Err = s.fulfil(order, now); out.Err != nil {
		s.move(order, OrderPaid, OrderFailed)
		return out
	}
	s.move(order, OrderPaid, OrderDone)
	return out
}

// move is a guarded status change; false means another caller moved it first.
func (s *PaymentOrderService) move(order *model.PaymentOrder, from, to string) bool {
	res := database.GetDB().Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", order.Id, from).Update("status", to)
	if res.Error != nil {
		logger.Warning("payment order", order.Id, from, "->", to, "failed:", res.Error)
		return false
	}
	if res.RowsAffected == 1 {
		order.Status = to
	}
	return res.RowsAffected == 1
}

func (s *PaymentOrderService) fulfil(order *model.PaymentOrder, now time.Time) (*PaymentResult, error) {
	note := fmt.Sprintf("%s #%s", order.Provider, order.InvoiceId)
	if order.Kind == OrderPlan {
		return s.billingService.ApplyOnlinePayment(order.Email, order.Months, order.Amount, note, now)
	}
	email, err := s.createFreeClient(order)
	if err != nil {
		return nil, err
	}
	order.Email = email
	database.GetDB().Model(order).Update("email", email)
	if err := s.ledgerService.RecordPayment(email, order.Amount, note, now); err != nil {
		logger.Warning("ledger: booking the free plan of", email, "failed:", err)
	}
	return &PaymentResult{Email: email, Amount: order.Amount, Currency: order.Currency}, nil
}

// createFreeClient makes the paid free-plan client on the configured inbounds
// and credits what was paid toward its first plan.
func (s *PaymentOrderService) createFreeClient(order *model.PaymentOrder) (string, error) {
	inbounds := s.settingService.GetTgFreePlanInbounds()
	if len(inbounds) == 0 {
		return "", ErrFreePlanOffline
	}
	email := s.freeEmailFor(order)
	gb, err := s.settingService.GetTgFreePlanGB()
	if err != nil || gb <= 0 {
		gb = 10
	}
	client := model.Client{
		Email: email, Enable: true, TotalGB: int64(gb) << 30, SubID: random.NumLower(16), TgID: order.TgID,
		TrafficReset: "monthly", TrafficResetDay: 1,
	}
	needRestart, err := s.clientService.Create(&s.inboundService, &ClientCreatePayload{Client: client, InboundIds: inbounds})
	if needRestart {
		s.billingService.xrayService.SetToNeedRestart()
	}
	if err != nil {
		return "", fmt.Errorf("create free plan client %s: %w", email, err)
	}
	billing, err := s.clientService.GetBillingByEmail(email)
	if err != nil {
		return email, err
	}
	billing.FreePlan, billing.Credit = true, order.Amount
	return email, s.clientService.SaveBilling(billing)
}

// freeEmailFor keeps the name chosen at order time unless a client took it since.
func (s *PaymentOrderService) freeEmailFor(order *model.PaymentOrder) string {
	for _, candidate := range []string{order.Email, "tg" + strconv.FormatInt(order.TgID, 10)} {
		if candidate == "" {
			continue
		}
		if _, err := s.clientService.GetRecordByEmail(nil, candidate); errors.Is(err, gorm.ErrRecordNotFound) {
			return candidate
		}
	}
	return "tg" + strconv.FormatInt(order.TgID, 10) + "-" + random.NumLower(4)
}
