package service

import (
	"slices"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// seedLocalClient seeds an enabled local client the way an operator's panel edit leaves it.
func seedLocalClient(t *testing.T, svc *ClientService, port int, c model.Client, up, down int64) *model.Inbound {
	t.Helper()
	c.ID = "22222222-2222-2222-2222-222222222222"
	c.SubID = c.Email
	ib := mkInbound(t, port, model.VLESS, clientsSettings(t, []model.Client{c}))
	if err := svc.SyncInbound(nil, ib.Id, []model.Client{c}); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	mkTraffic(t, ib.Id, c.Email, up, down, c.TotalGB, c.ExpiryTime, c.Enable)
	if err := database.GetDB().Model(xray.ClientTraffic{}).Where("email = ?", c.Email).
		UpdateColumn("reset", c.Reset).Error; err != nil {
		t.Fatalf("seed reset: %v", err)
	}
	return ib
}

func TestMoveExpiredToFreePlan_LifecycleDisabledClientComesBackOnTheFreePlan(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	now := time.Now()
	email := "lapsed@x"
	ib := seedLocalClient(t, svc, 54101, model.Client{
		Email: email, Enable: true, TotalGB: 0, ExpiryTime: now.Add(-time.Hour).UnixMilli(),
	}, 30<<30, 20<<30)
	// The real traffic tick switches the lapsed client off first, as on a live panel.
	if _, _, err := inboundSvc.AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)

	_, moved, err := svc.MoveExpiredToFreePlan(inboundSvc, 10, now)
	if err != nil {
		t.Fatalf("MoveExpiredToFreePlan: %v", err)
	}
	if !slices.Equal(moved, []string{email}) {
		t.Fatalf("moved = %v, want [%s]", moved, email)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, true)
	traffic := trafficOf(t, email)
	if traffic.ExpiryTime != 0 || traffic.Total != 10<<30 || traffic.Up != 0 || traffic.Down != 0 {
		t.Fatalf("traffic = expiry %d total %d up %d down %d, want 0 / 10 GiB / 0 / 0",
			traffic.ExpiryTime, traffic.Total, traffic.Up, traffic.Down)
	}
	rec, err := svc.GetRecordByEmail(nil, email)
	if err != nil {
		t.Fatalf("GetRecordByEmail: %v", err)
	}
	if rec.ExpiryTime != 0 || rec.TotalGB != 10<<30 || rec.TrafficReset != "monthly" || rec.TrafficResetDay != 1 {
		t.Fatalf("record = expiry %d total %d reset %q/%d, want 0 / 10 GiB / monthly/1",
			rec.ExpiryTime, rec.TotalGB, rec.TrafficReset, rec.TrafficResetDay)
	}
	billing, err := svc.GetBillingByEmail(email)
	if err != nil {
		t.Fatalf("GetBillingByEmail: %v", err)
	}
	if !billing.FreePlan {
		t.Fatalf("billing.FreePlan = false, want true so the bot and the ledger see the free plan")
	}
	// A second tick finds nothing: the free plan has no end date to lapse again.
	if _, again, err := svc.MoveExpiredToFreePlan(inboundSvc, 10, now.Add(time.Minute)); err != nil || len(again) != 0 {
		t.Fatalf("second run moved %v (err %v), want none", again, err)
	}
}

func TestMoveExpiredToFreePlan_StillEnabledExpiredClientIsMoved(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	now := time.Now()
	email := "just-lapsed@x"
	ib := seedLocalClient(t, svc, 54102, model.Client{
		Email: email, Enable: true, TotalGB: 100 << 30, ExpiryTime: now.Add(-time.Second).UnixMilli(),
	}, 0, 0)

	if _, moved, err := svc.MoveExpiredToFreePlan(inboundSvc, 4, now); err != nil || !slices.Equal(moved, []string{email}) {
		t.Fatalf("moved %v (err %v), want [%s]", moved, err, email)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, true)
	if got := trafficOf(t, email); got.ExpiryTime != 0 || got.Total != 4<<30 {
		t.Fatalf("traffic = expiry %d total %d, want 0 / 4 GiB", got.ExpiryTime, got.Total)
	}
}

func TestMoveExpiredToFreePlan_LeavesOthersAlone(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	now := time.Now()
	past := now.Add(-time.Hour).UnixMilli()

	seedLocalClient(t, svc, 54111, model.Client{Email: "active@x", Enable: true, ExpiryTime: now.Add(time.Hour).UnixMilli()}, 0, 0)
	seedLocalClient(t, svc, 54112, model.Client{Email: "forever@x", Enable: true}, 0, 0)
	seedLocalClient(t, svc, 54113, model.Client{Email: "delayed@x", Enable: true, ExpiryTime: -30 * dayMillis}, 0, 0)
	seedLocalClient(t, svc, 54114, model.Client{Email: "renews@x", Enable: true, ExpiryTime: past, Reset: 30}, 0, 0)
	// Switched off by the operator before the term ended: that switch must hold.
	manual := seedLocalClient(t, svc, 54115, model.Client{Email: "manual@x", Enable: false, ExpiryTime: past}, 0, 0)
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "manual@x").
		UpdateColumns(map[string]any{"enable": false, "updated_at": past - dayMillis}).Error; err != nil {
		t.Fatalf("backdate manual disable: %v", err)
	}

	_, moved, err := svc.MoveExpiredToFreePlan(inboundSvc, 10, now)
	if err != nil {
		t.Fatalf("MoveExpiredToFreePlan: %v", err)
	}
	if len(moved) != 0 {
		t.Fatalf("moved = %v, want none", moved)
	}
	assertEnableEverywhere(t, svc, inboundSvc, manual.Id, "manual@x", false)
	if got := trafficOf(t, "renews@x").ExpiryTime; got != past {
		t.Fatalf("auto-renewing client expiry = %d, want it left to the renewal (%d)", got, past)
	}
}
