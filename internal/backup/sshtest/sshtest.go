// Package sshtest runs a real in-process SSH server with SFTP and exec, so
// backup and restore code talks to it exactly as to a remote host.
package sshtest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os/exec"
	"strconv"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Server accepts one password for any user.
type Server struct {
	Host, Password string
	Port           int
	Fingerprint    string
}

// Start runs a server until t ends.
func Start(t testing.TB, password string) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if string(p) != password {
			return nil, ssh.ErrNoAuth
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveTestSSH(conn, cfg)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return &Server{Host: "127.0.0.1", Port: addr.Port, Password: password, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
}

func serveTestSSH(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, requests, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				switch req.Type {
				case "subsystem":
					_ = req.Reply(true, nil)
					srv, err := sftp.NewServer(ch)
					if err == nil {
						_ = srv.Serve()
					}
					return
				case "exec":
					_ = req.Reply(true, nil)
					var payload struct{ Cmd string }
					_ = ssh.Unmarshal(req.Payload, &payload)
					out, err := exec.CommandContext(context.Background(), "sh", "-c", payload.Cmd).CombinedOutput()
					_, _ = ch.Write(out)
					_ = ch.CloseWrite()
					code := 0
					if err != nil {
						code = 1
						var ee *exec.ExitError
						if errors.As(err, &ee) {
							code = ee.ExitCode()
						}
					}
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{uint32(code)}))
					return
				default:
					_ = req.Reply(false, nil)
				}
			}
		}()
	}
}

func (s *Server) String() string { return s.Host + ":" + strconv.Itoa(s.Port) }
