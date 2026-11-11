package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request must carry shop credentials, the idempotence key and a decimal
// amount, or YooKassa rejects it / opens a second payment on a retry.
func TestYooKassaCreateInvoiceSendsWhatTheAPIRequires(t *testing.T) {
	var got struct {
		user, pass, key string
		body            map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/payments" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		got.user, got.pass, _ = r.BasicAuth()
		got.key = r.Header.Get("Idempotence-Key")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = w.Write([]byte(`{"id":"2f1","status":"pending","confirmation":{"type":"redirect","confirmation_url":"https://yoomoney.ru/checkout/2f1"}}`))
	}))
	defer srv.Close()

	inv, err := NewYooKassa("shop1", "live_s3cret", srv.URL).CreateInvoice(context.Background(), Order{
		Amount: 295, Currency: "rub", Description: "VPN 1 mo", ReturnURL: "https://t.me/bot", IdempotenceKey: "order-7",
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if inv != (Invoice{ID: "2f1", URL: "https://yoomoney.ru/checkout/2f1"}) {
		t.Errorf("invoice = %+v", inv)
	}
	if got.user != "shop1" || got.pass != "live_s3cret" || got.key != "order-7" {
		t.Errorf("auth/key = %q %q %q", got.user, got.pass, got.key)
	}
	amount, _ := got.body["amount"].(map[string]any)
	if amount["value"] != "295.00" || amount["currency"] != "RUB" || got.body["capture"] != true {
		t.Errorf("body = %v", got.body)
	}
}

func TestYooKassaGetPaymentStatus(t *testing.T) {
	cases := []struct {
		reply string
		want  Payment
	}{
		{`{"id":"p","status":"succeeded","paid":true,"amount":{"value":"5.00","currency":"RUB"}}`, Payment{StatusPaid, 500, "RUB"}},
		{`{"id":"p","status":"pending","amount":{"value":"300.5","currency":"RUB"}}`, Payment{StatusPending, 30050, "RUB"}},
		{`{"id":"p","status":"canceled","amount":{"value":"5","currency":"RUB"}}`, Payment{StatusCanceled, 500, "RUB"}},
	}
	for _, tc := range cases {
		t.Run(tc.reply, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/payments/p" {
					t.Errorf("path = %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer srv.Close()
			got, err := NewYooKassa("s", "k", srv.URL).GetPayment(context.Background(), "p")
			if err != nil || got != tc.want {
				t.Errorf("GetPayment = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// A wrong secret must surface YooKassa's own reason, so the admin knows what to fix.
func TestYooKassaErrorCarriesTheAPIReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_credentials","description":"Login or password is incorrect"}`))
	}))
	defer srv.Close()
	_, err := NewYooKassa("s", "bad", srv.URL).GetPayment(context.Background(), "p")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401 invalid_credentials: Login or password is incorrect") {
		t.Errorf("err = %v", err)
	}
}

// The panel's "check connection" must tell a sandbox shop from a live one.
func TestYooKassaVerifyReadsTheShop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, _ := r.BasicAuth(); r.URL.Path != "/me" || user != "shop1" {
			t.Errorf("request = %s as %q", r.URL.Path, user)
		}
		_, _ = w.Write([]byte(`{"account_id":"shop1","test":true,"status":"enabled"}`))
	}))
	defer srv.Close()
	acc, err := NewYooKassa("shop1", "test_k", srv.URL).Verify(context.Background())
	if err != nil || acc != (Account{ID: "shop1", Test: true}) {
		t.Errorf("Verify = %+v, %v", acc, err)
	}
}

func TestPaymentMatchesOrder(t *testing.T) {
	paid := Payment{StatusPaid, 29500, "RUB"}
	if !paid.Matches(295, "rub") {
		t.Error("exact amount refused")
	}
	if paid.Matches(300, "RUB") || paid.Matches(295, "USD") {
		t.Error("a different amount or currency matched")
	}
}
