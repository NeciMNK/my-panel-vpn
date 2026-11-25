// Package backup keeps panel and node backups on another server over SSH and
// carries what a node restore needs onto a fresh server.
package backup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHLogin is how to reach a server: a password, a private key, or both.
type SSHLogin struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
	// HostKey is the SHA256 fingerprint to insist on; empty trusts the first key seen.
	HostKey string
}

// ErrHostKeyChanged means the server answered with another key than the pinned one.
var ErrHostKeyChanged = errors.New("the server's SSH host key changed; if it was reinstalled, clear the saved host key")

func (l SSHLogin) addr() string {
	port := l.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(strings.Trim(strings.TrimSpace(l.Host), "[]"), strconv.Itoa(port))
}

// ValidatePrivateKey reports whether key is an unencrypted private key SSH can use.
func ValidatePrivateKey(key string) error {
	if _, err := ssh.ParsePrivateKey([]byte(strings.TrimSpace(key) + "\n")); err != nil {
		return fmt.Errorf("SSH private key: %w", err)
	}
	return nil
}

func (l SSHLogin) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if key := strings.TrimSpace(l.PrivateKey); key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(key + "\n"))
		if err != nil {
			return nil, fmt.Errorf("SSH private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if l.Password != "" {
		methods = append(methods, ssh.Password(l.Password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = l.Password
				}
				return answers, nil
			}))
	}
	if len(methods) == 0 {
		return nil, errors.New("give a password or an SSH private key")
	}
	return methods, nil
}

// Dial logs in and returns the client with the fingerprint of the host key it saw.
func Dial(ctx context.Context, l SSHLogin) (*ssh.Client, string, error) {
	if strings.TrimSpace(l.Host) == "" {
		return nil, "", errors.New("server address is empty")
	}
	auth, err := l.authMethods()
	if err != nil {
		return nil, "", err
	}
	user := strings.TrimSpace(l.User)
	if user == "" {
		user = "root"
	}
	var seen string
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(key)
			if l.HostKey != "" && l.HostKey != seen {
				return ErrHostKeyChanged
			}
			return nil
		},
		Timeout: 20 * time.Second,
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", l.addr())
	if err != nil {
		return nil, "", err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(cfg.Timeout))
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, l.addr(), cfg)
	if err != nil {
		_ = conn.Close()
		if errors.Is(err, ErrHostKeyChanged) || strings.Contains(err.Error(), ErrHostKeyChanged.Error()) {
			return nil, seen, ErrHostKeyChanged
		}
		return nil, seen, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), seen, nil
}

// Run executes a shell command and returns its combined output.
func Run(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// ShellQuote makes s one literal POSIX shell word.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
