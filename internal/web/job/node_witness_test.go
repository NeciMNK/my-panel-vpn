package job

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

const (
	peerSees = iota
	peerBlind
	peerOldBuild
)

// fakeNode is a node panel: masterReaches gates its status answer to the master,
// verdict is what it says when asked to check another node.
type fakeNode struct {
	node          *model.Node
	masterReaches atomic.Bool
	verdict       atomic.Int32
	mu            sync.Mutex
	asked         []entity.PeerProbeRequest
}

func startFakeNode(t *testing.T, name string) *fakeNode {
	t.Helper()
	f := &fakeNode{}
	f.masterReaches.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "server/status"):
			if !f.masterReaches.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"obj":{"panelGuid":"` + name + `-guid","xray":{"state":"running"}}}`))
		case strings.HasSuffix(r.URL.Path, "server/probePeer"):
			body, _ := io.ReadAll(r.Body)
			var req entity.PeerProbeRequest
			_ = json.Unmarshal(body, &req)
			f.mu.Lock()
			f.asked = append(f.asked, req)
			f.mu.Unlock()
			switch f.verdict.Load() {
			case peerOldBuild:
				w.WriteHeader(http.StatusNotFound)
			case peerSees:
				_, _ = w.Write([]byte(`{"success":true,"obj":{"reachable":true,"port":2053,"latencyMs":40}}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"obj":{"reachable":false,"error":"i/o timeout"}}`))
			}
		default:
			_, _ = w.Write([]byte(`{"success":true,"obj":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	portNum, _ := strconv.Atoi(port)
	f.node = &model.Node{
		Name: name, Scheme: "http", Address: host, Port: portNum, BasePath: "/", ApiToken: "tok",
		Enable: true, Status: model.NodeStatusOnline, AllowPrivateAddress: true, TlsVerifyMode: "verify",
	}
	if err := database.GetDB().Create(f.node).Error; err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	return f
}

func witnessWorld(t *testing.T) (target, witness *fakeNode) {
	t.Helper()
	xuilogger.InitLogger(logging.ERROR)
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{APIPort: func() int { return 0 }, SetNeedRestart: func() {}}))
	t.Cleanup(func() { runtime.SetManager(nil) })
	return startFakeNode(t, "target"), startFakeNode(t, "witness")
}

func nodeState(t *testing.T, id int) (status, seenBy string) {
	t.Helper()
	var n model.Node
	if err := database.GetDB().First(&n, id).Error; err != nil {
		t.Fatalf("reload node %d: %v", id, err)
	}
	return n.Status, n.SeenBy
}

func describeEvent(e eventbus.Event) string {
	out := string(e.Type) + " " + e.Source
	if d, ok := e.Data.(*eventbus.NodeHealthData); ok && len(d.SeenBy) > 0 {
		out += " seen by " + strings.Join(d.SeenBy, ",")
	}
	return out
}

// When the master loses a node it asks the other nodes before burying it: a node
// another node still reaches is isolated (kept for users), one nobody reaches is offline.
func TestHeartbeatAsksOtherNodesBeforeMarkingANodeDead(t *testing.T) {
	type tick struct {
		masterReaches bool
		verdict       int32
		status        string
		seenBy        string
	}
	cases := []struct {
		name   string
		ticks  []tick
		events []string
	}{
		{
			name:   "another node still reaches it",
			ticks:  []tick{{false, peerSees, model.NodeStatusIsolated, "witness"}},
			events: []string{"node.down target seen by witness"},
		},
		{
			name:   "nobody reaches it",
			ticks:  []tick{{false, peerBlind, model.NodeStatusOffline, ""}},
			events: []string{"node.down target"},
		},
		{
			name:   "the other node cannot check yet",
			ticks:  []tick{{false, peerOldBuild, model.NodeStatusOffline, ""}},
			events: []string{"node.down target"},
		},
		{
			name: "isolated node then dies for everyone",
			ticks: []tick{
				{false, peerSees, model.NodeStatusIsolated, "witness"},
				{false, peerBlind, model.NodeStatusOffline, ""},
			},
			events: []string{"node.down target seen by witness", "node.down target"},
		},
		{
			name: "master sees the isolated node again",
			ticks: []tick{
				{false, peerSees, model.NodeStatusIsolated, "witness"},
				{false, peerSees, model.NodeStatusIsolated, "witness"},
				{true, peerSees, model.NodeStatusOnline, ""},
			},
			events: []string{"node.down target seen by witness", "node.up target"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, witness := witnessWorld(t)
			events := collectNodeEvents(t)
			job := NewNodeHeartbeatJob()
			job.witnessRecheck = 0

			for i, tk := range tc.ticks {
				target.masterReaches.Store(tk.masterReaches)
				witness.verdict.Store(tk.verdict)
				job.Run()
				status, seenBy := nodeState(t, target.node.Id)
				if status != tk.status || seenBy != tk.seenBy {
					t.Fatalf("tick %d: target status=%q seenBy=%q, want %q/%q", i+1, status, seenBy, tk.status, tk.seenBy)
				}
			}
			var got []string
			for _, e := range events() {
				got = append(got, describeEvent(e))
			}
			if strings.Join(got, "; ") != strings.Join(tc.events, "; ") {
				t.Fatalf("events = %q, want %q", got, tc.events)
			}
		})
	}
}

// The witness checks the node's panel port and the ports its users connect to.
func TestWitnessChecksPanelAndInboundPorts(t *testing.T) {
	target, witness := witnessWorld(t)
	id := target.node.Id
	for i, port := range []int{443, 8443} {
		ib := &model.Inbound{
			UserId: 1, NodeID: &id, Tag: fmt.Sprintf("in-%d", i), Enable: true, Port: port, Protocol: model.VLESS,
			Settings: `{"clients":[]}`,
		}
		if err := database.GetDB().Create(ib).Error; err != nil {
			t.Fatalf("create inbound: %v", err)
		}
	}
	target.masterReaches.Store(false)

	NewNodeHeartbeatJob().Run()

	witness.mu.Lock()
	defer witness.mu.Unlock()
	want := fmt.Sprintf("[{%s [%d 443 8443]}]", target.node.Address, target.node.Port)
	if got := fmt.Sprint(witness.asked); got != want {
		t.Fatalf("witness was asked %s, want %s", got, want)
	}
}
