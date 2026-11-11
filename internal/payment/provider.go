// Package payment talks to acquiring providers: it opens a payment page for an
// order and later reads whether the money arrived. It knows nothing of clients.
package payment

import (
	"context"
	"errors"
	"strings"
)

// Status is a payment's state at the provider, reduced to what the panel acts on.
type Status int

const (
	StatusPending Status = iota
	StatusPaid
	StatusCanceled
)

// Order is what the customer is asked to pay; Amount is in whole currency units.
type Order struct {
	Amount      int
	Currency    string
	Description string
	ReturnURL   string
	// IdempotenceKey makes a retried create return the same payment instead of a second one.
	IdempotenceKey string
}

// Invoice is a payment the provider opened: ID to poll it, URL for the customer.
type Invoice struct {
	ID  string
	URL string
}

// Payment is the provider's view of an invoice; AmountMinor (kopecks, cents) and
// Currency are what it charged, so a caller can refuse one that differs from its order.
type Payment struct {
	Status      Status
	AmountMinor int64
	Currency    string
}

// Matches reports whether the payment charged exactly the order's amount and currency.
func (p Payment) Matches(amount int, currency string) bool {
	return p.AmountMinor == int64(amount)*100 && strings.EqualFold(p.Currency, currency)
}

// Account is the shop the credentials open; Test marks a sandbox that moves no money.
type Account struct {
	ID   string
	Test bool
}

// Provider is one acquiring service. A new one implements this and is added to New.
type Provider interface {
	Name() string
	// Verify proves the credentials work without charging anyone.
	Verify(ctx context.Context) (Account, error)
	CreateInvoice(ctx context.Context, order Order) (Invoice, error)
	GetPayment(ctx context.Context, invoiceID string) (Payment, error)
}

// Config is the panel's provider setting: which one and its credentials.
type Config struct {
	Provider string
	ShopID   string
	Secret   string
}

const ProviderYooKassa = "yookassa"

var (
	ErrNotConfigured   = errors.New("payment provider is not configured")
	ErrUnknownProvider = errors.New("unknown payment provider")
)

// New builds the configured provider; ErrNotConfigured means "manual payments only".
func New(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "":
		return nil, ErrNotConfigured
	case ProviderYooKassa:
		if cfg.ShopID == "" || cfg.Secret == "" {
			return nil, ErrNotConfigured
		}
		return NewYooKassa(cfg.ShopID, cfg.Secret, ""), nil
	default:
		return nil, ErrUnknownProvider
	}
}
