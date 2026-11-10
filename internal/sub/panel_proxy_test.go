package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// "Through my VPN" must dial the client's node inbounds at the node's address and
// never this server's own inbound, which would loop the panel back to itself.
func TestPanelProxyClientModeRoutesThroughNodeInboundsOnly(t *testing.T) {
	initSubDB(t)
	service.RegisterSubLinkProvider(NewLinkProvider())
	t.Cleanup(func() { service.RegisterSubLinkProvider(nil) })
	db := database.GetDB()

	node := &model.Node{Name: "de-fra-1", Address: "203.0.113.50", Port: 2053, ApiToken: "x", Enable: true}
	if err := db.Create(node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	const email, uuid = "alice", "11111111-2222-4333-8444-555555555555"
	clients := `{"clients":[{"id":"` + uuid + `","email":"` + email + `","subId":"alice-sub","enable":true}]}`
	stream := `{"network":"tcp","security":"none"}`
	local := &model.Inbound{UserId: 1, Tag: "local-vmess", Enable: true, Listen: "198.51.100.9", Port: 4431, Protocol: model.VMESS, Remark: "here", Settings: clients, StreamSettings: stream}
	remote := &model.Inbound{UserId: 1, Tag: "node-vmess", Enable: true, Port: 4432, Protocol: model.VMESS, Remark: "Frankfurt", Settings: clients, StreamSettings: stream, NodeID: &node.Id}
	rec := &model.ClientRecord{Email: email, SubID: "alice-sub", UUID: uuid, Enable: true}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	for _, ib := range []*model.Inbound{local, remote} {
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("seed inbound: %v", err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: ib.Id}).Error; err != nil {
			t.Fatalf("seed client_inbound: %v", err)
		}
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: remote.Id, Email: email, Enable: true}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	bob := &model.ClientRecord{Email: "bob", SubID: "bob-sub", UUID: "99999999-2222-4333-8444-555555555555", Enable: true}
	if err := db.Create(bob).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: bob.Id, InboundId: local.Id}).Error; err != nil {
		t.Fatalf("seed client_inbound: %v", err)
	}

	proxy := &service.PanelProxyService{}
	if got, err := proxy.ClientCandidates(); err != nil || len(got) != 1 || got[0] != email {
		t.Fatalf("ClientCandidates = %v, %v; want only %s, who has a node inbound", got, err, email)
	}
	if err := proxy.Set(service.PanelProxyModeClient, "bob"); err == nil {
		t.Fatal("Set accepted a client with no inbound on another server")
	}
	if err := proxy.Set(service.PanelProxyModeClient, email); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cfg, err := (&service.XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}

	var outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			Vnext []struct {
				Address string `json:"address"`
				Port    int    `json:"port"`
			} `json:"vnext"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatalf("outbounds: %v", err)
	}
	var exits []string
	for _, ob := range outbounds {
		if !strings.HasPrefix(ob.Tag, service.PanelProxyTagPrefix) {
			continue
		}
		for _, v := range ob.Settings.Vnext {
			exits = append(exits, ob.Protocol+" "+v.Address)
			if v.Port != remote.Port {
				t.Errorf("exit %s dials port %d, want the node inbound's %d", ob.Tag, v.Port, remote.Port)
			}
		}
	}
	if len(exits) != 1 || exits[0] != "vmess 203.0.113.50" {
		t.Fatalf("panel proxy exits = %v, want only the node inbound at the node address", exits)
	}

	var routing struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if routing.Rules[0]["balancerTag"] != service.PanelProxyBalancerTag {
		t.Fatalf("first rule = %v, want the panel egress routed to %s", routing.Rules[0], service.PanelProxyBalancerTag)
	}
}
