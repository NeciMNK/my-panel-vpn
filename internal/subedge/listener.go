package subedge

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/network"
)

// ListenerConfig is what a master tells a node about its edge listener. Empty cert
// files serve plain HTTP, for an edge behind the admin's own TLS proxy.
type ListenerConfig struct {
	Enable   bool   `json:"enable" example:"true"`
	Port     int    `json:"port" example:"443"`
	CertFile string `json:"certFile" example:"/etc/x-ui/certs/sub.example.com/fullchain.pem"`
	KeyFile  string `json:"keyFile" example:"/etc/x-ui/certs/sub.example.com/privkey.pem"`
}

// Apply starts, moves or stops the edge listener to match cfg.
func (n *Node) Apply(cfg ListenerConfig) error {
	n.srvMu.Lock()
	defer n.srvMu.Unlock()
	if n.srv != nil && cfg == n.served {
		return nil
	}
	n.stopLocked()
	if !cfg.Enable {
		return nil
	}

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", net.JoinHostPort("", strconv.Itoa(cfg.Port)))
	if err != nil {
		return err
	}
	if cfg.CertFile != "" || cfg.KeyFile != "" {
		reloader, err := network.NewCertReloader(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			listener.Close()
			return err
		}
		listener = tls.NewListener(listener, &tls.Config{GetCertificate: reloader.GetCertificate, MinVersion: tls.VersionTLS12})
	}
	n.srv = &http.Server{
		Handler:           n,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	n.served = cfg
	go network.ServeHTTP(n.srv, listener, "Subscription edge")
	logger.Info("Subscription edge listening on", listener.Addr())
	return nil
}

// Stop closes the edge listener and every tunnel.
func (n *Node) Stop() {
	n.srvMu.Lock()
	n.stopLocked()
	n.srvMu.Unlock()
	n.mu.Lock()
	for _, t := range n.tunnels {
		t.conn.Close()
	}
	n.tunnels = nil
	n.mu.Unlock()
}

func (n *Node) stopLocked() {
	if n.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = n.srv.Shutdown(ctx)
	n.srv = nil
	n.served = ListenerConfig{}
}
