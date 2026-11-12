package service

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/testpg"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Two HA masters share one Postgres and each runs its own traffic poll; the
// in-process serial writer cannot order them, so the SQL itself must not lose a tick.
func TestAddTrafficTwoMastersPostgres(t *testing.T) {
	db := initTwoMastersDB(t)

	const inboundCount, clientCount, rounds = 12, 24, 60
	var forward []*xray.Traffic
	for i := range inboundCount {
		tag := fmt.Sprintf("in-%02d", i)
		ib := &model.Inbound{UserId: 1, Tag: tag, Enable: true, Port: 44000 + i, Protocol: model.VLESS, Settings: `{"clients":[]}`}
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("seed inbound: %v", err)
		}
		forward = append(forward, &xray.Traffic{IsInbound: true, Tag: tag, Up: 1, Down: 2})
	}
	var forwardClients []*xray.ClientTraffic
	for i := range clientCount {
		email := fmt.Sprintf("c%02d@x", i)
		if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: email, Enable: true}).Error; err != nil {
			t.Fatalf("seed client traffic: %v", err)
		}
		forwardClients = append(forwardClients, &xray.ClientTraffic{Email: email, Up: 3, Down: 4})
	}
	backward := slices.Clone(forward)
	slices.Reverse(backward)
	backwardClients := slices.Clone(forwardClients)
	slices.Reverse(backwardClients)

	deadlocksBefore := pgDeadlockCount(t, db)
	svc := &InboundService{}
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, report := range []struct {
		inbounds []*xray.Traffic
		clients  []*xray.ClientTraffic
	}{{forward, forwardClients}, {backward, backwardClients}} {
		wg.Go(func() {
			for range rounds {
				if _, _, _, _, _, err := svc.addTrafficLocked(report.inbounds, report.clients); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a master lost a traffic tick: %v", err)
	}
	// Retries would hide it, but two polls locking in one order never deadlock.
	if n := pgDeadlockCount(t, db) - deadlocksBefore; n != 0 {
		t.Errorf("two masters' polls deadlocked %d times; rows are not locked in one order", n)
	}

	var inbounds []model.Inbound
	if err := db.Where("tag LIKE ?", "in-%").Find(&inbounds).Error; err != nil {
		t.Fatal(err)
	}
	for _, ib := range inbounds {
		if ib.Up != 2*rounds || ib.Down != 4*rounds {
			t.Errorf("inbound %s: up=%d down=%d, want %d/%d", ib.Tag, ib.Up, ib.Down, 2*rounds, 4*rounds)
		}
	}
	var clients []xray.ClientTraffic
	if err := db.Where("email LIKE ?", "c%@x").Find(&clients).Error; err != nil {
		t.Fatal(err)
	}
	for _, ct := range clients {
		if ct.Up != 6*rounds || ct.Down != 8*rounds {
			t.Errorf("client %s: up=%d down=%d, want %d/%d", ct.Email, ct.Up, ct.Down, 6*rounds, 8*rounds)
		}
	}
}

// The other master's write here locks client_traffics before inbounds (an admin
// edit's order), so no ordering can avoid the deadlock: the poll must rerun.
func TestAddTrafficRetriesDeadlockVictimPostgres(t *testing.T) {
	db := initTwoMastersDB(t)
	ib := &model.Inbound{UserId: 1, Tag: "in-victim", Enable: true, Port: 44100, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: ib.Id, Email: "victim@x", Enable: true}).Error; err != nil {
		t.Fatalf("seed client traffic: %v", err)
	}

	other := db.Begin()
	// Long timeout here makes Postgres pick the poll, which waits first, as the victim.
	mustExec(t, other, "SET LOCAL deadlock_timeout = '30s'")
	mustExec(t, other, "UPDATE client_traffics SET up = up WHERE email = ?", "victim@x")

	pollErr := make(chan error, 1)
	go func() {
		_, _, _, _, _, err := (&InboundService{}).addTrafficLocked(
			[]*xray.Traffic{{IsInbound: true, Tag: "in-victim", Up: 10, Down: 20}},
			[]*xray.ClientTraffic{{Email: "victim@x", Up: 30, Down: 40}},
		)
		pollErr <- err
	}()
	waitForLockWaiter(t, db)
	mustExec(t, other, "UPDATE inbounds SET up = up WHERE tag = ?", "in-victim")
	if err := other.Commit().Error; err != nil {
		t.Fatalf("other master commit: %v", err)
	}

	if err := <-pollErr; err != nil {
		t.Fatalf("poll lost its tick to a deadlock with the other master: %v", err)
	}
	var reloaded model.Inbound
	if err := db.First(&reloaded, ib.Id).Error; err != nil {
		t.Fatal(err)
	}
	ct := readTraffic(t, db, "victim@x")
	if reloaded.Up != 10 || reloaded.Down != 20 || ct.Up != 30 || ct.Down != 40 {
		t.Fatalf("inbound %d/%d client %d/%d, want 10/20 and 30/40", reloaded.Up, reloaded.Down, ct.Up, ct.Down)
	}
}

func initTwoMastersDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("XUI_DB_TYPE") != "postgres" || os.Getenv("XUI_DB_DSN") == "" {
		t.Skip("set XUI_DB_TYPE=postgres and XUI_DB_DSN")
	}
	cleanup, err := testpg.IsolatePackage("two_masters")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return initTrafficTestDB(t)
}

func mustExec(t *testing.T, tx *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := tx.Exec(sql, args...).Error; err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func waitForLockWaiter(t *testing.T, db *gorm.DB) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var waiting int64
		if err := db.Raw("SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()").Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
	}
	t.Fatal("the poll never blocked on the other master's row lock")
}

func pgDeadlockCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	_ = db.Exec("SELECT pg_stat_clear_snapshot()").Error
	var n int64
	if err := db.Raw("SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}
