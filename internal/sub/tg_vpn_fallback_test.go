package sub

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type tgVpnFixture struct {
	t        *testing.T
	node     *model.Node
	probes   int
	probeErr error
	fallback *service.TgVpnFallback
}

// newTgVpnFixture seeds a master with a local inbound and node VMess, MTProto and
// hidden cascade-link (mp-*) inbounds, with the bot on and no proxy configured.
func newTgVpnFixture(t *testing.T) *tgVpnFixture {
	t.Helper()
	initSubDB(t)
	service.RegisterSubLinkProvider(NewLinkProvider())
	t.Cleanup(func() { service.RegisterSubLinkProvider(nil) })
	db := database.GetDB()

	// Offline: client pushes defer to the reconcile instead of dialing the node.
	node := &model.Node{Name: "de-fra-1", Address: "203.0.113.50", Port: 2053, ApiToken: "x", Enable: true, Status: "offline"}
	if err := db.Create(node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	empty := `{"clients":[]}`
	stream := `{"network":"tcp","security":"none"}`
	for _, ib := range []*model.Inbound{
		{UserId: 1, Tag: "local-vmess", Enable: true, Port: 4431, Protocol: model.VMESS, Settings: empty, StreamSettings: stream},
		{UserId: 1, Tag: "node-vmess", Enable: true, Port: 4432, Protocol: model.VMESS, Settings: empty, StreamSettings: stream, NodeID: &node.Id},
		{UserId: 1, Tag: "node-mtproto", Enable: true, Port: 4433, Protocol: model.MTProto, Settings: empty, NodeID: &node.Id},
		{UserId: 1, Tag: "mp-cascade-1-in-n1", Enable: true, Port: 4434, Protocol: model.VLESS, Settings: empty, StreamSettings: stream, NodeID: &node.Id},
	} {
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("seed inbound: %v", err)
		}
	}
	settings := service.SettingService{}
	if err := settings.SetTgbotEnabled(true); err != nil {
		t.Fatalf("enable bot: %v", err)
	}
	f := &tgVpnFixture{t: t, node: node, probeErr: errors.New("i/o timeout")}
	f.fallback = &service.TgVpnFallback{Probe: func(context.Context) error {
		f.probes++
		return f.probeErr
	}}
	return f
}

func (f *tgVpnFixture) run() bool {
	f.t.Helper()
	changed, err := f.fallback.Run(context.Background())
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return changed
}

func (f *tgVpnFixture) proxyMode() (string, string) {
	f.t.Helper()
	status, err := (&service.PanelProxyService{}).Status()
	if err != nil {
		f.t.Fatalf("Status: %v", err)
	}
	return status.Mode, status.Source
}

func (f *tgVpnFixture) techInboundTags() []string {
	f.t.Helper()
	var tags []string
	if err := database.GetDB().Model(&model.Inbound{}).
		Joins("JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ?", service.PanelTechClientEmail).
		Order("inbounds.tag").Pluck("inbounds.tag", &tags).Error; err != nil {
		f.t.Fatalf("tech inbounds: %v", err)
	}
	return tags
}

// Telegram blocked at the master: after two failed checks the panel must own a
// VPN client on the node, in the non-paying tech group, and route through it.
func TestTgVpnFallbackSwitchesToTechClientWhenTelegramIsBlocked(t *testing.T) {
	f := newTgVpnFixture(t)

	if f.run() {
		t.Fatal("one failed probe switched the panel to the VPN")
	}
	if mode, _ := f.proxyMode(); mode != service.PanelProxyModeOff {
		t.Fatalf("panel proxy mode after one failure = %q, want off", mode)
	}
	if !f.run() {
		t.Fatal("second failed probe did not ask for an Xray rebuild")
	}
	if mode, source := f.proxyMode(); mode != service.PanelProxyModeClient || source != service.PanelTechClientEmail {
		t.Fatalf("panel proxy = %q/%q, want client/%s", mode, source, service.PanelTechClientEmail)
	}
	if got, want := f.techInboundTags(), []string{"node-vmess"}; !slices.Equal(got, want) {
		t.Fatalf("tech client inbounds = %v, want %v: only node inbounds Xray can dial", got, want)
	}
	var rec model.ClientRecord
	if err := database.GetDB().Where("email = ?", service.PanelTechClientEmail).First(&rec).Error; err != nil {
		t.Fatalf("tech client: %v", err)
	}
	if rec.Group != service.PanelTechGroup || !rec.Enable || rec.SubID == "" {
		t.Fatalf("tech client group=%q enable=%v subId=%q, want group %q, enabled, with a subscription", rec.Group, rec.Enable, rec.SubID, service.PanelTechGroup)
	}
	var free int64
	if err := database.GetDB().Model(&model.LedgerFreeGroup{}).Where("name = ?", service.PanelTechGroup).Count(&free).Error; err != nil || free != 1 {
		t.Fatalf("tech group non-paying rows = %d (%v), want 1", free, err)
	}

	cfg, err := (&service.XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	var outbounds []struct {
		Tag      string `json:"tag"`
		Settings struct {
			Vnext []struct {
				Address string `json:"address"`
			} `json:"vnext"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatalf("outbounds: %v", err)
	}
	var exits []string
	for _, ob := range outbounds {
		if strings.HasPrefix(ob.Tag, service.PanelProxyTagPrefix) {
			for _, v := range ob.Settings.Vnext {
				exits = append(exits, v.Address)
			}
		}
	}
	if !slices.Equal(exits, []string{f.node.Address}) {
		t.Fatalf("panel proxy exits = %v, want the node at %s", exits, f.node.Address)
	}
}

// A reachable Telegram in between resets the count: two scattered blips are no filter.
func TestTgVpnFallbackNeedsConsecutiveFailures(t *testing.T) {
	f := newTgVpnFixture(t)
	f.run()
	f.probeErr = nil
	f.run()
	f.probeErr = errors.New("i/o timeout")
	if f.run() {
		t.Fatal("fail, ok, fail switched the panel to the VPN")
	}
	if mode, _ := f.proxyMode(); mode != service.PanelProxyModeOff {
		t.Fatalf("panel proxy mode = %q, want off", mode)
	}
}

// An admin-chosen route (or the option turned off) is never overridden or even probed.
func TestTgVpnFallbackLeavesAdminRoutingAlone(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{"bot proxy", "tgBotProxy", "socks5://127.0.0.1:1080"},
		{"custom Bot API server", "tgBotAPIServer", "https://tg.example.com"},
		{"panel outbound", "panelOutbound", "warp"},
		{"fallback off", "tgBotVpnFallback", "false"},
		{"bot off", "tgBotEnable", "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTgVpnFixture(t)
			if err := database.GetDB().Where("key = ?", tt.key).Delete(&model.Setting{}).Error; err != nil {
				t.Fatalf("clear %s: %v", tt.key, err)
			}
			if err := database.GetDB().Create(&model.Setting{Key: tt.key, Value: tt.val}).Error; err != nil {
				t.Fatalf("set %s: %v", tt.key, err)
			}
			f.run()
			f.run()
			if f.probes != 0 {
				t.Fatalf("probed Telegram %d times despite %s", f.probes, tt.name)
			}
			if mode, _ := f.proxyMode(); mode != service.PanelProxyModeOff {
				t.Fatalf("panel proxy mode = %q, want off", mode)
			}
		})
	}
}

// Once on the VPN, a node inbound added later must also carry the tech client, or
// the panel loses Telegram when the original node goes away.
func TestTgVpnFallbackAttachesTechClientToNewNodeInbounds(t *testing.T) {
	f := newTgVpnFixture(t)
	f.run()
	f.run()

	if f.run() {
		t.Fatal("Run with nothing new asked for an Xray rebuild")
	}
	added := &model.Inbound{UserId: 1, Tag: "node-trojan", Enable: true, Port: 4434, Protocol: model.Trojan, Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp","security":"none"}`, NodeID: &f.node.Id}
	if err := database.GetDB().Create(added).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if !f.run() {
		t.Fatal("attaching the tech client to a new node inbound did not ask for an Xray rebuild")
	}
	if got, want := f.techInboundTags(), []string{"node-trojan", "node-vmess"}; !slices.Equal(got, want) {
		t.Fatalf("tech client inbounds = %v, want %v", got, want)
	}
	if f.probes != 2 {
		t.Fatalf("probes = %d, want 2: no probing once the panel is on the VPN", f.probes)
	}
}
