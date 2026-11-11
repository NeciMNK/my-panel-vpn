package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const yooKassaAPI = "https://api.yookassa.ru/v3"

// YooKassa is the YooKassa (ЮKassa) REST API: a redirect payment page, then polling.
type YooKassa struct {
	shopID  string
	secret  string
	baseURL string
	client  *http.Client
}

// NewYooKassa uses the live API when baseURL is empty; tests point it at a fake.
func NewYooKassa(shopID, secret, baseURL string) *YooKassa {
	if baseURL == "" {
		baseURL = yooKassaAPI
	}
	return &YooKassa{
		shopID: shopID, secret: secret, baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (y *YooKassa) Name() string { return ProviderYooKassa }

type yooAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type yooPayment struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	Paid         bool      `json:"paid"`
	Amount       yooAmount `json:"amount"`
	Confirmation struct {
		Type            string `json:"type"`
		ConfirmationURL string `json:"confirmation_url"`
	} `json:"confirmation"`
}

type yooError struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

func (y *YooKassa) Verify(ctx context.Context) (Account, error) {
	var me struct {
		AccountID string `json:"account_id"`
		Test      bool   `json:"test"`
	}
	if err := y.do(ctx, http.MethodGet, "/me", "", nil, &me); err != nil {
		return Account{}, err
	}
	return Account{ID: me.AccountID, Test: me.Test}, nil
}

func (y *YooKassa) CreateInvoice(ctx context.Context, order Order) (Invoice, error) {
	if order.Amount <= 0 {
		return Invoice{}, fmt.Errorf("yookassa: amount must be positive, got %d", order.Amount)
	}
	body := map[string]any{
		"amount":       yooAmount{Value: strconv.Itoa(order.Amount) + ".00", Currency: strings.ToUpper(order.Currency)},
		"capture":      true,
		"confirmation": map[string]string{"type": "redirect", "return_url": order.ReturnURL},
		"description":  truncateRunes(order.Description, 128),
	}
	var created yooPayment
	if err := y.do(ctx, http.MethodPost, "/payments", order.IdempotenceKey, body, &created); err != nil {
		return Invoice{}, err
	}
	if created.ID == "" || created.Confirmation.ConfirmationURL == "" {
		return Invoice{}, fmt.Errorf("yookassa: payment %q came back without a payment page", created.ID)
	}
	return Invoice{ID: created.ID, URL: created.Confirmation.ConfirmationURL}, nil
}

func (y *YooKassa) GetPayment(ctx context.Context, invoiceID string) (Payment, error) {
	var got yooPayment
	if err := y.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(invoiceID), "", nil, &got); err != nil {
		return Payment{}, err
	}
	minor, err := parseMinor(got.Amount.Value)
	if err != nil {
		return Payment{}, fmt.Errorf("yookassa: payment %s: %w", invoiceID, err)
	}
	p := Payment{AmountMinor: minor, Currency: got.Amount.Currency}
	switch got.Status {
	case "succeeded":
		p.Status = StatusPaid
	case "canceled":
		p.Status = StatusCanceled
	default:
		// waiting_for_capture cannot happen with capture=true; it stays pending like "pending".
		p.Status = StatusPending
	}
	return p, nil
}

func (y *YooKassa) do(ctx context.Context, method, path, idempotenceKey string, in, out any) error {
	var reader io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, y.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(y.shopID, y.secret)
	req.Header.Set("Content-Type", "application/json")
	if idempotenceKey != "" {
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}
	resp, err := y.client.Do(req)
	if err != nil {
		return fmt.Errorf("yookassa: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("yookassa: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr yooError
		_ = json.Unmarshal(raw, &apiErr)
		return fmt.Errorf("yookassa: HTTP %d %s: %s", resp.StatusCode, apiErr.Code, apiErr.Description)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("yookassa: bad response: %w", err)
	}
	return nil
}

// parseMinor reads a decimal like "300.00" or "5" into minor units (kopecks).
func parseMinor(value string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(value), ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("amount %q has more than two decimals", value)
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", value, err)
	}
	cents := int64(0)
	if frac != "" {
		frac += strings.Repeat("0", 2-len(frac))
		if cents, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, fmt.Errorf("amount %q: %w", value, err)
		}
	}
	return units*100 + cents, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
