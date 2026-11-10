package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedBillingClient(t *testing.T, email string, port int) model.ClientRecord {
	t.Helper()
	c := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", SubID: email, Enable: true}
	ib := mkInbound(t, port, model.VLESS, clientsSettings(t, []model.Client{c}))
	if err := (&ClientService{}).SyncInbound(nil, ib.Id, []model.Client{c}); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	return lookupClientRecord(t, email)
}

func billingRows(t *testing.T, clientID int) int64 {
	t.Helper()
	var cnt int64
	if err := database.GetDB().Model(&model.ClientBilling{}).Where("client_id = ?", clientID).Count(&cnt).Error; err != nil {
		t.Fatalf("count billing: %v", err)
	}
	return cnt
}

// A zero row read for a new client and saved twice must stay one row holding
// the last write, or toggles would fork the client's billing state.
func TestSaveBillingUpsertsByClient(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	rec := seedBillingClient(t, "bill@x", 52101)

	first, err := svc.GetBillingByEmail("bill@x")
	if err != nil {
		t.Fatalf("GetBillingByEmail: %v", err)
	}
	first.Trusted = true
	if err := svc.SaveBilling(first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := svc.GetBillingByEmail("bill@x")
	if err != nil {
		t.Fatalf("GetBillingByEmail: %v", err)
	}
	second.GraceDays = 15
	if err := svc.SaveBilling(&model.ClientBilling{ClientId: rec.Id, Trusted: second.Trusted, GraceDays: second.GraceDays}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	if n := billingRows(t, rec.Id); n != 1 {
		t.Fatalf("billing rows = %d, want 1", n)
	}
	got, _ := svc.GetBillingByEmail("bill@x")
	if !got.Trusted || got.GraceDays != 15 {
		t.Errorf("billing = %+v, want trusted with 15 grace days", got)
	}
}

// A deleted client's billing must go with it: a new client reusing the email
// must not inherit "trusted" or open credit.
func TestDeleteClientRemovesBilling(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	rec := seedBillingClient(t, "billing-deleted@x", 52102)
	if err := svc.SaveBilling(&model.ClientBilling{ClientId: rec.Id, Trusted: true}); err != nil {
		t.Fatalf("seed billing: %v", err)
	}

	if _, err := svc.Delete(&InboundService{}, rec.Id, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := billingRows(t, rec.Id); n != 0 {
		t.Errorf("Delete left %d billing row(s) behind", n)
	}
}

func TestBulkDeleteRemovesBilling(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	rec := seedBillingClient(t, "billing-bulk-deleted@x", 52103)
	if err := svc.SaveBilling(&model.ClientBilling{ClientId: rec.Id, Trusted: true}); err != nil {
		t.Fatalf("seed billing: %v", err)
	}

	if _, _, err := svc.BulkDelete(&InboundService{}, []string{"billing-bulk-deleted@x"}, false); err != nil {
		t.Fatalf("BulkDelete: %v", err)
	}

	if n := billingRows(t, rec.Id); n != 0 {
		t.Errorf("BulkDelete left %d billing row(s) behind", n)
	}
}
