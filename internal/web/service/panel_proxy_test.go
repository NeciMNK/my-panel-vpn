package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/outbound"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func panelProxyRouting(t *testing.T, cfg *xray.Config) (balancers []map[string]any, rules []map[string]any) {
	t.Helper()
	var routing struct {
		Balancers []map[string]any `json:"balancers"`
		Rules     []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatalf("routing: %v", err)
	}
	return routing.Balancers, routing.Rules
}

func outboundTags(t *testing.T, cfg *xray.Config) []string {
	t.Helper()
	var obs []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &obs); err != nil {
		t.Fatalf("outbounds: %v", err)
	}
	tags := make([]string, 0, len(obs))
	for _, ob := range obs {
		tags = append(tags, ob.Tag)
	}
	return tags
}

// The panel's own traffic must reach a balancer over every prefixed exit (the
// subscription's and the generated ones); the panel pins it, so it needs no observatory.
func TestInjectPanelProxyRoutesPanelTrafficThroughBalancer(t *testing.T) {
	cfg := egressTestConfig()
	cfg.OutboundConfigs = json_util.RawMessage(`[{"tag":"direct","protocol":"freedom"},{"tag":"panelproxy-de","protocol":"freedom"}]`)
	extra := []any{map[string]any{"tag": "panelproxy-de", "protocol": "freedom"}}

	if !injectPanelProxy(cfg, extra) {
		t.Fatal("injectPanelProxy reported no balancer")
	}
	injectPanelEgress(cfg, PanelProxyBalancerTag)

	if got, want := outboundTags(t, cfg), []string{"direct", "panelproxy-de", "panelproxy-de-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outbound tags = %v, want %v", got, want)
	}
	balancers, rules := panelProxyRouting(t, cfg)
	var lb map[string]any
	for _, b := range balancers {
		if b["tag"] == PanelProxyBalancerTag {
			lb = b
		}
	}
	if lb == nil {
		t.Fatalf("balancers = %v, want %s", balancers, PanelProxyBalancerTag)
	}
	if !reflect.DeepEqual(lb["selector"], []any{PanelProxyTagPrefix}) {
		t.Fatalf("balancer = %v", lb)
	}
	if lb["strategy"].(map[string]any)["type"] != "random" {
		t.Fatalf("strategy = %v, want random", lb["strategy"])
	}
	if rules[0]["balancerTag"] != PanelProxyBalancerTag || !reflect.DeepEqual(rules[0]["inboundTag"], []any{PanelEgressInboundTag}) {
		t.Fatalf("first rule = %v, want the panel egress bridge routed to the balancer", rules[0])
	}
	if len(cfg.Observatory) != 0 || len(cfg.BurstObservatory) != 0 {
		t.Fatalf("observatory %s burst %s, want none", cfg.Observatory, cfg.BurstObservatory)
	}
}

// Xray runs one observatory with one probe URL, so joining the template's would
// judge the panel's exits by its URL (google.com) instead of Telegram.
func TestInjectPanelProxyLeavesTemplateObservatory(t *testing.T) {
	tests := []struct {
		name        string
		observatory string
		burst       string
	}{
		{"observatory", `{"subjectSelector":["warp"],"probeUrl":"https://www.google.com/generate_204"}`, ""},
		{"burstObservatory", "", `{"subjectSelector":["warp"],"pingConfig":{"destination":"https://www.google.com/generate_204"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := egressTestConfig()
			cfg.Observatory = json_util.RawMessage(tt.observatory)
			cfg.BurstObservatory = json_util.RawMessage(tt.burst)
			if !injectPanelProxy(cfg, []any{map[string]any{"tag": "panelproxy-x", "protocol": "freedom"}}) {
				t.Fatal("injectPanelProxy reported no balancer")
			}
			if string(cfg.Observatory) != tt.observatory || string(cfg.BurstObservatory) != tt.burst {
				t.Fatalf("observatory %s burst %s, want the template's untouched", cfg.Observatory, cfg.BurstObservatory)
			}
			balancers, _ := panelProxyRouting(t, cfg)
			last := balancers[len(balancers)-1]
			if last["strategy"].(map[string]any)["type"] != "random" {
				t.Fatalf("strategy = %v, want random", last["strategy"])
			}
		})
	}
}

func TestPickPanelProxyExit(t *testing.T) {
	ok := func(tag string, ms int64) *outbound.TestOutboundResult {
		return &outbound.TestOutboundResult{Tag: tag, Success: true, Delay: ms}
	}
	down := func(tag string) *outbound.TestOutboundResult {
		return &outbound.TestOutboundResult{Tag: tag, Error: "context deadline exceeded"}
	}
	tests := []struct {
		name       string
		current    string
		results    []*outbound.TestOutboundResult
		wantTarget string
		wantChange bool
	}{
		{"first pin takes the fastest exit reaching Telegram", "", []*outbound.TestOutboundResult{down("panelproxy-ru"), ok("panelproxy-de", 300), ok("panelproxy-nl", 120)}, "panelproxy-nl", true},
		{"a pin that still reaches Telegram is kept", "panelproxy-de", []*outbound.TestOutboundResult{ok("panelproxy-de", 300), ok("panelproxy-nl", 120)}, "panelproxy-de", false},
		{"a pin that lost Telegram moves to the fastest", "panelproxy-ru", []*outbound.TestOutboundResult{down("panelproxy-ru"), ok("panelproxy-de", 300), ok("panelproxy-nl", 120)}, "panelproxy-nl", true},
		{"a pin whose exit left the config moves", "panelproxy-gone", []*outbound.TestOutboundResult{ok("panelproxy-de", 300)}, "panelproxy-de", true},
		{"no exit reaching Telegram keeps the pin", "panelproxy-de", []*outbound.TestOutboundResult{down("panelproxy-de"), down("panelproxy-nl")}, "panelproxy-de", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, change := pickPanelProxyExit(tt.current, tt.results)
			if target != tt.wantTarget || change != tt.wantChange {
				t.Fatalf("pickPanelProxyExit = (%q, %v), want (%q, %v)", target, change, tt.wantTarget, tt.wantChange)
			}
		})
	}
}

// With nothing to route through the panel must keep its direct path: a balancer
// over zero outbounds would make the egress rule unroutable.
func TestInjectPanelProxyWithoutExitsLeavesConfig(t *testing.T) {
	cfg := egressTestConfig()
	before := *cfg
	if injectPanelProxy(cfg, nil) {
		t.Fatal("injectPanelProxy reported a balancer without any panelproxy outbound")
	}
	if string(cfg.OutboundConfigs) != string(before.OutboundConfigs) || string(cfg.RouterConfig) != string(before.RouterConfig) || len(cfg.Observatory) != 0 {
		t.Fatalf("config changed: outbounds %s routing %s observatory %s", cfg.OutboundConfigs, cfg.RouterConfig, cfg.Observatory)
	}
}

func TestPanelProxyLinkOutbound(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"socks with credentials", "socks5://bob:s3cret@198.51.100.4:1080",
			`{"protocol":"socks","settings":{"servers":[{"address":"198.51.100.4","port":1080,"users":[{"pass":"s3cret","user":"bob"}]}]},"tag":"198.51.100.4"}`,
		},
		{
			"https proxy dials TLS", "https://proxy.example:8443",
			`{"protocol":"http","settings":{"servers":[{"address":"proxy.example","port":8443}]},"streamSettings":{"security":"tls","tlsSettings":{"serverName":"proxy.example"}},"tag":"proxy.example"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ob, err := panelProxyLinkOutbound(tt.raw)
			if err != nil {
				t.Fatalf("panelProxyLinkOutbound: %v", err)
			}
			got, _ := json.Marshal(ob)
			if string(got) != tt.want {
				t.Fatalf("outbound = %s\nwant       %s", got, tt.want)
			}
			if err := xray.ValidateOutboundConfig(got); err != nil {
				t.Fatalf("xray-core refuses the outbound: %v", err)
			}
		})
	}

	for _, bad := range []string{"socks5://198.51.100.4", "198.51.100.4:1080", "ftp://198.51.100.4:21"} {
		if _, err := panelProxyLinkOutbound(bad); err == nil {
			t.Errorf("panelProxyLinkOutbound(%q) accepted an unusable source", bad)
		}
	}
}

// Leaving subscription mode must drop the subscription it created, or its
// outbounds keep being merged and refreshed for nothing.
func TestPanelProxySetLinkModeDropsOwnSubscription(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	own := &model.OutboundSubscription{Remark: "Panel proxy", Url: "https://sub.example/a", TagPrefix: PanelProxyTagPrefix, Enabled: true}
	other := &model.OutboundSubscription{Remark: "mine", Url: "https://sub.example/b", TagPrefix: "sub1-", Enabled: true}
	for _, sub := range []*model.OutboundSubscription{own, other} {
		if err := db.Create(sub).Error; err != nil {
			t.Fatalf("seed subscription: %v", err)
		}
	}

	svc := &PanelProxyService{}
	if err := svc.Set(PanelProxyModeLink, " socks5://198.51.100.4:1080 "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var left []model.OutboundSubscription
	if err := db.Order("id").Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Id != other.Id {
		t.Fatalf("subscriptions left = %+v, want only the admin's own", left)
	}
	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := &PanelProxyStatus{Mode: PanelProxyModeLink, Source: "socks5://198.51.100.4:1080", Active: true, Outbounds: []string{"panelproxy-198-51-100-4"}}
	if !reflect.DeepEqual(status, want) {
		t.Fatalf("status = %+v, want %+v", status, want)
	}

	if err := svc.Set(PanelProxyModeOff, "ignored"); err != nil {
		t.Fatalf("Set off: %v", err)
	}
	if active, extra := svc.generatedOutbounds(); active || extra != nil {
		t.Fatalf("generatedOutbounds after off = %v %v", active, extra)
	}
	if err := svc.Set(PanelProxyModeLink, "not a link"); err == nil {
		t.Fatal("Set stored an unusable link")
	}
	if mode, _, _ := svc.stored(); mode != PanelProxyModeOff {
		t.Fatalf("mode after a refused Set = %q, want it unchanged", mode)
	}
}

// The quick panel proxy used to be ignored by the bot, Discord and the updater
// unless the legacy panelOutbound setting was also non-empty.
func TestPanelEgressProxyURLFollowsQuickPanelProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake core is a shell script")
	}
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	binFolder := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binFolder)
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	if err := os.WriteFile(filepath.Join(binFolder, xray.GetBinaryName()), []byte(fakeXrayCore("26.6.27", true)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&PanelProxyService{}).Set(PanelProxyModeLink, "socks5://198.51.100.4:1080"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if tag, _ := (&SettingService{}).GetPanelOutbound(); tag != "" {
		t.Fatalf("panelOutbound = %q, the test needs it empty", tag)
	}
	startFakeCore(t)

	want := fmt.Sprintf("socks5://127.0.0.1:%d", panelEgressBasePort)
	if got := (&SettingService{}).PanelEgressProxyURL(); got != want {
		t.Fatalf("PanelEgressProxyURL() = %q, want %q", got, want)
	}
}

// With the bridge now always used, the subscription mode's own feed would be
// fetched through its own exits and could never be refreshed once they all die.
func TestPanelProxyOwnSubscriptionIsFetchedDirectly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake core is a shell script")
	}
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	binFolder := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binFolder)
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	if err := os.WriteFile(filepath.Join(binFolder, xray.GetBinaryName()), []byte(fakeXrayCore("26.6.27", true)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&PanelProxyService{}).Set(PanelProxyModeLink, "socks5://198.51.100.4:1080"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	startFakeCore(t)

	for _, tc := range []struct {
		name           string
		viaPanelEgress bool
		wantDirect     bool
	}{
		{name: "panel proxy's own subscription", viaPanelEgress: false, wantDirect: true},
		{name: "admin's subscription", viaPanelEgress: true, wantDirect: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := (&OutboundSubscriptionService{}).subscriptionFetchClient(5*time.Second, false, tc.viaPanelEgress)
			ctx := netsafe.ContextWithAllowPrivate(context.Background(), false)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Do(req)
			if err == nil {
				t.Fatal("fetch of a closed port succeeded")
			}
			// Only the direct, SSRF-guarded dialer refuses the private target itself.
			if direct := strings.Contains(err.Error(), "blocked private"); direct != tc.wantDirect {
				t.Fatalf("direct fetch = %v, want %v (err: %v)", direct, tc.wantDirect, err)
			}
		})
	}
}
