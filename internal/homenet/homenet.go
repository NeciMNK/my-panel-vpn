// Package homenet turns a home network (subnets behind a WireGuard, OpenVPN,
// L2TP/IPsec or IKEv2 tunnel) into Xray pieces and, except WireGuard, a system tunnel.
package homenet

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

const (
	KindWireGuard = "wireguard"
	KindOpenVPN   = "openvpn"
	KindL2TP      = "l2tp"
	KindIKEv2     = "ikev2"
)

// Kinds lists the tunnel types a home network can use.
var Kinds = []string{KindWireGuard, KindOpenVPN, KindL2TP, KindIKEv2}

// IfName is the system interface of a home network's system tunnel;
// Linux caps interface names at 15 bytes.
func IfName(id int) string { return fmt.Sprintf("mph%d", id) }

// NormalizeTargets validates subnets ("192.168.1.0/24") and single addresses
// ("192.168.1.10"), returning them in canonical form without duplicates.
func NormalizeTargets(items []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		var canon string
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("%q is not a subnet like 192.168.1.0/24", item)
			}
			canon = p.Masked().String()
		} else {
			a, err := netip.ParseAddr(item)
			if err != nil {
				return nil, fmt.Errorf("%q is not an IP address", item)
			}
			canon = a.String()
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	return out, nil
}

// asPrefix turns a target into a CIDR, for route tables and WireGuard allowedIPs.
func asPrefix(target string) string {
	if strings.Contains(target, "/") {
		return target
	}
	a, err := netip.ParseAddr(target)
	if err != nil {
		return target
	}
	return netip.PrefixFrom(a, a.BitLen()).String()
}

// WireGuard is the part of a standard wg-quick config Xray needs.
type WireGuard struct {
	PrivateKey   string
	Address      []string
	MTU          int
	PublicKey    string
	PresharedKey string
	Endpoint     string
	Keepalive    int
}

// ParseWireGuard reads a wg-quick config ([Interface] + one [Peer]) as the home
// router exports it.
func ParseWireGuard(text string) (*WireGuard, error) {
	wg := &WireGuard{}
	section := ""
	peers := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			if section == "peer" {
				peers++
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("wireguard config: cannot read line %q", line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch section + "." + key {
		case "interface.privatekey":
			wg.PrivateKey = value
		case "interface.address":
			for _, a := range strings.Split(value, ",") {
				if a = strings.TrimSpace(a); a != "" {
					wg.Address = append(wg.Address, a)
				}
			}
		case "interface.mtu":
			wg.MTU, _ = strconv.Atoi(value)
		case "peer.publickey":
			wg.PublicKey = value
		case "peer.presharedkey":
			wg.PresharedKey = value
		case "peer.endpoint":
			wg.Endpoint = value
		case "peer.persistentkeepalive":
			wg.Keepalive, _ = strconv.Atoi(value)
		}
	}
	switch {
	case peers != 1:
		return nil, errors.New("wireguard config: exactly one [Peer] is supported")
	case wg.PrivateKey == "" || wg.PublicKey == "":
		return nil, errors.New("wireguard config: PrivateKey and the peer's PublicKey are required")
	case wg.Endpoint == "":
		return nil, errors.New("wireguard config: the peer needs an Endpoint (the home router's address:port)")
	case len(wg.Address) == 0:
		return nil, errors.New("wireguard config: Address is required")
	}
	if _, _, err := net.SplitHostPort(wg.Endpoint); err != nil {
		return nil, fmt.Errorf("wireguard config: Endpoint %q: %w", wg.Endpoint, err)
	}
	return wg, nil
}

// Outbound is an Xray userspace WireGuard outbound reaching only targets, so a
// home router that pushes 0.0.0.0/0 cannot pull the server's traffic home.
func (wg *WireGuard) Outbound(tag string, targets []string) map[string]any {
	allowed := make([]string, 0, len(targets))
	for _, t := range targets {
		allowed = append(allowed, asPrefix(t))
	}
	peer := map[string]any{"publicKey": wg.PublicKey, "endpoint": wg.Endpoint, "allowedIPs": allowed}
	if wg.PresharedKey != "" {
		peer["preSharedKey"] = wg.PresharedKey
	}
	if wg.Keepalive > 0 {
		peer["keepAlive"] = wg.Keepalive
	}
	settings := map[string]any{
		"secretKey":   wg.PrivateKey,
		"address":     wg.Address,
		"peers":       []any{peer},
		"noKernelTun": true,
	}
	if wg.MTU > 0 {
		settings["mtu"] = wg.MTU
	}
	return map[string]any{"tag": tag, "protocol": "wireguard", "settings": settings}
}

// InterfaceOutbound sends traffic out of a system tunnel's interface. Freedom
// blocks private IPs for proxied users by default, so the home targets are allowed.
func InterfaceOutbound(tag string, id int, targets []string) map[string]any {
	allowed := make([]string, 0, len(targets))
	for _, t := range targets {
		allowed = append(allowed, asPrefix(t))
	}
	return map[string]any{
		"tag":            tag,
		"protocol":       "freedom",
		"settings":       map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": allowed}}},
		"streamSettings": map[string]any{"sockopt": map[string]any{"interface": IfName(id)}},
	}
}
