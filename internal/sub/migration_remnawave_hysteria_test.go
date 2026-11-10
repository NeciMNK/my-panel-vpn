package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Shaped like a live Remnawave 2.x Hysteria2 profile: salamander finalmask on
// the inbound, TLS from the node, and the user's vlessUuid as Hysteria auth.
const rwHysteriaSnapshotJSON = `{
 "configProfiles": [{
  "uuid": "a1000000-0000-4000-8000-000000000001", "name": "Hysteria-2",
  "config": {"inbounds": [
   {"tag": "HY2", "port": 8443, "protocol": "hysteria",
    "settings": {"clients": [], "version": 2},
    "streamSettings": {"network": "hysteria", "security": "tls",
     "tlsSettings": {"alpn": ["h3"], "certificates": [CERTS]},
     "hysteriaSettings": {"version": 2},
     "finalmask": {"udp": [{"type": "salamander", "settings": {"password": "mask-pass-1"}}]}}}
  ]},
  "inbounds": [{"uuid": "b1000000-0000-4000-8000-000000000001", "profileUuid": "a1000000-0000-4000-8000-000000000001", "tag": "HY2", "type": "hysteria", "port": 8443}]
 }],
 "internalSquads": [{"uuid": "c1000000-0000-4000-8000-000000000001", "name": "HY",
  "inbounds": [{"uuid": "b1000000-0000-4000-8000-000000000001"}]}],
 "nodes": [{"uuid": "d1000000-0000-4000-8000-000000000001", "name": "NL", "address": "nl.example.com", "isDisabled": false,
  "configProfile": {"activeConfigProfileUuid": "a1000000-0000-4000-8000-000000000001",
   "activeInbounds": [{"uuid": "b1000000-0000-4000-8000-000000000001", "tag": "HY2"}]}}],
 "hosts": [{"uuid": "e1000000-0000-4000-8000-000000000001", "viewPosition": 0, "remark": "Hysteria NL", "address": "nl.example.com", "port": 8443,
  "sni": "panel.example.com", "alpn": "h3", "fingerprint": "chrome", "isDisabled": false, "securityLayer": "DEFAULT", "nodes": [],
  "finalMask": {"udp": [{"type": "salamander", "settings": {"password": "mask-pass-1"}}]},
  "inbound": {"configProfileUuid": "a1000000-0000-4000-8000-000000000001", "configProfileInboundUuid": "b1000000-0000-4000-8000-000000000001"}}],
 "users": [{"id": 1, "shortUuid": "Hy2sub0000000001", "username": "carol", "status": "ACTIVE",
  "trafficLimitBytes": 0, "trafficLimitStrategy": "NO_RESET", "expireAt": "2099-12-31T00:00:00.000Z",
  "trojanPassword": "carolTrojan", "vlessUuid": "7b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d", "ssPassword": "carolSS",
  "createdAt": "2025-03-10T08:00:00.000Z", "subscriptionUrl": "https://sub.example.com/Hy2sub0000000001",
  "activeInternalSquads": [{"uuid": "c1000000-0000-4000-8000-000000000001", "name": "HY"}]}]
}`

const rwHysteriaNode = "d1000000-0000-4000-8000-000000000001"

func loadHysteriaSnapshot(t *testing.T, certs string) *service.RemnawaveSnapshot {
	t.Helper()
	snap, err := service.ParseRemnawaveSnapshot([]byte(strings.Replace(rwHysteriaSnapshotJSON, "CERTS", certs, 1)))
	if err != nil {
		t.Fatalf("ParseRemnawaveSnapshot: %v", err)
	}
	return snap
}

// A Remnawave Hysteria2 user must keep connecting with the same link: the
// vlessUuid as auth, the host's address and TLS name, the salamander password.
func TestRemnawaveImport_HysteriaKeepsUserLink(t *testing.T) {
	seedSubDB(t)
	snap := loadHysteriaSnapshot(t, `{"certificate": ["-----BEGIN CERTIFICATE-----"], "key": ["-----BEGIN PRIVATE KEY-----"]}`)
	res, err := (&service.MigrationService{}).ApplyRemnawave(snap, map[string]string{rwHysteriaNode: service.MigrationTargetLocal}, false, 1)
	if err != nil {
		t.Fatalf("ApplyRemnawave: %v", err)
	}
	if res.InboundsCreated != 1 || res.Clients.Created != 1 || res.HostsCreated != 1 {
		t.Fatalf("result = %+v", res)
	}
	links, _, _, _, err := NewSubService("").GetSubs("Hy2sub0000000001", "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %v, want the one Hysteria host", links)
	}
	u, err := url.Parse(links[0])
	if err != nil {
		t.Fatalf("parse %q: %v", links[0], err)
	}
	if got := u.Scheme + "://" + u.User.Username() + "@" + u.Host; got != "hysteria2://7b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d@nl.example.com:8443" {
		t.Errorf("endpoint = %s", got)
	}
	q := u.Query()
	for k, want := range map[string]string{"sni": "panel.example.com", "alpn": "h3", "obfs": "salamander", "obfs-password": "mask-pass-1"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q (link %s)", k, q.Get(k), want, links[0])
		}
	}

	// Remnawave hosts repeat the inbound's mask; a second salamander layer breaks JSON clients.
	body, _, err := NewSubJsonService("", "", "", "", NewSubService("")).GetJson("Hy2sub0000000001", "sub.example.com", true)
	if err != nil {
		t.Fatalf("GetJson: %v", err)
	}
	if n := strings.Count(body, `"salamander"`); n != 1 {
		t.Errorf("JSON sub has %d salamander masks, want 1:\n%s", n, body)
	}
	clash, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash("Hy2sub0000000001", "sub.example.com")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	if !strings.Contains(clash, "sni: panel.example.com") || !strings.Contains(clash, "client-fingerprint: chrome") {
		t.Errorf("clash proxy lacks the host's TLS name:\n%s", clash)
	}
}

// Remnawave nodes read TLS certificates from their own disk; the preview must
// say which files have to exist on the target server.
func TestRemnawavePreview_WarnsAboutNodeCertificateFiles(t *testing.T) {
	seedSubDB(t)
	snap := loadHysteriaSnapshot(t, `{"certificateFile": "/opt/certs/server.crt", "keyFile": "/opt/certs/server.key"}`)
	p, err := (&service.MigrationService{}).PreviewRemnawave(snap, map[string]string{rwHysteriaNode: service.MigrationTargetLocal})
	if err != nil {
		t.Fatalf("PreviewRemnawave: %v", err)
	}
	if w := strings.Join(p.Warnings, "\n"); !strings.Contains(w, "/opt/certs/server.crt") || !strings.Contains(w, "/opt/certs/server.key") {
		t.Errorf("warnings %v: want both certificate paths named", p.Warnings)
	}
}
