package homenet

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const routerWG = `[Interface]
PrivateKey = cPriv=
Address = 10.8.0.2/32, fd00::2/128
MTU = 1380
DNS = 192.168.1.1

[Peer]
PublicKey = hPub=
PresharedKey = psk=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = home.example.net:51820
PersistentKeepalive = 25
`

func TestParseWireGuardReadsARouterExport(t *testing.T) {
	wg, err := ParseWireGuard(routerWG)
	if err != nil {
		t.Fatal(err)
	}
	want := &WireGuard{
		PrivateKey: "cPriv=", Address: []string{"10.8.0.2/32", "fd00::2/128"}, MTU: 1380,
		PublicKey: "hPub=", PresharedKey: "psk=", Endpoint: "home.example.net:51820", Keepalive: 25,
	}
	if !reflect.DeepEqual(wg, want) {
		t.Fatalf("got %+v\nwant %+v", wg, want)
	}
}

func TestParseWireGuardRejects(t *testing.T) {
	cases := map[string]string{
		"no peer":       "[Interface]\nPrivateKey = a\nAddress = 10.0.0.2/32\n",
		"two peers":     routerWG + "\n[Peer]\nPublicKey = x\nEndpoint = b:1\n",
		"no endpoint":   strings.Replace(routerWG, "Endpoint = home.example.net:51820\n", "", 1),
		"bad endpoint":  strings.Replace(routerWG, "home.example.net:51820", "home.example.net", 1),
		"garbage line":  routerWG + "\nnonsense\n",
		"no address":    strings.Replace(routerWG, "Address = 10.8.0.2/32, fd00::2/128\n", "", 1),
		"no public key": strings.Replace(routerWG, "PublicKey = hPub=\n", "", 1),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWireGuard(text); err == nil || !strings.HasPrefix(err.Error(), "wireguard config:") {
				t.Fatalf("err = %v, want a wireguard config error", err)
			}
		})
	}
}

func TestWireGuardOutboundOnlyReachesTheHomeTargets(t *testing.T) {
	wg, _ := ParseWireGuard(routerWG)
	raw, _ := json.Marshal(wg.Outbound("mp-home-1-tunnel", []string{"192.168.1.0/24", "10.0.0.5"}))
	want := `{"protocol":"wireguard","settings":{"address":["10.8.0.2/32","fd00::2/128"],"mtu":1380,"noKernelTun":true,` +
		`"peers":[{"allowedIPs":["192.168.1.0/24","10.0.0.5/32"],"endpoint":"home.example.net:51820","keepAlive":25,"preSharedKey":"psk=","publicKey":"hPub="}],` +
		`"secretKey":"cPriv="},"tag":"mp-home-1-tunnel"}`
	if string(raw) != want {
		t.Fatalf("outbound =\n%s\nwant\n%s", raw, want)
	}
}

func TestNormalizeTargets(t *testing.T) {
	got, err := NormalizeTargets([]string{" 192.168.1.7/24", "192.168.1.0/24", "10.0.0.5", "", "fd00::1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"192.168.1.0/24", "10.0.0.5", "fd00::1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"192.168.1.0/33", "home", "10.0.0.256"} {
		if _, err := NormalizeTargets([]string{bad}); err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}
}

const pastedOVPN = `client
dev tun0
proto udp
remote home.example.net 1194
redirect-gateway def1
script-security 2
up /etc/openvpn/update-resolv-conf
route 0.0.0.0 0.0.0.0
auth-user-pass
<ca>
-----BEGIN CERTIFICATE-----
up evil
-----END CERTIFICATE-----
</ca>
`

func TestRenderOpenVPNKeepsOnlyTheHomeRoutes(t *testing.T) {
	conf, err := RenderOpenVPN(&Tunnel{Id: 3, OpenVPN: pastedOVPN, Username: "u", Routes: []string{"192.168.1.0/24", "10.0.0.5", "fd00::/64"}}, "/x/auth")
	if err != nil {
		t.Fatal(err)
	}
	want := `client
proto udp
remote home.example.net 1194
<ca>
-----BEGIN CERTIFICATE-----
up evil
-----END CERTIFICATE-----
</ca>
dev mph3
dev-type tun
route-nopull
script-security 1
auth-user-pass /x/auth
route 192.168.1.0 255.255.255.0
route 10.0.0.5 255.255.255.255
`
	if conf != want {
		t.Fatalf("conf =\n%s\nwant\n%s", conf, want)
	}
	if _, err := RenderOpenVPN(&Tunnel{OpenVPN: "client\n"}, ""); err == nil || err.Error() != "openvpn config has no remote line" {
		t.Fatalf("err = %v", err)
	}
}

func TestL2TPFiles(t *testing.T) {
	ipsecDir = "/ipsec"
	tun := &Tunnel{Id: 2, Kind: KindL2TP, Server: "1.2.3.4", Username: "me", Password: "pw", PSK: "key", Routes: []string{"192.168.1.0/24"}}
	files, err := L2TPFiles(tun, "/d")
	if err != nil {
		t.Fatal(err)
	}
	if got := files["/d/ip-up"]; got != "#!/bin/sh\nip route replace 192.168.1.0/24 dev \"$1\"\n" {
		t.Fatalf("ip-up = %q", got)
	}
	if got := files["/ipsec/x-ui-home-2.secrets"]; got != "%any 1.2.3.4 : PSK \"key\"\n" {
		t.Fatalf("secrets = %q", got)
	}
	if !strings.Contains(files["/d/options"], "ifname mph2\nname \"me\"\npassword \"pw\"\n") {
		t.Fatalf("options = %q", files["/d/options"])
	}
	tun.PSK = ""
	files, _ = L2TPFiles(tun, "/d")
	if _, ok := files["/ipsec/x-ui-home-2.conf"]; ok || len(files) != 3 {
		t.Fatalf("without a PSK no IPsec files are written: %v", files)
	}
	tun.Password = `p"w`
	if _, err := L2TPFiles(tun, "/d"); err == nil || !strings.HasPrefix(err.Error(), "password") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyOpenVPNWritesAndStartsThenTearsDown(t *testing.T) {
	root := t.TempDir()
	baseDir, unitDir = filepath.Join(root, "home"), filepath.Join(root, "units")
	var cmds []string
	run = func(name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil
	}
	lookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	t.Cleanup(func() { run, lookPath = runCommand, exec.LookPath })

	tun := &Tunnel{Id: 4, Kind: KindOpenVPN, Enable: true, OpenVPN: pastedOVPN, Username: "u", Password: "p", Routes: []string{"192.168.1.0/24"}}
	if err := Apply(tun); err != nil {
		t.Fatal(err)
	}
	if want := []string{"systemctl daemon-reload", "systemctl enable x-ui-home-4", "systemctl restart x-ui-home-4"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands = %v", cmds)
	}
	auth, err := os.ReadFile(filepath.Join(baseDir, "4", "auth"))
	if err != nil || string(auth) != "u\np\n" {
		t.Fatalf("auth = %q, %v", auth, err)
	}
	unit, _ := os.ReadFile(filepath.Join(unitDir, "x-ui-home-4.service"))
	if !strings.Contains(string(unit), "ExecStart=/usr/sbin/openvpn --config "+filepath.Join(baseDir, "4", "client.conf")) {
		t.Fatalf("unit = %s", unit)
	}

	cmds = nil
	tun.Enable = false
	if err := Apply(tun); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "4")); !os.IsNotExist(err) {
		t.Fatalf("tunnel dir still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "x-ui-home-4.service")); !os.IsNotExist(err) {
		t.Fatalf("unit still there: %v", err)
	}
	if want := []string{"systemctl disable --now x-ui-home-4", "systemctl daemon-reload"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("teardown commands = %v", cmds)
	}
}

func TestEnsurePackagesInstallsOnlyWhatIsMissing(t *testing.T) {
	var cmds []string
	run = func(name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil
	}
	lookPath = func(bin string) (string, error) {
		if bin == "xl2tpd" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + bin, nil
	}
	t.Cleanup(func() { run, lookPath = runCommand, exec.LookPath })
	if err := ensurePackages(map[string]string{"xl2tpd": "xl2tpd", "pppd": "ppp"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"apt-get update -qq", "env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq xl2tpd"}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands = %v", cmds)
	}
}
