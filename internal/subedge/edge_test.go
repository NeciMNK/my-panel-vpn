package subedge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// connectMaster opens a tunnel from a master serving h to the node and waits
// until the node has registered it.
func connectMaster(t *testing.T, node *Node, name string, priority int, h http.Handler) context.CancelFunc {
	t.Helper()
	accept := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := node.AcceptTunnel(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	}))
	t.Cleanup(accept.Close)
	before := len(node.Tunnels())

	ctx, cancel := context.WithCancel(context.Background())
	req, err := NewTunnelRequest(ctx, accept.URL+"/tunnel", Master{Name: name, Priority: priority})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := TunnelConn(resp)
	if err != nil {
		t.Fatal(err)
	}
	go ServeTunnel(ctx, conn, h)
	t.Cleanup(cancel)

	deadline := time.Now().Add(5 * time.Second)
	for len(node.Tunnels()) == before {
		if time.Now().After(deadline) {
			t.Fatalf("node never registered the tunnel of %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cancel
}

func edgeGet(t *testing.T, node *Node, path string, header http.Header) (int, string) {
	t.Helper()
	edge := httptest.NewTLSServer(node)
	t.Cleanup(edge.Close)
	req, err := http.NewRequest(http.MethodGet, edge.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "sub.example.com"
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := edge.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestEdgeServesSubscriptionThroughMasterTunnel(t *testing.T) {
	node := &Node{}
	connectMaster(t, node, "master-1", 1, FromEdge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s host=%s tls=%t ip=%s xfh=%q",
			r.URL.Path, r.Host, r.TLS != nil, strings.Split(r.RemoteAddr, ":")[0], r.Header.Get("X-Forwarded-Host"))
	})))

	status, body := edgeGet(t, node, "/sub/abc", http.Header{"X-Forwarded-Host": {"evil.example.com"}})

	want := `path=/sub/abc host=sub.example.com tls=true ip=127.0.0.1 xfh=""`
	if status != http.StatusOK || body != want {
		t.Fatalf("edge answered %d %q, want 200 %q", status, body, want)
	}
}

func TestEdgeFallsBackToTheNextMaster(t *testing.T) {
	answer := func(name string, status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, name)
		})
	}

	t.Run("primary answers 502", func(t *testing.T) {
		node := &Node{}
		connectMaster(t, node, "backup", 2, answer("backup", http.StatusOK))
		connectMaster(t, node, "primary", 1, answer("primary", http.StatusBadGateway))
		if status, body := edgeGet(t, node, "/sub/abc", nil); status != http.StatusOK || body != "backup" {
			t.Fatalf("edge answered %d %q, want the backup master's 200", status, body)
		}
	})

	t.Run("primary answers 404", func(t *testing.T) {
		node := &Node{}
		connectMaster(t, node, "backup", 2, answer("backup", http.StatusOK))
		connectMaster(t, node, "primary", 1, answer("primary", http.StatusNotFound))
		if status, body := edgeGet(t, node, "/sub/abc", nil); status != http.StatusNotFound || body != "primary" {
			t.Fatalf("edge answered %d %q, want the primary master's own 404", status, body)
		}
	})

	t.Run("primary tunnel closed", func(t *testing.T) {
		node := &Node{}
		connectMaster(t, node, "backup", 2, answer("backup", http.StatusOK))
		closePrimary := connectMaster(t, node, "primary", 1, answer("primary", http.StatusOK))
		closePrimary()
		if status, body := edgeGet(t, node, "/sub/abc", nil); status != http.StatusOK || body != "backup" {
			t.Fatalf("edge answered %d %q, want the backup master's 200", status, body)
		}
	})
}

func TestEdgeWithoutMastersAnswers503(t *testing.T) {
	status, _ := edgeGet(t, &Node{}, "/sub/abc", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("edge answered %d, want 503", status)
	}
}

func TestAcceptTunnelRejectsPlainRequests(t *testing.T) {
	node := &Node{}
	rec := httptest.NewRecorder()
	err := node.AcceptTunnel(rec, httptest.NewRequest(http.MethodPost, "/tunnel", nil))
	if !errors.Is(err, ErrNotTunnelUpgrade) {
		t.Fatalf("AcceptTunnel = %v, want ErrNotTunnelUpgrade", err)
	}
	if len(node.Tunnels()) != 0 {
		t.Fatalf("node registered %d tunnels from a plain request", len(node.Tunnels()))
	}
}
