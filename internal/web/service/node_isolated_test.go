package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// The master cannot reach an isolated node, so an edit must queue for the
// reconcile instead of spending a push timeout per call on it.
func TestEditOnIsolatedNodeQueuesInsteadOfPushing(t *testing.T) {
	setupConflictDB(t)
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{APIPort: func() int { return 0 }, SetNeedRestart: func() {}}))
	t.Cleanup(func() { runtime.SetManager(nil) })
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":[]}`))
	}))
	t.Cleanup(srv.Close)
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	portNum, _ := strconv.Atoi(port)

	db := database.GetDB()
	node := &model.Node{
		Name: "iso", Scheme: "http", Address: host, Port: portNum, BasePath: "/", ApiToken: "tok",
		Enable: true, Status: model.NodeStatusIsolated, AllowPrivateAddress: true,
	}
	if err := db.Create(node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	id := node.Id
	ib := &model.Inbound{
		UserId: 1, NodeID: &id, Tag: "in-443-tcp", Enable: true, Port: 443, Protocol: model.VLESS,
		Settings: `{"clients":[{"email":"a@x","enable":true}]}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	if _, err := (&ClientService{}).DelInboundClientByEmail(&InboundService{}, ib.Id, "a@x", false, false); err != nil {
		t.Fatalf("DelInboundClientByEmail: %v", err)
	}

	if n := hits.Load(); n != 0 {
		t.Fatalf("master sent %d requests to a node it cannot reach", n)
	}
	if _, _, dirty, _, err := (&NodeService{}).NodeSyncState(id); err != nil || !dirty {
		t.Fatalf("NodeSyncState dirty=%v err=%v, want the edit queued for reconcile", dirty, err)
	}
}
