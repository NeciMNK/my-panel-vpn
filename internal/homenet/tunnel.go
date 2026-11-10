package homenet

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tunnel is a system OpenVPN or L2TP/IPsec client the gateway server keeps up.
type Tunnel struct {
	Id       int      `json:"id"`
	Kind     string   `json:"kind"`
	Enable   bool     `json:"enable"`
	OpenVPN  string   `json:"openvpn,omitempty"`
	Server   string   `json:"server,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	PSK      string   `json:"psk,omitempty"`
	Routes   []string `json:"routes"`
}

// TunnelStatus is what the gateway reports back about its tunnel.
type TunnelStatus struct {
	Active bool   `json:"active" example:"true"`
	Detail string `json:"detail" example:"active"`
}

// Directives that run programs or reroute the whole server; a home network only
// ever needs its own routes, so these are dropped from a pasted .ovpn.
var ovpnDropped = map[string]bool{
	"up": true, "down": true, "route-up": true, "route-pre-down": true, "ipchange": true,
	"tls-verify": true, "auth-user-pass-verify": true, "client-connect": true,
	"client-disconnect": true, "learn-address": true, "plugin": true, "script-security": true,
	"redirect-gateway": true, "route": true, "route-ipv6": true, "dev": true, "dev-type": true,
	"auth-user-pass": true, "daemon": true, "log": true, "log-append": true, "writepid": true,
	"management": true, "cd": true, "chroot": true, "config": true,
}

// RenderOpenVPN rewrites a pasted .ovpn to bring up only this home network:
// fixed interface name, no pushed routes, no scripts, routes to the targets.
func RenderOpenVPN(t *Tunnel, authFile string) (string, error) {
	var b strings.Builder
	inBlock := ""
	remote := false
	sc := bufio.NewScanner(strings.NewReader(t.OpenVPN))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if inBlock != "" {
			b.WriteString(line + "\n")
			if strings.EqualFold(trimmed, "</"+inBlock+">") {
				inBlock = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && !strings.HasPrefix(trimmed, "</") {
			inBlock = strings.Trim(trimmed, "<>")
			b.WriteString(line + "\n")
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		name := strings.ToLower(strings.Fields(trimmed)[0])
		if ovpnDropped[name] {
			continue
		}
		if name == "remote" {
			remote = true
		}
		b.WriteString(trimmed + "\n")
	}
	if !remote {
		return "", errors.New("openvpn config has no remote line")
	}
	fmt.Fprintf(&b, "dev %s\ndev-type tun\nroute-nopull\nscript-security 1\n", IfName(t.Id))
	if t.Username != "" {
		fmt.Fprintf(&b, "auth-user-pass %s\n", authFile)
	}
	for _, r := range t.Routes {
		p, err := netip.ParsePrefix(asPrefix(r))
		if err != nil || !p.Addr().Is4() {
			continue
		}
		fmt.Fprintf(&b, "route %s %s\n", p.Masked().Addr(), prefixMask(p.Bits()))
	}
	return b.String(), nil
}

func prefixMask(bits int) string {
	m := uint32(0)
	if bits > 0 {
		m = ^uint32(0) << (32 - bits)
	}
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&0xff, m>>8&0xff, m&0xff)
}

func checkPPPValue(field, v string) error {
	if strings.ContainsAny(v, "\"\\\n\r") {
		return fmt.Errorf("%s must not contain quotes, backslashes or line breaks", field)
	}
	return nil
}

// L2TPFiles renders the xl2tpd, pppd and IPsec files of an L2TP tunnel, keyed
// by absolute path; IPsec files are omitted when there is no pre-shared key.
func L2TPFiles(t *Tunnel, dir string) (map[string]string, error) {
	for field, v := range map[string]string{"server": t.Server, "username": t.Username, "password": t.Password, "psk": t.PSK} {
		if err := checkPPPValue(field, v); err != nil {
			return nil, err
		}
	}
	if t.Server == "" || strings.ContainsAny(t.Server, " \t") {
		return nil, errors.New("l2tp: server address is required")
	}
	conn := unitName(t.Id)
	files := map[string]string{}
	files[filepath.Join(dir, "xl2tpd.conf")] = fmt.Sprintf(`[lac home]
lns = %s
ppp debug = no
pppoptfile = %s
length bit = yes
autodial = yes
redial = yes
redial timeout = 10
`, t.Server, filepath.Join(dir, "options"))
	files[filepath.Join(dir, "options")] = fmt.Sprintf(`ifname %s
name "%s"
password "%s"
noauth
nodefaultroute
persist
maxfail 0
mtu 1400
mru 1400
noccp
refuse-eap
ip-up-script %s
`, IfName(t.Id), t.Username, t.Password, filepath.Join(dir, "ip-up"))
	var up strings.Builder
	up.WriteString("#!/bin/sh\n")
	for _, r := range t.Routes {
		fmt.Fprintf(&up, "ip route replace %s dev \"$1\"\n", asPrefix(r))
	}
	files[filepath.Join(dir, "ip-up")] = up.String()
	if t.PSK != "" {
		files[filepath.Join(ipsecDir, conn+".conf")] = fmt.Sprintf(`conn %s
	keyexchange=ikev1
	authby=secret
	type=transport
	left=%%defaultroute
	leftprotoport=17/1701
	right=%s
	rightprotoport=17/1701
	ike=aes256-sha1-modp2048,aes128-sha1-modp2048,3des-sha1-modp1024!
	esp=aes256-sha1,aes128-sha1,3des-sha1!
	auto=add
`, conn, t.Server)
		files[filepath.Join(ipsecDir, conn+".secrets")] = fmt.Sprintf("%%any %s : PSK \"%s\"\n", t.Server, t.PSK)
	}
	return files, nil
}

func unitName(id int) string { return fmt.Sprintf("x-ui-home-%d", id) }

// Paths are variables so tests can render into a temp dir.
var (
	baseDir    = "/etc/x-ui/home"
	ipsecDir   = "/etc/ipsec.d"
	unitDir    = "/etc/systemd/system"
	run        = runCommand
	lookPath   = exec.LookPath
	ipsecFiles = [2]string{"/etc/ipsec.conf", "/etc/ipsec.secrets"}
)

// commandTimeout bounds one system command; apt installs are the slowest.
const commandTimeout = 10 * time.Minute

func runCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensurePackages(bins map[string]string) error {
	var missing []string
	for bin, pkg := range bins {
		if _, err := lookPath(bin); err != nil {
			missing = append(missing, pkg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if _, err := lookPath("apt-get"); err != nil {
		return fmt.Errorf("install %s on this server first", strings.Join(missing, ", "))
	}
	if err := run("apt-get", "update", "-qq"); err != nil {
		return err
	}
	return run("env", append([]string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "-qq"}, missing...)...)
}

// ensureInclude adds an include line for our IPsec files to a strongSwan main
// file once, leaving everything else in it alone.
func ensureInclude(path, line string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(raw), line) {
		return nil
	}
	body := string(raw)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body+line+"\n"), 0o600)
}

func writeFiles(files map[string]string) error {
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if filepath.Base(path) == "ip-up" {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			return err
		}
	}
	return nil
}

func unitFile(t *Tunnel, dir string) string {
	var exec string
	switch t.Kind {
	case KindOpenVPN:
		exec = "ExecStart=/usr/sbin/openvpn --config " + filepath.Join(dir, "client.conf")
	default:
		pre := ""
		if t.PSK != "" {
			pre = fmt.Sprintf("ExecStartPre=-/bin/sh -c 'ipsec status >/dev/null 2>&1 || ipsec start; sleep 2; ipsec reload; ipsec up %s'\n", unitName(t.Id))
		}
		exec = pre + fmt.Sprintf("ExecStart=/usr/sbin/xl2tpd -D -c %s -p /run/%s.pid -C /run/%s.control",
			filepath.Join(dir, "xl2tpd.conf"), unitName(t.Id), unitName(t.Id))
	}
	return fmt.Sprintf(`[Unit]
Description=x-ui home network %d
After=network-online.target
Wants=network-online.target

[Service]
%s
Restart=always
RestartSec=10s

[Install]
WantedBy=multi-user.target
`, t.Id, exec)
}

// Apply brings the tunnel to the wanted state on this server: installs the
// client, writes its files and (re)starts its unit, or tears it all down.
func Apply(t *Tunnel) error {
	dir := filepath.Join(baseDir, fmt.Sprint(t.Id))
	unit := unitName(t.Id)
	unitPath := filepath.Join(unitDir, unit+".service")
	if !t.Enable {
		_ = run("systemctl", "disable", "--now", unit)
		if t.Kind == KindL2TP {
			_ = run("ipsec", "down", unit)
		}
		for _, p := range []string{unitPath, filepath.Join(ipsecDir, unit+".conf"), filepath.Join(ipsecDir, unit+".secrets")} {
			_ = os.Remove(p)
		}
		_ = os.RemoveAll(dir)
		return run("systemctl", "daemon-reload")
	}
	files := map[string]string{}
	switch t.Kind {
	case KindOpenVPN:
		if err := ensurePackages(map[string]string{"openvpn": "openvpn"}); err != nil {
			return err
		}
		auth := filepath.Join(dir, "auth")
		conf, err := RenderOpenVPN(t, auth)
		if err != nil {
			return err
		}
		files[filepath.Join(dir, "client.conf")] = conf
		if t.Username != "" {
			files[auth] = t.Username + "\n" + t.Password + "\n"
		}
	case KindL2TP:
		bins := map[string]string{"xl2tpd": "xl2tpd", "pppd": "ppp"}
		if t.PSK != "" {
			bins["ipsec"] = "strongswan"
		}
		if err := ensurePackages(bins); err != nil {
			return err
		}
		l2tp, err := L2TPFiles(t, dir)
		if err != nil {
			return err
		}
		files = l2tp
		if t.PSK != "" {
			if err := ensureInclude(ipsecFiles[0], "include "+ipsecDir+"/x-ui-home-*.conf"); err != nil {
				return err
			}
			if err := ensureInclude(ipsecFiles[1], "include "+ipsecDir+"/x-ui-home-*.secrets"); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("tunnel kind %q is not a system tunnel", t.Kind)
	}
	files[unitPath] = unitFile(t, dir)
	if err := writeFiles(files); err != nil {
		return err
	}
	if err := os.Chmod(unitPath, 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "enable", unit); err != nil {
		return err
	}
	return run("systemctl", "restart", unit)
}

// Status reports whether the tunnel's unit runs and its interface exists.
func Status(id int) TunnelStatus {
	if err := run("systemctl", "is-active", "--quiet", unitName(id)); err != nil {
		return TunnelStatus{Detail: "service is not running"}
	}
	if _, err := os.Stat("/sys/class/net/" + IfName(id)); err != nil {
		return TunnelStatus{Detail: "connecting"}
	}
	return TunnelStatus{Active: true, Detail: "active"}
}

var applyMu sync.Mutex

// ApplyWithin runs Apply but answers within ApplyBudget: installing a client
// can outlast the master's request, so the rest finishes in the background.
func ApplyWithin(t *Tunnel) (TunnelStatus, error) {
	done := make(chan error, 1)
	go func() {
		applyMu.Lock()
		defer applyMu.Unlock()
		done <- Apply(t)
	}()
	select {
	case err := <-done:
		if err != nil || !t.Enable {
			return TunnelStatus{}, err
		}
		return Status(t.Id), nil
	case <-time.After(ApplyBudget):
		return TunnelStatus{Detail: "installing the tunnel client"}, nil
	}
}

// ApplyBudget stays under the master's 60s node request timeout.
var ApplyBudget = 45 * time.Second
