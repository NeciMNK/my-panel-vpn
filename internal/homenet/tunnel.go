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
	"slices"
	"strings"
	"sync"
	"time"
)

// Tunnel is a system OpenVPN, L2TP/IPsec or IKEv2 client the gateway server keeps up.
type Tunnel struct {
	Id       int      `json:"id"`
	Kind     string   `json:"kind"`
	Enable   bool     `json:"enable"`
	OpenVPN  string   `json:"openvpn,omitempty"`
	Server   string   `json:"server,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	PSK      string   `json:"psk,omitempty"`
	CA       string   `json:"ca,omitempty"`
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
// fixed interface name, no pushed routes or pasted scripts, only our ip-up.
func RenderOpenVPN(t *Tunnel, dir string) (string, error) {
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
	fmt.Fprintf(&b, "dev %s\ndev-type tun\nroute-nopull\npersist-tun\nscript-security 2\nup %s\n",
		IfName(t.Id), filepath.Join(dir, upScript))
	if t.Username != "" {
		fmt.Fprintf(&b, "auth-user-pass %s\n", filepath.Join(dir, "auth"))
	}
	return b.String(), nil
}

// upScript routes the home targets in the tunnel's own table, used only by
// sockets bound to its interface, so the gateway's LAN traffic never enters it.
const upScript = "ip-up"

// RouteTable is the routing table holding a home network's tunnel routes.
func RouteTable(id int) int { return 28000 + id }

func routeUpScript(t *Tunnel) string {
	dev, table := IfName(t.Id), RouteTable(t.Id)
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\nsysctl -qw net.ipv4.conf.%s.rp_filter=2\n", dev)
	for _, family := range []string{"-4", "-6"} {
		var routes []string
		for _, r := range t.Routes {
			if p, err := netip.ParsePrefix(asPrefix(r)); err == nil && p.Addr().Is4() == (family == "-4") {
				routes = append(routes, p.Masked().String())
			}
		}
		if len(routes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "ip %s rule del oif %s lookup %d 2>/dev/null\nip %s rule add oif %s lookup %d\n", family, dev, table, family, dev, table)
		for _, r := range routes {
			fmt.Fprintf(&b, "ip %s route replace %s dev %s table %d\n", family, r, dev, table)
		}
	}
	b.WriteString("exit 0\n")
	return b.String()
}

func checkPPPValue(field, v string) error {
	if strings.ContainsAny(v, "\"\\\n\r") {
		return fmt.Errorf("%s must not contain quotes, backslashes or line breaks", field)
	}
	return nil
}

// L2TPFiles renders the xl2tpd, pppd and swanctl files of an L2TP tunnel, keyed
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
`, IfName(t.Id), t.Username, t.Password, filepath.Join(dir, upScript))
	files[filepath.Join(dir, upScript)] = routeUpScript(t)
	if t.PSK != "" {
		files[filepath.Join(swanctlDir, conn+".conf")] = fmt.Sprintf(`connections {
	%[1]s {
		version = 1
		remote_addrs = %[2]s
		proposals = aes256-sha1-modp2048, aes128-sha1-modp2048, 3des-sha1-modp1024
		dpd_delay = 30s
		keyingtries = 0
		local {
			auth = psk
		}
		remote {
			auth = psk
		}
		children {
			%[1]s {
				mode = transport
				local_ts = dynamic[udp/1701]
				remote_ts = dynamic[udp/1701]
				esp_proposals = aes256-sha1, aes128-sha1, 3des-sha1
				dpd_action = restart
				close_action = start
			}
		}
	}
}
secrets {
	ike-%[1]s {
		id = "%[2]s"
		secret = "%[3]s"
	}
}
`, conn, t.Server, t.PSK)
		files[filepath.Join(dir, "start")] = swanctlStart(t.Id)
	}
	return files, nil
}

func unitName(id int) string { return fmt.Sprintf("x-ui-home-%d", id) }

// Paths are variables so tests can render into a temp dir.
var (
	baseDir  = "/etc/x-ui/home"
	ipsecDir = "/etc/ipsec.d"
	unitDir  = "/etc/systemd/system"
	run      = runCommand
	lookPath = exec.LookPath
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
		var err error
		if filepath.IsAbs(bin) {
			_, err = os.Stat(bin)
		} else {
			_, err = lookPath(bin)
		}
		if err != nil && !slices.Contains(missing, pkg) {
			missing = append(missing, pkg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	if _, err := lookPath("apt-get"); err != nil {
		return fmt.Errorf("install %s on this server first", strings.Join(missing, ", "))
	}
	if err := run("apt-get", "update", "-qq"); err != nil {
		return err
	}
	return run("env", append([]string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "-qq"}, missing...)...)
}

func writeFiles(files map[string]string) error {
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if base := filepath.Base(path); base == upScript || base == "updown" || base == "start" {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			return err
		}
	}
	return nil
}

// charonUnit is the systemd unit running charon: strongswan-starter where the
// legacy ipsec tools are installed, else charon-systemd (Debian 13 has no starter).
func charonUnit() string {
	if _, err := lookPath("ipsec"); err == nil {
		return "strongswan-starter"
	}
	return "strongswan"
}

// ensureCharon installs and starts the charon that swanctl drives, plus the
// extra packages a tunnel needs; vici is loaded only when charon starts.
func ensureCharon(bins map[string]string) error {
	bins["swanctl"] = "strongswan-swanctl"
	if charonUnit() == "strongswan" {
		bins["charon-systemd"] = "charon-systemd"
	}
	if err := ensurePackages(bins); err != nil {
		return err
	}
	if run("swanctl", "--stats") != nil {
		return run("systemctl", "restart", charonUnit())
	}
	return nil
}

// dropStarterConn unloads an L2TP connection written to ipsec.d by panels
// before the swanctl switch, so charon does not keep two SAs for it.
func dropStarterConn(conn string) error {
	legacy := []string{filepath.Join(ipsecDir, conn+".conf"), filepath.Join(ipsecDir, conn+".secrets")}
	if _, err := os.Stat(legacy[0]); err != nil {
		return nil
	}
	_ = run("ipsec", "down", conn)
	for _, p := range legacy {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := lookPath("ipsec"); err == nil {
		return run("ipsec", "reload")
	}
	return nil
}

func unitFile(t *Tunnel, dir string) string {
	if t.Kind == KindIKEv2 {
		daemon := charonUnit()
		return fmt.Sprintf(`[Unit]
Description=x-ui home network %[1]d
After=network-online.target %[4]s.service
Wants=network-online.target %[4]s.service
PartOf=%[4]s.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%[2]s
ExecStop=-/usr/sbin/swanctl --terminate --ike %[3]s --timeout 10
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
`, t.Id, filepath.Join(dir, "start"), unitName(t.Id), daemon)
	}
	var exec, deps string
	switch t.Kind {
	case KindOpenVPN:
		exec = "ExecStart=/usr/sbin/openvpn --config " + filepath.Join(dir, "client.conf")
	default:
		pre, post := "", ""
		if t.PSK != "" {
			deps = " " + charonUnit() + ".service"
			pre = "ExecStartPre=" + filepath.Join(dir, "start") + "\n"
			post = fmt.Sprintf("\nExecStopPost=-/usr/sbin/swanctl --terminate --ike %s --timeout 10", unitName(t.Id))
		}
		exec = pre + fmt.Sprintf("ExecStart=/usr/sbin/xl2tpd -D -c %s -p /run/%s.pid -C /run/%s.control",
			filepath.Join(dir, "xl2tpd.conf"), unitName(t.Id), unitName(t.Id)) + post
	}
	return fmt.Sprintf(`[Unit]
Description=x-ui home network %d
After=network-online.target%[3]s
Wants=network-online.target%[3]s

[Service]
%[2]s
Restart=always
RestartSec=10s

[Install]
WantedBy=multi-user.target
`, t.Id, exec, deps)
}

// Apply brings the tunnel to the wanted state on this server: installs the
// client, writes its files and (re)starts its unit, or tears it all down.
func Apply(t *Tunnel) error {
	dir := filepath.Join(baseDir, fmt.Sprint(t.Id))
	unit := unitName(t.Id)
	unitPath := filepath.Join(unitDir, unit+".service")
	if !t.Enable {
		_ = run("systemctl", "disable", "--now", unit)
		table := fmt.Sprint(RouteTable(t.Id))
		for _, family := range []string{"-4", "-6"} {
			_ = run("ip", family, "rule", "del", "oif", IfName(t.Id), "lookup", table)
			_ = run("ip", family, "route", "flush", "table", table)
		}
		for _, p := range []string{unitPath, filepath.Join(ipsecDir, unit+".conf"), filepath.Join(ipsecDir, unit+".secrets")} {
			_ = os.Remove(p)
		}
		if conf := filepath.Join(swanctlDir, unit+".conf"); os.Remove(conf) == nil {
			_ = run("swanctl", "--load-all", "--noprompt")
		}
		if t.Kind == KindIKEv2 {
			_ = run("ip", "link", "del", IfName(t.Id))
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
		conf, err := RenderOpenVPN(t, dir)
		if err != nil {
			return err
		}
		files[filepath.Join(dir, "client.conf")] = conf
		files[filepath.Join(dir, upScript)] = routeUpScript(t)
		if t.Username != "" {
			files[filepath.Join(dir, "auth")] = t.Username + "\n" + t.Password + "\n"
		}
	case KindL2TP:
		l2tp, err := L2TPFiles(t, dir)
		if err != nil {
			return err
		}
		files = l2tp
		if err := dropStarterConn(unit); err != nil {
			return err
		}
		bins := map[string]string{"xl2tpd": "xl2tpd", "pppd": "ppp"}
		if t.PSK == "" {
			err = ensurePackages(bins)
		} else {
			err = ensureCharon(bins)
		}
		if err != nil {
			return err
		}
	case KindIKEv2:
		ike, err := IKEv2Files(t, dir)
		if err != nil {
			return err
		}
		files = ike
		files[filepath.Join(dir, "start")] = swanctlStart(t.Id)
		bins := map[string]string{}
		if t.PSK == "" {
			bins[eapPlugin] = "libcharon-extra-plugins"
		}
		if err := ensureCharon(bins); err != nil {
			return err
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
