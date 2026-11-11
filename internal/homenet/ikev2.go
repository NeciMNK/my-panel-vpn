package homenet

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

// Paths are variables so tests can render into a temp dir.
var (
	swanctlDir = "/etc/swanctl/conf.d"
	eapPlugin  = "/usr/lib/ipsec/plugins/libstrongswan-eap-mschapv2.so"
)

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

func ikev2Server(server string) (isIP bool, err error) {
	if server == "" {
		return false, errors.New("ikev2: server address is required")
	}
	if _, err := netip.ParseAddr(server); err == nil {
		return true, nil
	}
	if !hostnameRe.MatchString(server) {
		return false, errors.New("ikev2: server must be an IP address or a hostname")
	}
	return false, nil
}

func checkCA(text string) error {
	block, _ := pem.Decode([]byte(text))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("ikev2: the router's CA certificate is not a PEM certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("ikev2: the router's CA certificate: %w", err)
	}
	return nil
}

func splitFamilies(routes []string) (v4, v6 []string) {
	for _, r := range routes {
		p, err := netip.ParsePrefix(asPrefix(r))
		if err != nil {
			continue
		}
		if p.Addr().Is4() {
			v4 = append(v4, p.Masked().String())
		} else {
			v6 = append(v6, p.Masked().String())
		}
	}
	return v4, v6
}

// IKEv2Files renders an IKEv2 client keyed by absolute path: a swanctl
// connection bound to XFRM interface mph<id>, its updown script and CA.
func IKEv2Files(t *Tunnel, dir string) (map[string]string, error) {
	for field, v := range map[string]string{"username": t.Username, "password": t.Password, "psk": t.PSK} {
		if err := checkPPPValue(field, v); err != nil {
			return nil, err
		}
	}
	serverIsIP, err := ikev2Server(t.Server)
	if err != nil {
		return nil, err
	}
	eap := t.PSK == ""
	if eap && (t.Username == "" || t.Password == "") {
		return nil, errors.New("ikev2: set a pre-shared key, or a username and password")
	}
	if eap && strings.TrimSpace(t.CA) == "" {
		return nil, errors.New("ikev2: login with a username needs the router's CA certificate")
	}
	if eap {
		if err := checkCA(t.CA); err != nil {
			return nil, err
		}
	}
	v4, v6 := splitFamilies(t.Routes)
	if len(v4)+len(v6) == 0 {
		return nil, errors.New("ikev2: no home addresses to route")
	}
	var vips []string
	if len(v4) > 0 {
		vips = append(vips, "0.0.0.0")
	}
	if len(v6) > 0 {
		vips = append(vips, "::")
	}

	conn := unitName(t.Id)
	updown := filepath.Join(dir, "updown")
	var local, remote, secrets string
	if eap {
		local = fmt.Sprintf("\t\t\tauth = eap-mschapv2\n\t\t\teap_id = \"%s\"\n", t.Username)
		remoteID := "%any"
		if !serverIsIP {
			remoteID = `"` + t.Server + `"`
		}
		remote = fmt.Sprintf("\t\t\tauth = pubkey\n\t\t\tid = %s\n\t\t\tcacerts = %s\n", remoteID, filepath.Join(dir, "ca.pem"))
		secrets = fmt.Sprintf("\teap-%s {\n\t\tid = \"%s\"\n\t\tsecret = \"%s\"\n\t}\n", conn, t.Username, t.Password)
	} else {
		local = "\t\t\tauth = psk\n"
		id := ""
		if t.Username != "" {
			local += fmt.Sprintf("\t\t\tid = \"%s\"\n", t.Username)
			id = fmt.Sprintf("\t\tid = \"%s\"\n", t.Username)
		}
		remote = "\t\t\tauth = psk\n\t\t\tid = %any\n"
		secrets = fmt.Sprintf("\tike-%s {\n%s\t\tsecret = \"%s\"\n\t}\n", conn, id, t.PSK)
	}
	ifID := RouteTable(t.Id)
	swanctl := fmt.Sprintf(`connections {
	%[1]s {
		version = 2
		remote_addrs = %[2]s
		vips = %[3]s
		proposals = default, aes256-sha256-modp2048, aes256-sha1-modp1024
		mobike = no
		dpd_delay = 30s
		keyingtries = 0
		local {
%[4]s		}
		remote {
%[5]s		}
		children {
			%[1]s {
				remote_ts = %[6]s
				esp_proposals = default, aes256-sha256, aes256-sha1
				if_id_in = %[7]d
				if_id_out = %[7]d
				updown = %[8]s
				dpd_action = restart
				close_action = start
			}
		}
	}
}
secrets {
%[9]s}
`, conn, t.Server, strings.Join(vips, ", "), local, remote, strings.Join(append(v4, v6...), ", "), ifID, updown, secrets)

	files := map[string]string{
		filepath.Join(swanctlDir, conn+".conf"): swanctl,
		updown:                                  ikev2Updown(t.Id, v4, v6),
	}
	if eap {
		files[filepath.Join(dir, "ca.pem")] = t.CA
	}
	return files, nil
}

// ikev2Updown creates mph<id> when the SA comes up and routes the home targets
// in its own table from the virtual IP, the only source the policy matches.
func ikev2Updown(id int, v4, v6 []string) string {
	dev, table := IfName(id), RouteTable(id)
	var b strings.Builder
	fmt.Fprintf(&b, `#!/bin/sh
dev=%[1]s
case "$PLUTO_VERB" in
up-client*)
	ip link show "$dev" >/dev/null 2>&1 || ip link add "$dev" type xfrm dev "$PLUTO_INTERFACE" if_id %[2]d || exit 1
	ip link set "$dev" mtu 1400 up
	sysctl -qw net.ipv4.conf.%[1]s.rp_filter=2
`, dev, table)
	for _, fam := range []struct {
		flag, n, nodad string
		routes         []string
	}{{"-4", "4", "", v4}, {"-6", "6", " nodad", v6}} {
		if len(fam.routes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\tsrc=\"$PLUTO_MY_SOURCEIP%s_1\"\n\tif [ -n \"$src\" ]; then\n", fam.n)
		fmt.Fprintf(&b, "\t\tip %s addr replace \"$src\" dev \"$dev\"%s\n", fam.flag, fam.nodad)
		fmt.Fprintf(&b, "\t\tip %[1]s rule del oif %[2]s lookup %[3]d 2>/dev/null\n\t\tip %[1]s rule add oif %[2]s lookup %[3]d\n", fam.flag, dev, table)
		for _, r := range fam.routes {
			fmt.Fprintf(&b, "\t\tip %s route replace %s dev %s src \"$src\" table %d\n", fam.flag, r, dev, table)
		}
		b.WriteString("\tfi\n")
	}
	b.WriteString(`	;;
down-client*)
	ip link del "$dev" 2>/dev/null
	;;
esac
exit 0
`)
	return b.String()
}

// swanctlStart loads the connection into the running charon and
// starts it; charon itself retries and restarts it from then on.
func swanctlStart(id int) string {
	return fmt.Sprintf(`#!/bin/sh
i=0
until swanctl --stats >/dev/null 2>&1; do
	i=$((i+1))
	[ "$i" -ge 30 ] && exit 1
	sleep 1
done
swanctl --load-all --noprompt || exit 1
swanctl --initiate --child %[1]s --timeout 20 >/dev/null 2>&1
exit 0
`, unitName(id))
}
