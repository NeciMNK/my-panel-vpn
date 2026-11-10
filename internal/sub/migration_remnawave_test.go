package sub

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Key pair from an independent X25519 implementation (Python cryptography).
const (
	rwRealityPrivate = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA"
	rwRealityPublic  = "B6N8vBQgk8i3VdwbEOhstCY3StFqqFPtC9_AsrhtHHw"
)

// A trimmed copy of what Remnawave 2.x returns from its REST API: two nodes on
// one Reality profile inbound, a Trojan inbound on one of them, one squad.
const rwSnapshotJSON = `{
 "configProfiles": [{
  "uuid": "a0000000-0000-4000-8000-000000000001", "name": "Default",
  "config": {"inbounds": [
   {"tag": "VLESS_REALITY", "port": 443, "protocol": "vless",
    "settings": {"clients": [], "decryption": "none"},
    "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]},
    "streamSettings": {"network": "raw", "security": "reality",
     "realitySettings": {"show": false, "xver": 0, "target": "www.microsoft.com:443",
      "serverNames": ["www.microsoft.com"], "privateKey": "` + rwRealityPrivate + `",
      "shortIds": ["6ba85179e30d4fc2"]}}},
   {"tag": "TROJAN_WS", "port": "8443", "protocol": "trojan",
    "settings": {"clients": []},
    "streamSettings": {"network": "ws", "security": "none", "wsSettings": {"path": "/tr"}}}
  ]},
  "inbounds": [
   {"uuid": "b0000000-0000-4000-8000-000000000001", "profileUuid": "a0000000-0000-4000-8000-000000000001", "tag": "VLESS_REALITY", "type": "vless", "port": 443, "rawInbound": null},
   {"uuid": "b0000000-0000-4000-8000-000000000002", "profileUuid": "a0000000-0000-4000-8000-000000000001", "tag": "TROJAN_WS", "type": "trojan", "port": 8443, "rawInbound": null}
  ]
 }],
 "internalSquads": [{"uuid": "c0000000-0000-4000-8000-000000000001", "name": "Default-Squad",
  "inbounds": [{"uuid": "b0000000-0000-4000-8000-000000000001"}, {"uuid": "b0000000-0000-4000-8000-000000000002"}]}],
 "nodes": [
  {"uuid": "d0000000-0000-4000-8000-000000000001", "name": "Germany", "address": "de.example.com", "viewPosition": 0, "isDisabled": false,
   "configProfile": {"activeConfigProfileUuid": "a0000000-0000-4000-8000-000000000001",
    "activeInbounds": [{"uuid": "b0000000-0000-4000-8000-000000000001", "tag": "VLESS_REALITY"}, {"uuid": "b0000000-0000-4000-8000-000000000002", "tag": "TROJAN_WS"}]}},
  {"uuid": "d0000000-0000-4000-8000-000000000002", "name": "Netherlands", "address": "185.0.0.2", "viewPosition": 1, "isDisabled": false,
   "configProfile": {"activeConfigProfileUuid": "a0000000-0000-4000-8000-000000000001",
    "activeInbounds": [{"uuid": "b0000000-0000-4000-8000-000000000001", "tag": "VLESS_REALITY"}]}}
 ],
 "hosts": [
  {"uuid": "e0000000-0000-4000-8000-000000000001", "viewPosition": 0, "remark": "🇩🇪 Germany", "address": "de.example.com", "port": 443,
   "path": null, "sni": "www.microsoft.com", "host": null, "alpn": null, "fingerprint": "chrome", "isDisabled": false,
   "securityLayer": "DEFAULT", "nodes": [], "inbound": {"configProfileUuid": "a0000000-0000-4000-8000-000000000001", "configProfileInboundUuid": "b0000000-0000-4000-8000-000000000001"}},
  {"uuid": "e0000000-0000-4000-8000-000000000002", "viewPosition": 1, "remark": "🇳🇱 Netherlands", "address": "nl.example.com", "port": 443,
   "path": null, "sni": "www.microsoft.com", "host": null, "alpn": null, "fingerprint": "chrome", "isDisabled": false,
   "securityLayer": "DEFAULT", "nodes": ["d0000000-0000-4000-8000-000000000002"], "inbound": {"configProfileUuid": "a0000000-0000-4000-8000-000000000001", "configProfileInboundUuid": "b0000000-0000-4000-8000-000000000001"}},
  {"uuid": "e0000000-0000-4000-8000-000000000003", "viewPosition": 2, "remark": "Trojan DE", "address": "de.example.com", "port": 8443,
   "path": "/tr", "sni": null, "host": null, "alpn": null, "fingerprint": null, "isDisabled": false,
   "securityLayer": "DEFAULT", "nodes": [], "inbound": {"configProfileUuid": "a0000000-0000-4000-8000-000000000001", "configProfileInboundUuid": "b0000000-0000-4000-8000-000000000002"}}
 ],
 "users": [
  {"id": 1, "shortUuid": "Zx3pQ9tLmN2kVb7c", "username": "alice", "status": "ACTIVE",
   "trafficLimitBytes": 107374182400, "trafficLimitStrategy": "MONTH", "expireAt": "2027-01-31T12:00:00.000Z",
   "telegramId": 123456789, "email": null, "description": "friend", "tag": null, "hwidDeviceLimit": 3,
   "trojanPassword": "tr0janPassw0rdAlice", "vlessUuid": "5f0a1c2e-3b4d-4e5f-8a9b-0c1d2e3f4a5b", "ssPassword": "ssAlice",
   "createdAt": "2025-03-10T08:00:00.000Z", "subscriptionUrl": "https://sub.example.com/Zx3pQ9tLmN2kVb7c",
   "activeInternalSquads": [{"uuid": "c0000000-0000-4000-8000-000000000001", "name": "Default-Squad"}],
   "userTraffic": {"usedTrafficBytes": 5368709120, "lifetimeUsedTrafficBytes": 9, "onlineAt": "2026-11-01T10:00:00.000Z"}},
  {"id": 2, "shortUuid": "Qw8eRt5yUi1oPa4s", "username": "bob", "status": "DISABLED",
   "trafficLimitBytes": 0, "trafficLimitStrategy": "NO_RESET", "expireAt": "2099-12-31T00:00:00.000Z",
   "telegramId": null, "description": null, "tag": null, "hwidDeviceLimit": null,
   "trojanPassword": "tr0janBob", "vlessUuid": "6a1b2c3d-4e5f-4a6b-9c7d-8e9f0a1b2c3d", "ssPassword": "ssBob",
   "createdAt": "2025-03-10T08:00:00.000Z", "subscriptionUrl": "https://sub.example.com/Qw8eRt5yUi1oPa4s",
   "activeInternalSquads": [{"uuid": "c0000000-0000-4000-8000-000000000001", "name": "Default-Squad"}],
   "usedTrafficBytes": 42}
 ]
}`

const (
	rwGermany     = "d0000000-0000-4000-8000-000000000001"
	rwNetherlands = "d0000000-0000-4000-8000-000000000002"
)

func loadRemnawaveSnapshot(t *testing.T) *service.RemnawaveSnapshot {
	t.Helper()
	snap, err := service.ParseRemnawaveSnapshot([]byte(rwSnapshotJSON))
	if err != nil {
		t.Fatalf("ParseRemnawaveSnapshot: %v", err)
	}
	return snap
}

// After importing a Remnawave panel, a user's subscription must hand out the
// same credentials, addresses and Reality parameters Remnawave did.
func TestRemnawaveImport_SubscriptionKeepsUserConfigs(t *testing.T) {
	seedSubDB(t)
	svc := &service.MigrationService{}
	targets := map[string]string{rwGermany: service.MigrationTargetLocal, rwNetherlands: service.MigrationTargetSkip}

	res, err := svc.ApplyRemnawave(loadRemnawaveSnapshot(t), targets, true, 1)
	if err != nil {
		t.Fatalf("ApplyRemnawave: %v", err)
	}
	if res.InboundsCreated != 2 || len(res.InboundErrors) != 0 {
		t.Fatalf("inbounds created=%d errors=%v, want 2 and none", res.InboundsCreated, res.InboundErrors)
	}
	if res.Clients.Created != 2 || len(res.Clients.Skipped) != 0 {
		t.Fatalf("clients created=%d skipped=%v, want 2 and none", res.Clients.Created, res.Clients.Skipped)
	}
	if res.HostsCreated != 2 {
		t.Fatalf("hosts created=%d (errors %v), want the two Germany hosts", res.HostsCreated, res.HostErrors)
	}

	links, _, _, _, err := NewSubService("").GetSubs("Zx3pQ9tLmN2kVb7c", "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %d (%v), want the Germany Reality host and the Trojan host", len(links), links)
	}
	var vless, trojan *url.URL
	for _, l := range links {
		u, perr := url.Parse(l)
		if perr != nil {
			t.Fatalf("parse %q: %v", l, perr)
		}
		switch u.Scheme {
		case "vless":
			vless = u
		case "trojan":
			trojan = u
		}
	}
	if vless == nil || trojan == nil {
		t.Fatalf("links %v: want one vless and one trojan", links)
	}
	if got := vless.User.Username() + "@" + vless.Host; got != "5f0a1c2e-3b4d-4e5f-8a9b-0c1d2e3f4a5b@de.example.com:443" {
		t.Errorf("vless endpoint = %s", got)
	}
	q := vless.Query()
	for k, want := range map[string]string{"pbk": rwRealityPublic, "sid": "6ba85179e30d4fc2", "sni": "www.microsoft.com", "flow": "xtls-rprx-vision", "security": "reality"} {
		if q.Get(k) != want {
			t.Errorf("vless %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if got := trojan.User.Username() + "@" + trojan.Host; got != "tr0janPassw0rdAlice@de.example.com:8443" {
		t.Errorf("trojan endpoint = %s", got)
	}

	var alice model.ClientRecord
	if err := database.GetDB().Where("email = ?", "alice").First(&alice).Error; err != nil {
		t.Fatalf("alice: %v", err)
	}
	if alice.TotalGB != 107374182400 || alice.ExpiryTime != 1801396800000 || alice.TgID != 123456789 ||
		alice.LimitHwid != 3 || alice.TrafficReset != "monthly" || alice.TrafficResetDay != 1 || !alice.Enable {
		t.Errorf("alice = %+v", alice)
	}
	var stat xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", "alice").First(&stat).Error; err != nil {
		t.Fatalf("alice traffic: %v", err)
	}
	if stat.Up+stat.Down != 5368709120 {
		t.Errorf("alice used traffic = %d, want 5368709120", stat.Up+stat.Down)
	}
	var bob model.ClientRecord
	if err := database.GetDB().Where("email = ?", "bob").First(&bob).Error; err != nil {
		t.Fatalf("bob: %v", err)
	}
	if bob.Enable || bob.SubID != "Qw8eRt5yUi1oPa4s" {
		t.Errorf("bob enable=%v subId=%q, want disabled with his short uuid", bob.Enable, bob.SubID)
	}

	settings := service.SettingService{}
	if p, _ := settings.GetSubPath(); p != "/" {
		t.Errorf("subPath = %q, want / to keep https://sub.example.com/<shortUuid>", p)
	}
	if u, _ := settings.GetSubURI(); u != "https://sub.example.com/" {
		t.Errorf("subURI = %q", u)
	}
	tmpl, _ := settings.GetRemarkTemplate()
	named, _, _, _, err := NewSubService(tmpl).GetSubs("Zx3pQ9tLmN2kVb7c", "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs with the applied remark template: %v", err)
	}
	for _, l := range named {
		if u, _ := url.Parse(l); u.Fragment != "🇩🇪 Germany" && u.Fragment != "Trojan DE" {
			t.Errorf("config name %q, want the Remnawave host remark", u.Fragment)
		}
	}
}

// The preview pairs each Remnawave node with the panel node at its address and
// flags a port this panel already serves.
func TestRemnawavePreview_SuggestsNodesAndFlagsConflicts(t *testing.T) {
	seedSubDB(t)
	db := database.GetDB()
	node := &model.Node{Name: "nl", Address: "185.0.0.2", Port: 2053, ApiToken: "x", Enable: true}
	if err := db.Create(node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	seedSubInbound(t, "other", "taken", 443, 1, `{"network":"tcp","security":"none"}`)

	p, err := (&service.MigrationService{}).PreviewRemnawave(loadRemnawaveSnapshot(t), map[string]string{rwGermany: service.MigrationTargetLocal})
	if err != nil {
		t.Fatalf("PreviewRemnawave: %v", err)
	}
	suggested := map[string]string{}
	for _, s := range p.Servers {
		suggested[s.Key] = s.Suggested
	}
	if suggested[rwNetherlands] != "node:"+strconv.Itoa(node.Id) {
		t.Errorf("Netherlands suggested %q, want node:%d", suggested[rwNetherlands], node.Id)
	}
	conflicts := map[string]bool{}
	for _, in := range p.Inbounds {
		conflicts[in.ServerKey+"/"+in.Protocol] = in.Conflict != ""
	}
	if !conflicts[rwGermany+"/vless"] {
		t.Errorf("local vless on 443 not flagged: %+v", p.Inbounds)
	}
	if conflicts[rwNetherlands+"/vless"] || conflicts[rwGermany+"/trojan"] {
		t.Errorf("unexpected conflicts: %+v", p.Inbounds)
	}
	if p.Clients.Total != 2 || p.Sub.Domain != "sub.example.com" || p.Sub.Path != "/" {
		t.Errorf("clients=%+v sub=%+v", p.Clients, p.Sub)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "routing") {
		t.Errorf("warnings %v: want the not-imported routing notice", p.Warnings)
	}
}

// Remnawave lists only enabled hosts in a subscription, so an imported inbound
// whose hosts are missing or all disabled must not add a link of its own.
func TestRemnawaveImport_InboundWithoutHostStaysOutOfSub(t *testing.T) {
	cases := map[string]func(*service.RemnawaveSnapshot){
		"no host":       func(s *service.RemnawaveSnapshot) { s.Hosts = s.Hosts[:2] },
		"host disabled": func(s *service.RemnawaveSnapshot) { s.Hosts[2].IsDisabled = true },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			seedSubDB(t)
			snap := loadRemnawaveSnapshot(t)
			edit(snap)
			targets := map[string]string{rwGermany: service.MigrationTargetLocal, rwNetherlands: service.MigrationTargetSkip}
			if _, err := (&service.MigrationService{}).ApplyRemnawave(snap, targets, false, 1); err != nil {
				t.Fatalf("ApplyRemnawave: %v", err)
			}
			links, _, _, _, err := NewSubService("").GetSubs("Zx3pQ9tLmN2kVb7c", "sub.example.com")
			if err != nil {
				t.Fatalf("GetSubs: %v", err)
			}
			if len(links) != 1 || !strings.HasPrefix(links[0], "vless://") {
				t.Fatalf("links = %v, want only the Germany Reality host", links)
			}
		})
	}
}
