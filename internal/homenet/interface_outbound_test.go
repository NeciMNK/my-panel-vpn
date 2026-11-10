package homenet

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/freedom"
)

// Xray's freedom blackholes private IPs for vless/vmess/trojan/ss users unless a
// final rule allows them, which left the home LAN unreachable through a tunnel.
func TestInterfaceOutboundAllowsOnlyTheHomeTargetsPastFreedomsPrivateBlock(t *testing.T) {
	raw, err := json.Marshal(InterfaceOutbound("mp-home-3-tunnel", 3, []string{"192.168.1.0/24", "10.0.0.5"}))
	if err != nil {
		t.Fatal(err)
	}
	var detour conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &detour); err != nil {
		t.Fatalf("xray rejects the outbound JSON: %v", err)
	}
	built, err := detour.Build()
	if err != nil {
		t.Fatalf("xray cannot build the outbound: %v", err)
	}
	inst, err := built.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := inst.(*freedom.Config)
	if !ok {
		t.Fatalf("proxy settings are %T, want freedom", inst)
	}
	allowed := func(ip string) bool {
		for _, r := range cfg.FinalRules {
			if r.Action != freedom.RuleAction_Allow {
				continue
			}
			m, err := geodata.IPReg.BuildIPMatcher(r.Ip)
			if err != nil {
				t.Fatal(err)
			}
			if m.Match(net.ParseIP(ip)) {
				return true
			}
		}
		return false
	}
	for ip, want := range map[string]bool{"192.168.1.40": true, "10.0.0.5": true, "10.0.0.6": false, "172.16.0.1": false} {
		if got := allowed(ip); got != want {
			t.Errorf("final rules allow %s = %v, want %v", ip, got, want)
		}
	}
}
