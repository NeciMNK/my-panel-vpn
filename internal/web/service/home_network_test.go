package service

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/homenet"
)

const homeWG = "[Interface]\nPrivateKey = cPriv=\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = hPub=\nEndpoint = home.example.net:51820\n"

func homeFixture() *model.HomeNetwork {
	return &model.HomeNetwork{
		Id: 4, Kind: "wireguard", GatewayNodeId: 2, Config: homeWG,
		Subnets: []string{"192.168.1.0/24"}, Hosts: []string{"10.0.0.5"},
		Link: &model.CascadeLink{NodeId: 2, Address: "gw.example.net", Port: 31000, ClientId: "id"},
	}
}

func ruleByTag(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	for _, r := range templateRouting(cfg)["rules"].([]any) {
		if m := r.(map[string]any); m["ruleTag"] == tag {
			raw, _ := json.Marshal(m)
			out := map[string]any{}
			_ = json.Unmarshal(raw, &out)
			return out
		}
	}
	t.Fatalf("no rule %s in %v", tag, ruleTags(cfg))
	return nil
}

func TestHomeOnAnEntryServerAllowsListedUsersAndBlocksTheRest(t *testing.T) {
	cfg := baseTemplate(t)
	rules := []resolvedHomeRule{
		{Emails: []string{"anna", "bob"}, Targets: []string{"192.168.1.0/24", "10.0.0.5"}},
		{Emails: []string{"carl"}, Targets: []string{"10.0.0.5"}},
		{Emails: nil, Targets: []string{"10.0.0.5"}},
	}
	if err := applyHomeToTemplate(cfg, homeFixture(), 7, rules); err != nil {
		t.Fatal(err)
	}
	want := []string{"->api", "mp-home-4-allow-0", "mp-home-4-allow-1", "mp-home-4-deny", "->blocked"}
	if got := ruleTags(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	carl := ruleByTag(t, cfg, "mp-home-4-allow-1")
	if !reflect.DeepEqual(carl["user"], []any{"carl"}) || !reflect.DeepEqual(carl["ip"], []any{"10.0.0.5"}) || carl["outboundTag"] != "mp-home-4-via" {
		t.Fatalf("carl's rule = %v", carl)
	}
	deny := ruleByTag(t, cfg, "mp-home-4-deny")
	if !reflect.DeepEqual(deny["ip"], []any{"192.168.1.0/24", "10.0.0.5"}) || deny["outboundTag"] != "mp-home-4-block" {
		t.Fatalf("deny rule = %v", deny)
	}
	obs := cfg["outbounds"].([]any)
	if tagOf(obs[len(obs)-1], "tag") != "mp-home-4-via" || tagOf(obs[len(obs)-2], "tag") != "mp-home-4-block" {
		t.Fatalf("outbounds = %v", obs)
	}
}

func TestHomeOnTheGatewayHoldsTheTunnelAndServesOnlyTheLink(t *testing.T) {
	cfg := baseTemplate(t)
	before, _ := json.Marshal(cfg)
	rules := []resolvedHomeRule{{Emails: []string{"anna"}, Targets: []string{"192.168.1.0/24"}}}
	if err := applyHomeToTemplate(cfg, homeFixture(), 2, rules); err != nil {
		t.Fatal(err)
	}
	want := []string{"->api", "mp-home-4-gw", "mp-home-4-gw-deny", "mp-home-4-allow-0", "mp-home-4-deny", "->blocked"}
	if got := ruleTags(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	gw := ruleByTag(t, cfg, "mp-home-4-gw")
	if !reflect.DeepEqual(gw["inboundTag"], []any{"mp-home-4-in-n2"}) || gw["outboundTag"] != "mp-home-4-tunnel" {
		t.Fatalf("gateway rule = %v", gw)
	}
	if ruleByTag(t, cfg, "mp-home-4-allow-0")["outboundTag"] != "mp-home-4-tunnel" {
		t.Fatal("users on the gateway itself must go straight into the tunnel")
	}
	var tunnel map[string]any
	for _, o := range cfg["outbounds"].([]any) {
		if tagOf(o, "tag") == "mp-home-4-tunnel" {
			tunnel = o.(map[string]any)
		}
	}
	if tunnel == nil || tunnel["protocol"] != "wireguard" {
		t.Fatalf("tunnel outbound = %v", tunnel)
	}

	raw, _ := json.Marshal(cfg)
	again := map[string]any{}
	_ = json.Unmarshal(raw, &again)
	stripManaged(again, homePrefix(4))
	if after, _ := json.Marshal(again); string(after) != string(before) {
		t.Fatalf("strip left traces:\n before %s\n after  %s", before, after)
	}
}

func TestHomeOpenVPNGatewayBindsToTheTunnelInterface(t *testing.T) {
	cfg := baseTemplate(t)
	h := homeFixture()
	h.Kind, h.Link = "openvpn", nil
	if err := applyHomeToTemplate(cfg, h, 2, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg["outbounds"].([]any)[3])
	if string(raw) != `{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["192.168.1.0/24","10.0.0.5/32"]}]},"streamSettings":{"sockopt":{"interface":"mph4"}},"tag":"mp-home-4-tunnel"}` {
		t.Fatalf("tunnel outbound = %s", raw)
	}
	if got := ruleTags(cfg); !reflect.DeepEqual(got, []string{"->api", "mp-home-4-deny", "->blocked"}) {
		t.Fatalf("without a link the gateway has no link rules: %v", got)
	}
}

func TestHomeEntryWithoutALinkFails(t *testing.T) {
	h := homeFixture()
	h.Link = nil
	if err := applyHomeToTemplate(baseTemplate(t), h, 7, nil); err == nil || err.Error() != "no link to the gateway server yet" {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveHomeRulesExpandsGroups(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	for _, c := range []model.ClientRecord{
		{Email: "kid1", Group: "family"}, {Email: "kid2", Group: "family"}, {Email: "guest", Group: "guests"},
	} {
		if err := database.GetDB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := homeFixture()
	h.Rules = []model.HomeAccessRule{
		{Emails: []string{"anna", "kid1"}, Groups: []string{"family"}},
		{Groups: []string{"guests"}, Targets: []string{"10.0.0.5"}},
	}
	got, err := resolveHomeRules(h)
	if err != nil {
		t.Fatal(err)
	}
	want := []resolvedHomeRule{
		{Emails: []string{"anna", "kid1", "kid2"}, Targets: []string{"192.168.1.0/24", "10.0.0.5"}},
		{Emails: []string{"guest"}, Targets: []string{"10.0.0.5"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestHomeNetworkValidation(t *testing.T) {
	base := func() *HomeNetworkRequest {
		return &HomeNetworkRequest{Kind: "wireguard", Config: homeWG, Subnets: []string{"192.168.1.7/24"}}
	}
	req := base()
	if err := (&HomeNetworkService{}).validate(req); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req.Subnets, []string{"192.168.1.0/24"}) {
		t.Fatalf("subnets not normalized: %v", req.Subnets)
	}
	cases := map[string]func(r *HomeNetworkRequest){
		"connection type must be wireguard, openvpn, l2tp or ikev2":    func(r *HomeNetworkRequest) { r.Kind = "pptp" },
		"add at least one subnet or IP of the home network":            func(r *HomeNetworkRequest) { r.Subnets = nil },
		"rule 1: 10.9.9.9 is not one of this network's subnets or IPs": func(r *HomeNetworkRequest) { r.Rules = []model.HomeAccessRule{{Targets: []string{"10.9.9.9"}}} },
		"wireguard config: exactly one [Peer] is supported":            func(r *HomeNetworkRequest) { r.Config = "[Interface]\nPrivateKey = a\n" },
		"openvpn config has no remote line":                            func(r *HomeNetworkRequest) { r.Kind, r.Config = "openvpn", "client\n" },
		"password must not contain quotes, backslashes or line breaks": func(r *HomeNetworkRequest) { r.Kind, r.Server, r.Password = "l2tp", "1.2.3.4", `a"b` },
		"ikev2: login with a username needs the router's CA certificate": func(r *HomeNetworkRequest) {
			r.Kind, r.Config, r.Server, r.Username, r.Password = "ikev2", "", "1.2.3.4", "anna", "pw"
		},
	}
	for want, mutate := range cases {
		r := base()
		mutate(r)
		if err := (&HomeNetworkService{}).validate(r); err == nil || strings.TrimSpace(err.Error()) != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func localRuleTags(t *testing.T) []string {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tag := range ruleTags(tmpl.Config) {
		if strings.HasPrefix(tag, "mp-") {
			out = append(out, tag)
		}
	}
	return out
}

func TestHomeNetworkCreateAndDeleteOnASinglePanel(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	if err := (&XraySettingService{}).SaveXraySetting(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`); err != nil {
		t.Fatal(err)
	}
	validWG := "[Interface]\nPrivateKey = aPzA0NGtDTXv3n7bIrjd6GWkB0eQHtJqYQyUqGa2Qmk=\nAddress = 10.8.0.2/32\n\n" +
		"[Peer]\nPublicKey = 2fGuuGwOBXmFMjmqb1xjHOZIXBQ4mkpUWrdXkKxVCA0=\nEndpoint = home.example.net:51820\n"
	svc := &HomeNetworkService{}
	h, err := svc.Create(t.Context(), &HomeNetworkRequest{
		Enable: true, Kind: "wireguard", Config: validWG, Subnets: []string{"192.168.1.0/24"},
		Rules: []model.HomeAccessRule{{Emails: []string{"anna"}}},
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if h.Link != nil || !reflect.DeepEqual(h.Servers, []int{0}) {
		t.Fatalf("a lone panel needs no link: link=%v servers=%v", h.Link, h.Servers)
	}
	want := []string{"mp-home-1-allow-0", "mp-home-1-deny"}
	if got := localRuleTags(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("template rules = %v, want %v", got, want)
	}
	if err := svc.Delete(t.Context(), h.Id); err != nil {
		t.Fatal(err)
	}
	if got := localRuleTags(t); len(got) != 0 {
		t.Fatalf("delete left rules: %v", got)
	}
}

const homeRouterCA = `-----BEGIN CERTIFICATE-----
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

// The gateway renders the IKEv2 client from what the master sends it; without
// the router's CA there it cannot verify the router and refuses to start.
func TestHomeIKEv2TunnelCarriesTheRouterCA(t *testing.T) {
	h := &model.HomeNetwork{
		Id: 3, Kind: "ikev2", Config: homeRouterCA, Server: "home.example.net",
		Username: "anna", Password: "pw", Subnets: []string{"192.168.1.0/24"},
	}
	tun := homeTunnel(h, true)
	files, err := homenet.IKEv2Files(tun, "/d")
	if err != nil {
		t.Fatal(err)
	}
	if files["/d/ca.pem"] != homeRouterCA || tun.OpenVPN != "" {
		t.Fatalf("tunnel = %+v", tun)
	}
}
