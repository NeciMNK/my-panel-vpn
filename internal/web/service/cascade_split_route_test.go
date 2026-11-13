package service

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xtls/xray-core/app/router"
	xraygeodata "github.com/xtls/xray-core/common/geodata"
	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/dns"
	routingsession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// fakeResolver answers the router's on-demand lookups from a fixed table.
type fakeResolver map[string]string

func (fakeResolver) Type() any    { return dns.ClientType() }
func (fakeResolver) Start() error { return nil }
func (fakeResolver) Close() error { return nil }
func (r fakeResolver) LookupIP(domain string, _ dns.IPOption) ([]xraynet.IP, uint32, error) {
	ip, ok := r[domain]
	if !ok {
		return nil, 0, dns.ErrEmptyResponse
	}
	return []xraynet.IP{netip.MustParseAddr(ip).AsSlice()}, 600, nil
}

func writeRouteGeodata(t *testing.T, dir string) {
	t.Helper()
	cidr := func(p string) *xraygeodata.CIDR {
		pfx := netip.MustParsePrefix(p)
		return &xraygeodata.CIDR{Ip: pfx.Addr().AsSlice(), Prefix: uint32(pfx.Bits())}
	}
	ips, err := proto.Marshal(&xraygeodata.GeoIPList{Entry: []*xraygeodata.GeoIP{
		{Code: "RU", Cidr: []*xraygeodata.CIDR{cidr("178.248.232.0/21")}},
		{Code: "PRIVATE", Cidr: []*xraygeodata.CIDR{cidr("10.0.0.0/8")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sites, err := proto.Marshal(&xraygeodata.GeoSiteList{Entry: []*xraygeodata.GeoSite{
		{Code: "CATEGORY-RU", Domain: []*xraygeodata.Domain{{Type: xraygeodata.Domain_Domain, Value: "yandex.ru"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"geoip.dat": ips, "geosite.dat": sites} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Bug 23: the entry decided "Russian or not" by the domain's zone, so 2ip.ru
// (hosted in Germany) went direct and habr.com (hosted in Russia) went abroad.
func TestCascadeSplitRoutesByServerCountry(t *testing.T) {
	dir := t.TempDir()
	writeRouteGeodata(t, dir)
	t.Setenv("xray.location.asset", dir)

	cfg := baseTemplate(t)
	c := &model.Cascade{Id: 4, Mode: CascadeModeSplit, Strategy: "leastPing", Links: []model.CascadeLink{{NodeId: 2}}}
	applyCascadeToTemplate(cfg, c, nil, aiExitPlan{}, splitZone("RU"))
	raw, err := json.Marshal(templateRouting(cfg))
	if err != nil {
		t.Fatal(err)
	}
	rc := new(conf.RouterConfig)
	if err := json.Unmarshal(raw, rc); err != nil {
		t.Fatal(err)
	}
	built, err := rc.Build()
	if err != nil {
		t.Fatal(err)
	}
	r := new(router.Router)
	resolver := fakeResolver{
		"2ip.ru":    "188.40.167.82",
		"habr.com":  "178.248.237.68",
		"yandex.ru": "77.88.55.88",
	}
	if err := r.Init(context.Background(), built, resolver, nil, nil); err != nil {
		t.Fatal(err)
	}

	direct, exit := cascadeDirectTag(4), cascadeLinkTag(4, 2)
	cases := []struct{ host, want string }{
		{"2ip.ru", exit},
		{"habr.com", direct},
		{"yandex.ru", direct},
		{"unresolvable.example", exit},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
				Target: xraynet.TCPDestination(xraynet.DomainAddress(tc.host), 443),
			}})
			route, err := r.PickRoute(routingsession.AsRoutingContext(ctx))
			if err != nil {
				t.Fatalf("no route for %s: %v", tc.host, err)
			}
			if got := route.GetOutboundTag(); got != tc.want {
				t.Fatalf("%s went to %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// A single-exit split cascade saved before bug 23 kept its .ru zone rule after
// an upgrade: only multi-exit cascades were rewritten on the first sticky pass.
func TestStickyPassRewritesSingleExitEntriesOnceAfterRestart(t *testing.T) {
	initJoinDB(t)
	seedFixedExitNodes(t)
	saved := appliedAIRules
	appliedAIRules = map[int]string{}
	t.Cleanup(func() { appliedAIRules = saved })

	c := &model.Cascade{
		Name: "ru-de", Enable: true, EntryNodeId: 0, ExitNodeIds: []int{2}, Strategy: "leastPing",
		Mode: CascadeModeSplit, Links: []model.CascadeLink{fixedTestLink(t, 2)},
	}
	if err := database.GetDB().Create(c).Error; err != nil {
		t.Fatal(err)
	}
	stale := baseTemplate(t)
	applyCascadeToTemplate(stale, c, nil, aiExitPlan{}, cascadeZone{Domains: []string{"domain:ru"}, IPs: []string{"geoip:ru"}})
	delete(templateRouting(stale), "domainStrategy")
	if err := (&XraySettingService{}).SaveXraySetting(string(mustJSON(t, stale))); err != nil {
		t.Fatal(err)
	}

	(&CascadeService{}).StickCascadeUsers(context.Background())

	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	routing := templateRouting(tmpl.Config)
	if routing["domainStrategy"] != splitDomainStrategy {
		t.Fatalf("domainStrategy = %v, want %s", routing["domainStrategy"], splitDomainStrategy)
	}
	for _, r := range cascadeRules(t, tmpl.Config, c.Id) {
		if slices.Contains(strs(r["domain"]), "domain:ru") {
			t.Fatalf("stale .ru zone rule survived the restart: %v", r)
		}
	}
}
