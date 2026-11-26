package service

import (
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A record without credentials (imported with no inbounds) attached to two
// inbounds at once used to get a different secret on each one (DE/NL auth 404).
func TestAttachMintsOneCredentialAcrossInbounds(t *testing.T) {
	protocols := []struct {
		proto    model.Protocol
		settings string
		onWire   func(model.Client) string
		onRecord func(*model.ClientRecord) string
	}{
		{model.Hysteria, `{"clients":[]}`, func(c model.Client) string { return c.Auth }, func(r *model.ClientRecord) string { return r.Auth }},
		{model.VLESS, `{"clients":[]}`, func(c model.Client) string { return c.ID }, func(r *model.ClientRecord) string { return r.UUID }},
		{model.Trojan, `{"clients":[]}`, func(c model.Client) string { return c.Password }, func(r *model.ClientRecord) string { return r.Password }},
		{model.Shadowsocks, `{"method":"aes-256-gcm","clients":[]}`, func(c model.Client) string { return c.Password }, func(r *model.ClientRecord) string { return r.Password }},
		{model.MTProto, `{"clients":[]}`, func(c model.Client) string { return c.Secret }, func(r *model.ClientRecord) string { return r.Secret }},
	}
	attachers := []struct {
		name   string
		attach func(t *testing.T, svc *ClientService, inboundSvc *InboundService, email string, ids []int)
	}{
		{"Attach", func(t *testing.T, svc *ClientService, inboundSvc *InboundService, email string, ids []int) {
			t.Helper()
			if _, err := svc.AttachByEmail(inboundSvc, email, ids); err != nil {
				t.Fatalf("AttachByEmail: %v", err)
			}
		}},
		{"BulkAttach", func(t *testing.T, svc *ClientService, inboundSvc *InboundService, email string, ids []int) {
			t.Helper()
			res, _, err := svc.BulkAttach(inboundSvc, []string{email}, ids)
			if err != nil || len(res.Errors) != 0 {
				t.Fatalf("BulkAttach: err=%v errors=%v", err, res.Errors)
			}
		}},
	}
	for _, p := range protocols {
		for _, a := range attachers {
			t.Run(string(p.proto)+"/"+a.name, func(t *testing.T) {
				setupBulkDB(t)
				svc, inboundSvc := &ClientService{}, &InboundService{}
				const email = "imported@x"
				if err := database.GetDB().Create(&model.ClientRecord{Email: email, SubID: "sub-imported", Enable: true}).Error; err != nil {
					t.Fatalf("seed record: %v", err)
				}
				ids := []int{
					mkInbound(t, 30001, p.proto, p.settings).Id,
					mkInbound(t, 30002, p.proto, p.settings).Id,
					mkInbound(t, 30003, p.proto, p.settings).Id,
				}

				a.attach(t, svc, inboundSvc, email, ids)

				rec, err := svc.GetRecordByEmail(nil, email)
				if err != nil {
					t.Fatalf("GetRecordByEmail: %v", err)
				}
				want := p.onRecord(rec)
				if want == "" {
					t.Fatalf("record has no credential after attach")
				}
				for _, id := range ids {
					ib, err := inboundSvc.GetInbound(id)
					if err != nil {
						t.Fatalf("GetInbound(%d): %v", id, err)
					}
					clients, err := inboundSvc.GetClients(ib)
					if err != nil || len(clients) != 1 {
						t.Fatalf("inbound %d clients = %v, err %v", id, clients, err)
					}
					if got := p.onWire(clients[0]); got != want {
						t.Fatalf("inbound %d credential = %q, record holds %q", id, got, want)
					}
				}
			})
		}
	}
}

// One service or subId-less client in a bulk attach used to fail the whole
// inbound with "Duplicate email", leaving every other client unattached.
func TestBulkAttachSkipsUnattachableClientsOnly(t *testing.T) {
	setupBulkDB(t)
	svc, inboundSvc := &ClientService{}, &InboundService{}
	seed := []model.ClientRecord{
		{Email: "anna@x", SubID: "sub-anna", UUID: "11111111-1111-1111-1111-111111111111", Enable: true},
		{Email: "mp-cascade-3-n2", UUID: "22222222-2222-2222-2222-222222222222", Enable: true},
		{Email: "legacy@x", UUID: "33333333-3333-3333-3333-333333333333", Enable: true},
	}
	for i := range seed {
		if err := database.GetDB().Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].Email, err)
		}
	}
	ib := mkInbound(t, 31001, model.VLESS, `{"clients":[]}`)

	res, _, err := svc.BulkAttach(inboundSvc, []string{"anna@x", "mp-cascade-3-n2", "legacy@x"}, []int{ib.Id})
	if err != nil {
		t.Fatalf("BulkAttach: %v", err)
	}
	if !slices.Equal(res.Attached, []string{"anna@x"}) {
		t.Fatalf("attached = %v, want [anna@x]", res.Attached)
	}
	wantErrors := []string{
		"mp-cascade-3-n2: " + errServiceClientAttach.Error(),
		"legacy@x: " + errAttachWithoutSubID.Error(),
	}
	if !slices.Equal(res.Errors, wantErrors) {
		t.Fatalf("errors = %q, want %q", res.Errors, wantErrors)
	}
	list, err := svc.ListForInbound(nil, ib.Id)
	if err != nil {
		t.Fatalf("ListForInbound: %v", err)
	}
	if got := sortedEmails(list); !slices.Equal(got, []string{"anna@x"}) {
		t.Fatalf("inbound clients = %v, want [anna@x]", got)
	}
}

// Cascade link clients (mp-…) surfaced in /clients/list, inviting users to
// bulk-attach them to regular inbounds.
func TestClientListsHideServiceClients(t *testing.T) {
	svc, inboundSvc, settingSvc := setupPagingServices(t)
	for _, r := range []model.ClientRecord{
		{Email: "anna@x", SubID: "sub-anna", Enable: true},
		{Email: "mp-cascade-3-n2", Enable: true},
		{Email: "mp-home-1-n4", Enable: true},
	} {
		if err := database.GetDB().Create(&r).Error; err != nil {
			t.Fatalf("seed %s: %v", r.Email, err)
		}
	}

	rows, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := make([]string, 0, len(rows))
	for _, r := range rows {
		listed = append(listed, r.Email)
	}
	if !slices.Equal(listed, []string{"anna@x"}) {
		t.Fatalf("List emails = %v, want [anna@x]", listed)
	}

	page, err := svc.ListPaged(inboundSvc, settingSvc, ClientPageParams{PageSize: 50})
	if err != nil {
		t.Fatalf("ListPaged: %v", err)
	}
	if got := pagedEmails(page.Items); !slices.Equal(got, []string{"anna@x"}) {
		t.Fatalf("ListPaged emails = %v, want [anna@x]", got)
	}
	if page.Total != 1 || page.Filtered != 1 || page.Summary.Total != 1 {
		t.Fatalf("total=%d filtered=%d summary.total=%d, want 1/1/1", page.Total, page.Filtered, page.Summary.Total)
	}
}
