// Package subedge lets a node serve the master's subscription server: each
// master opens a tunnel to the node, and the node's edge listener forwards
// subscription requests through the best live tunnel.
package subedge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/network"
)

// ErrNotTunnelUpgrade is returned by AcceptTunnel for a request that does not
// ask to switch to the sub edge tunnel protocol.
var ErrNotTunnelUpgrade = errors.New("not a sub edge tunnel upgrade request")

const (
	upgradeProtocol      = "x-ui-sub-edge/1"
	headerMasterName     = "X-Sub-Edge-Master"
	headerMasterPriority = "X-Sub-Edge-Priority"
	headerClient         = "X-Sub-Edge-Client"
	maxEdgeRequestBody   = 64 << 10
	tunnelReadIdle       = 30 * time.Second
	tunnelPingTimeout    = 15 * time.Second
)

// masterAnswerTimeout is how long the edge waits for a master's response
// headers before it tries the next master or the cache.
var masterAnswerTimeout = 5 * time.Second

// Master identifies the master panel on the other end of a tunnel; a lower
// Priority is tried first, as in the master cluster.
type Master struct {
	Name     string
	Priority int
}

// TunnelInfo describes one live tunnel for status display.
type TunnelInfo struct {
	Master   string `json:"master" example:"master-1"`
	Priority int    `json:"priority" example:"1"`
	Since    int64  `json:"since" example:"1762736400"`
}

type tunnel struct {
	Master
	since time.Time
	cc    *http.ClientConn
	conn  net.Conn
}

// Node is the node side of the feature: the tunnels masters opened to it and
// the public edge listener that forwards through them.
type Node struct {
	// CacheDir is where cached responses are kept; nil keeps them in memory.
	CacheDir func() string

	mu      sync.Mutex
	tunnels []*tunnel

	cacheOnce sync.Once
	cache     *responseCache

	srvMu  sync.Mutex
	srv    *http.Server
	served ListenerConfig
}

// AcceptTunnel takes over the connection of a master's upgrade request and
// keeps it as a tunnel; it returns once the tunnel is registered.
func (n *Node) AcceptTunnel(w http.ResponseWriter, r *http.Request) error {
	if !strings.EqualFold(r.Header.Get("Upgrade"), upgradeProtocol) || !hasToken(r.Header.Get("Connection"), "upgrade") {
		return ErrNotTunnelUpgrade
	}
	priority, _ := strconv.Atoi(r.Header.Get(headerMasterPriority))
	master := Master{Name: r.Header.Get(headerMasterName), Priority: priority}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("the connection cannot be taken over for a tunnel")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + upgradeProtocol + "\r\n\r\n"); err != nil {
		conn.Close()
		return err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return err
	}
	bc := &bufferedConn{Conn: conn, r: rw.Reader}
	cc, err := tunnelClientConn(r.Context(), bc)
	if err != nil {
		conn.Close()
		return err
	}
	n.add(&tunnel{Master: master, since: time.Now(), cc: cc, conn: bc})
	return nil
}

// add registers t, replacing an older tunnel of the same master.
func (n *Node) add(t *tunnel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	kept := n.tunnels[:0]
	for _, old := range n.tunnels {
		if old.Name == t.Name {
			old.conn.Close()
			continue
		}
		kept = append(kept, old)
	}
	n.tunnels = append(kept, t)
}

// live returns the usable tunnels best first, dropping the closed ones.
func (n *Node) live() []*tunnel {
	n.mu.Lock()
	defer n.mu.Unlock()
	kept := n.tunnels[:0]
	for _, t := range n.tunnels {
		if t.cc.Err() != nil {
			t.conn.Close()
			continue
		}
		kept = append(kept, t)
	}
	n.tunnels = kept
	out := append([]*tunnel(nil), kept...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// Tunnels lists the live tunnels best first.
func (n *Node) Tunnels() []TunnelInfo {
	live := n.live()
	out := make([]TunnelInfo, 0, len(live))
	for _, t := range live {
		out = append(out, TunnelInfo{Master: t.Name, Priority: t.Priority, Since: t.since.Unix()})
	}
	return out
}

// ServeHTTP forwards a subscription request to the best master that answers
// without a server error, and answers from the cache when none does.
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxEdgeRequestBody+1))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) > maxEdgeRequestBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	for _, t := range n.live() {
		resp, err := t.ask(forwardedRequest(r, body))
		if err != nil {
			continue
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			resp.Body.Close()
			continue
		}
		relay(w, resp)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		now := time.Now()
		if cached, ok := n.responses().lookup(r, now); ok {
			serveCached(w, r, cached, now)
			return
		}
	}
	http.Error(w, "subscription temporarily unavailable", http.StatusServiceUnavailable)
}

// UpdateCache stores a master's new responses and drops those of
// subscriptions it no longer serves.
func (n *Node) UpdateCache(u CacheUpdate) error {
	return n.responses().update(u)
}

func (n *Node) responses() *responseCache {
	n.cacheOnce.Do(func() { n.cache = &responseCache{dir: n.CacheDir} })
	return n.cache
}

// ask sends req to the tunnel's master, giving up when the response headers
// do not arrive within masterAnswerTimeout.
func (t *tunnel) ask(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(masterAnswerTimeout, cancel)
	resp, err := t.cc.RoundTrip(req.WithContext(ctx))
	if !timer.Stop() && err == nil {
		resp.Body.Close()
		err = context.DeadlineExceeded
	}
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// hopHeaders are dropped both ways; the forwarding ones so a client cannot
// pose as another host or address to the master.
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", headerClient,
}

func forwardedRequest(r *http.Request, body []byte) *http.Request {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL = &url.URL{Scheme: "https", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	out.Host = r.Host
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	out.Header.Set(headerClient, r.RemoteAddr)
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return out
}

func relay(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// FromEdge wraps the master's subscription handler for tunnel requests: the
// client address comes from the node, and the client reached the node over TLS.
func FromEdge(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client := r.Header.Get(headerClient); client != "" {
			r.RemoteAddr = client
		}
		r.Header.Del(headerClient)
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		r.TLS = &tls.ConnectionState{HandshakeComplete: true, ServerName: host}
		h.ServeHTTP(w, r)
	})
}

// NewTunnelRequest builds the master's upgrade request to a node's tunnel endpoint.
func NewTunnelRequest(ctx context.Context, target string, m Master) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", upgradeProtocol)
	req.Header.Set(headerMasterName, m.Name)
	req.Header.Set(headerMasterPriority, strconv.Itoa(m.Priority))
	return req, nil
}

// TunnelConn turns the node's answer to NewTunnelRequest into the tunnel connection.
func TunnelConn(resp *http.Response) (net.Conn, error) {
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("node refused the tunnel: HTTP %d: %q", resp.StatusCode, bytes.TrimSpace(snippet))
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("node switched protocols without a writable connection")
	}
	return &rwcConn{ReadWriteCloser: rwc}, nil
}

// ServeTunnel answers the node's requests on conn with h until the tunnel
// breaks or ctx ends.
func ServeTunnel(ctx context.Context, conn net.Conn, h http.Handler) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		conn.Close()
	}()
	srv := &http.Server{
		Handler:   h,
		Protocols: unencryptedHTTP2(),
		HTTP2:     tunnelHTTP2Config(),
	}
	srv.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed || state == http.StateHijacked {
			go srv.Close()
		}
	}
	network.ServeHTTP(srv, newOneConnListener(conn), "Subscription edge tunnel")
}

func unencryptedHTTP2() *http.Protocols {
	p := &http.Protocols{}
	p.SetUnencryptedHTTP2(true)
	return p
}

func tunnelHTTP2Config() *http.HTTP2Config {
	return &http.HTTP2Config{SendPingTimeout: tunnelReadIdle, PingTimeout: tunnelPingTimeout}
}

// tunnelClientConn speaks HTTP/2 with prior knowledge over an existing connection.
func tunnelClientConn(ctx context.Context, conn net.Conn) (*http.ClientConn, error) {
	used := false
	tr := &http.Transport{
		Protocols: unencryptedHTTP2(),
		HTTP2:     tunnelHTTP2Config(),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if used {
				return nil, errors.New("the tunnel connection was already used")
			}
			used = true
			return conn, nil
		},
	}
	return tr.NewClientConn(context.WithoutCancel(ctx), "http", "sub-edge-tunnel:80")
}

// oneConnListener hands out a single connection, then waits until closed.
type oneConnListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newOneConnListener(c net.Conn) *oneConnListener {
	l := &oneConnListener{conns: make(chan net.Conn, 1), done: make(chan struct{})}
	l.conns <- c
	return l
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return tunnelAddr{} }

func hasToken(header, token string) bool {
	for part := range strings.SplitSeq(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type rwcConn struct {
	io.ReadWriteCloser
}

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "sub-edge" }
func (tunnelAddr) String() string  { return "sub-edge-tunnel" }

func (c *rwcConn) LocalAddr() net.Addr              { return tunnelAddr{} }
func (c *rwcConn) RemoteAddr() net.Addr             { return tunnelAddr{} }
func (c *rwcConn) SetDeadline(time.Time) error      { return nil }
func (c *rwcConn) SetReadDeadline(time.Time) error  { return nil }
func (c *rwcConn) SetWriteDeadline(time.Time) error { return nil }
