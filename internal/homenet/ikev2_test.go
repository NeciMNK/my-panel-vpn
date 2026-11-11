package homenet

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const routerCA = `-----BEGIN CERTIFICATE-----
MIIBeDCCAR+gAwIBAgIUR6xi17frsjUSus+uOUaGqItpXSAwCgYIKoZIzj0EAwIw
EjEQMA4GA1UEAwwHaG9tZSBDQTAeFw0yNjExMTEwNDM5NDZaFw0zNjExMDgwNDM5
NDZaMBIxEDAOBgNVBAMMB2hvbWUgQ0EwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AASDbJ5bIh3tb0rHDdnQ4IZwuReYoI2wKSV70WRtyl+JA91Bq+2gu4WBPFMs9ycx
fFAwu2zpTngfKXRVcLivXhdQo1MwUTAdBgNVHQ4EFgQUJaoGl6mmkGrmT/rdeW0m
Jgtq1lYwHwYDVR0jBBgwFoAUJaoGl6mmkGrmT/rdeW0mJgtq1lYwDwYDVR0TAQH/
BAUwAwEB/zAKBggqhkjOPQQDAgNHADBEAiApkxR/a4XbcHKKI/ObjuPHQRhkCeNW
/Nr5o/YQGhbw8AIgRtZ5IVr1gLmT0PAVT707wCAIMeyX2AQpV6Wyfm9Fz4Q=
-----END CERTIFICATE-----
`

func ikev2Paths(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	prev := [2]string{swanctlDir, eapPlugin}
	swanctlDir = filepath.Join(root, "swanctl")
	eapPlugin = filepath.Join(root, "eap-mschapv2.so")
	t.Cleanup(func() { swanctlDir, eapPlugin = prev[0], prev[1] })
	return root
}

func TestIKEv2FilesWithPSK(t *testing.T) {
	ikev2Paths(t)
	tun := &Tunnel{Id: 5, Kind: KindIKEv2, Server: "home.example.net", Username: "gw", PSK: "key", Routes: []string{"192.168.1.0/24", "192.168.1.10"}}
	files, err := IKEv2Files(tun, "/d")
	if err != nil {
		t.Fatal(err)
	}
	want := `connections {
	x-ui-home-5 {
		version = 2
		remote_addrs = home.example.net
		vips = 0.0.0.0
		proposals = default, aes256-sha256-modp2048, aes256-sha1-modp1024
		mobike = no
		dpd_delay = 30s
		keyingtries = 0
		local {
			auth = psk
			id = "gw"
		}
		remote {
			auth = psk
			id = %any
		}
		children {
			x-ui-home-5 {
				remote_ts = 192.168.1.0/24, 192.168.1.10/32
				esp_proposals = default, aes256-sha256, aes256-sha1
				if_id_in = 28005
				if_id_out = 28005
				updown = /d/updown
				dpd_action = restart
				close_action = start
			}
		}
	}
}
secrets {
	ike-x-ui-home-5 {
		id = "gw"
		secret = "key"
	}
}
`
	if got := files[filepath.Join(swanctlDir, "x-ui-home-5.conf")]; got != want {
		t.Fatalf("swanctl conf =\n%s\nwant\n%s", got, want)
	}
	if _, ok := files["/d/ca.pem"]; ok || len(files) != 2 {
		t.Fatalf("a PSK tunnel writes only the swanctl file and updown: %v", files)
	}
}

// Sockets bound to mph5 must leave from the router-assigned address: only it
// matches the IPsec policy, any other source would leak past the tunnel.
func TestIKEv2UpdownRoutesFromTheVirtualIP(t *testing.T) {
	ikev2Paths(t)
	tun := &Tunnel{Id: 5, Kind: KindIKEv2, Server: "1.2.3.4", PSK: "key", Routes: []string{"192.168.1.0/24", "fd00::/64"}}
	files, err := IKEv2Files(tun, "/d")
	if err != nil {
		t.Fatal(err)
	}
	want := `#!/bin/sh
dev=mph5
case "$PLUTO_VERB" in
up-client*)
	ip link show "$dev" >/dev/null 2>&1 || ip link add "$dev" type xfrm dev "$PLUTO_INTERFACE" if_id 28005 || exit 1
	ip link set "$dev" mtu 1400 up
	sysctl -qw net.ipv4.conf.mph5.rp_filter=2
	src="$PLUTO_MY_SOURCEIP4_1"
	if [ -n "$src" ]; then
		ip -4 addr replace "$src" dev "$dev"
		ip -4 rule del oif mph5 lookup 28005 2>/dev/null
		ip -4 rule add oif mph5 lookup 28005
		ip -4 route replace 192.168.1.0/24 dev mph5 src "$src" table 28005
	fi
	src="$PLUTO_MY_SOURCEIP6_1"
	if [ -n "$src" ]; then
		ip -6 addr replace "$src" dev "$dev" nodad
		ip -6 rule del oif mph5 lookup 28005 2>/dev/null
		ip -6 rule add oif mph5 lookup 28005
		ip -6 route replace fd00::/64 dev mph5 src "$src" table 28005
	fi
	;;
down-client*)
	ip link del "$dev" 2>/dev/null
	;;
esac
exit 0
`
	if got := files["/d/updown"]; got != want {
		t.Fatalf("updown =\n%s\nwant\n%s", got, want)
	}
	if conf := files[filepath.Join(swanctlDir, "x-ui-home-5.conf")]; !strings.Contains(conf, "\t\tvips = 0.0.0.0, ::\n") ||
		!strings.Contains(conf, "\tike-x-ui-home-5 {\n\t\tsecret = \"key\"\n\t}\n") {
		t.Fatalf("both families need a virtual IP; a PSK without username matches any id:\n%s", conf)
	}
}

func TestIKEv2FilesWithEAP(t *testing.T) {
	ikev2Paths(t)
	tun := &Tunnel{Id: 6, Kind: KindIKEv2, Server: "home.example.net", Username: "anna", Password: "pw", CA: routerCA, Routes: []string{"10.0.0.0/8"}}
	files, err := IKEv2Files(tun, "/d")
	if err != nil {
		t.Fatal(err)
	}
	conf := files[filepath.Join(swanctlDir, "x-ui-home-6.conf")]
	for _, part := range []string{
		"\t\tlocal {\n\t\t\tauth = eap-mschapv2\n\t\t\teap_id = \"anna\"\n\t\t}\n",
		"\t\tremote {\n\t\t\tauth = pubkey\n\t\t\tid = \"home.example.net\"\n\t\t\tcacerts = /d/ca.pem\n\t\t}\n",
		"\teap-x-ui-home-6 {\n\t\tid = \"anna\"\n\t\tsecret = \"pw\"\n\t}\n",
	} {
		if !strings.Contains(conf, part) {
			t.Fatalf("swanctl conf lacks %q:\n%s", part, conf)
		}
	}
	if files["/d/ca.pem"] != routerCA {
		t.Fatalf("ca.pem = %q", files["/d/ca.pem"])
	}
	tun.Server = "1.2.3.4"
	files, _ = IKEv2Files(tun, "/d")
	if conf := files[filepath.Join(swanctlDir, "x-ui-home-6.conf")]; !strings.Contains(conf, "\t\t\tauth = pubkey\n\t\t\tid = %any\n") {
		t.Fatalf("a router reached by IP is checked by its CA only:\n%s", conf)
	}
}

func TestIKEv2FilesRejects(t *testing.T) {
	ikev2Paths(t)
	base := Tunnel{Id: 6, Kind: KindIKEv2, Server: "1.2.3.4", Username: "anna", Password: "pw", CA: routerCA, Routes: []string{"10.0.0.0/8"}}
	cases := map[string]func(*Tunnel){
		"server address is required":                 func(t *Tunnel) { t.Server = "" },
		"server must be an IP address or a hostname": func(t *Tunnel) { t.Server = "a b}" },
		"pre-shared key, or a username and password": func(t *Tunnel) { t.Password = "" },
		"router's CA certificate":                    func(t *Tunnel) { t.CA = "" },
		"is not a PEM certificate":                   func(t *Tunnel) { t.CA = "hello" },
		"password must not contain quotes":           func(t *Tunnel) { t.Password = `p"w` },
	}
	for want, mutate := range cases {
		t.Run(want, func(t *testing.T) {
			tun := base
			mutate(&tun)
			if _, err := IKEv2Files(&tun, "/d"); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to mention %q", err, want)
			}
		})
	}
}

// Debian 13 ships no strongswan-starter: charon-systemd runs charon there as
// strongswan.service, and the unit must follow it or the tunnel never starts.
func TestApplyIKEv2WithoutStarterUsesCharonSystemd(t *testing.T) {
	root := ikev2Paths(t)
	baseDir, unitDir = filepath.Join(root, "home"), filepath.Join(root, "units")
	var cmds []string
	run = func(name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if name == "swanctl" {
			return os.ErrNotExist
		}
		return nil
	}
	installed := false
	lookPath = func(bin string) (string, error) {
		if bin == "ipsec" || !installed && (bin == "swanctl" || bin == "charon-systemd") {
			return "", os.ErrNotExist
		}
		return "/usr/sbin/" + bin, nil
	}
	t.Cleanup(func() { run, lookPath = runCommand, exec.LookPath })

	tun := &Tunnel{Id: 8, Kind: KindIKEv2, Enable: true, Server: "1.2.3.4", PSK: "key", Routes: []string{"192.168.1.0/24"}}
	if err := Apply(tun); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apt-get update -qq",
		"env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq charon-systemd strongswan-swanctl",
		"swanctl --stats", "systemctl restart strongswan",
		"systemctl daemon-reload", "systemctl enable x-ui-home-8", "systemctl restart x-ui-home-8",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands =\n%v\nwant\n%v", cmds, want)
	}
	unit, _ := os.ReadFile(filepath.Join(unitDir, "x-ui-home-8.service"))
	if !strings.Contains(string(unit), "Wants=network-online.target strongswan.service\nPartOf=strongswan.service\n") {
		t.Fatalf("unit does not follow charon-systemd:\n%s", unit)
	}
}

func TestApplyIKEv2InstallsLoadsAndTearsDown(t *testing.T) {
	root := ikev2Paths(t)
	baseDir, unitDir = filepath.Join(root, "home"), filepath.Join(root, "units")
	var cmds []string
	run = func(name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if name == "swanctl" && args[0] == "--stats" {
			return os.ErrNotExist
		}
		return nil
	}
	lookPath = func(bin string) (string, error) {
		if bin == "swanctl" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + bin, nil
	}
	t.Cleanup(func() { run, lookPath = runCommand, exec.LookPath })

	tun := &Tunnel{Id: 7, Kind: KindIKEv2, Enable: true, Server: "1.2.3.4", Username: "anna", Password: "pw", CA: routerCA, Routes: []string{"192.168.1.0/24"}}
	if err := Apply(tun); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apt-get update -qq",
		"env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq libcharon-extra-plugins strongswan-swanctl",
		"swanctl --stats", "systemctl restart strongswan-starter",
		"systemctl daemon-reload", "systemctl enable x-ui-home-7", "systemctl restart x-ui-home-7",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands =\n%v\nwant\n%v", cmds, want)
	}
	unit, _ := os.ReadFile(filepath.Join(unitDir, "x-ui-home-7.service"))
	for _, part := range []string{
		"Wants=network-online.target strongswan-starter.service\nPartOf=strongswan-starter.service\n",
		"Type=oneshot\nRemainAfterExit=yes\nExecStart=" + filepath.Join(baseDir, "7", "start") + "\n",
		"ExecStop=-/usr/sbin/swanctl --terminate --ike x-ui-home-7 --timeout 10\nRestart=on-failure\n",
	} {
		if !strings.Contains(string(unit), part) {
			t.Fatalf("unit lacks %q:\n%s", part, unit)
		}
	}
	start, err := os.Stat(filepath.Join(baseDir, "7", "start"))
	if err != nil || start.Mode().Perm() != 0o700 {
		t.Fatalf("start script: %v, %v", start, err)
	}

	cmds = nil
	tun.Enable = false
	if err := Apply(tun); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(swanctlDir, "x-ui-home-7.conf")); !os.IsNotExist(err) {
		t.Fatalf("swanctl conf still there: %v", err)
	}
	want = []string{
		"systemctl disable --now x-ui-home-7",
		"ip -4 rule del oif mph7 lookup 28007", "ip -4 route flush table 28007",
		"ip -6 rule del oif mph7 lookup 28007", "ip -6 route flush table 28007",
		"swanctl --load-all --noprompt", "ip link del mph7",
		"systemctl daemon-reload",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("teardown commands =\n%v\nwant\n%v", cmds, want)
	}
}
