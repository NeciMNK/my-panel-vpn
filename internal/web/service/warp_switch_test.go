package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const warpConfigJSON = `{"config":{"client_id":"AQID","interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700::2"}},` +
	`"peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=","endpoint":{"host":"engage.cloudflareclient.com:2408"}}]}}`

type fakeWarpAccount struct {
	data  string
	calls []string
}

func (f *fakeWarpAccount) GetWarpData() (string, error) {
	f.calls = append(f.calls, "data")
	return f.data, nil
}

func (f *fakeWarpAccount) RegWarp(secretKey, publicKey string) (string, error) {
	f.calls = append(f.calls, "reg")
	f.data = fmt.Sprintf(`{"private_key":%q}`, secretKey)
	return "{}", nil
}

func (f *fakeWarpAccount) GetWarpConfig() (string, error) {
	f.calls = append(f.calls, "config")
	return warpConfigJSON, nil
}

func TestBuildWarpOutboundMatchesThePanelDialog(t *testing.T) {
	out, err := buildWarpOutbound(`{"private_key":"sk="}`, warpConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out["settings"])
	want := `{"address":["172.16.0.2/32","2606:4700::2/128"],"mtu":1420,"noKernelTun":true,` +
		`"peers":[{"endpoint":"engage.cloudflareclient.com:2408","publicKey":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="}],` +
		`"reserved":[1,2,3],"secretKey":"sk="}`
	if string(raw) != want {
		t.Fatalf("settings =\n%s\nwant\n%s", raw, want)
	}
	if _, err := buildWarpOutbound(`{"private_key":""}`, warpConfigJSON); err == nil || err.Error() != "WARP returned no peer or key" {
		t.Fatalf("err = %v", err)
	}
}

func TestWarpRuleStaysLastWhenCascadesAreAppliedAfterIt(t *testing.T) {
	cfg := baseTemplate(t)
	applyWarpRule(cfg, &model.WarpSwitch{Mode: WarpAll})
	applyCascadeToTemplate(cfg, &model.Cascade{Id: 2, Links: []model.CascadeLink{{NodeId: 3}}}, 0, nil, aiExitPlan{}, cascadeZone{})
	want := []string{"->api", "->blocked", "mp-cascade-2-route", "mp-warp-all"}
	if got := ruleTags(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
}

func localTemplate(t *testing.T) map[string]any {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	return tmpl.Config
}

func TestWarpSwitchOnThisPanel(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	if err := (&XraySettingService{}).SaveXraySetting(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`); err != nil {
		t.Fatal(err)
	}
	svc := &WarpSwitchService{}
	acct := &fakeWarpAccount{}

	if _, err := svc.Set(t.Context(), &WarpSwitchRequest{Mode: WarpDomains, Domains: []string{" geosite:openai ", "chatgpt.com"}}, acct); err != nil {
		t.Fatal(err)
	}
	if want := []string{"data", "reg", "data", "config"}; !reflect.DeepEqual(acct.calls, want) {
		t.Fatalf("a server without WARP registers first: calls = %v", acct.calls)
	}
	cfg := localTemplate(t)
	if !hasOutbound(cfg, "warp") {
		t.Fatal("warp outbound was not added")
	}
	rule := ruleByTag(t, cfg, "mp-warp-domains")
	if !reflect.DeepEqual(rule["domain"], []any{"geosite:openai", "chatgpt.com"}) || rule["outboundTag"] != "warp" {
		t.Fatalf("domains rule = %v", rule)
	}

	acct.calls = nil
	if _, err := svc.Set(t.Context(), &WarpSwitchRequest{Mode: WarpAll}, acct); err != nil {
		t.Fatal(err)
	}
	if len(acct.calls) != 0 {
		t.Fatalf("an existing warp outbound is reused, got calls %v", acct.calls)
	}
	if got := localRuleTags(t); !reflect.DeepEqual(got, []string{"mp-warp-all"}) {
		t.Fatalf("rules = %v", got)
	}

	row, err := svc.Set(t.Context(), &WarpSwitchRequest{Mode: WarpOff}, acct)
	if err != nil {
		t.Fatal(err)
	}
	cfg = localTemplate(t)
	if len(localRuleTags(t)) != 0 || !hasOutbound(cfg, "warp") {
		t.Fatalf("off drops the rule and keeps the account's outbound: rules %v", localRuleTags(t))
	}
	rows, _ := svc.List()
	if len(rows) != 1 || rows[0].Id != row.Id || rows[0].Mode != WarpOff {
		t.Fatalf("one row per server, got %+v", rows)
	}
}

func TestWarpSwitchValidation(t *testing.T) {
	cases := map[string]*WarpSwitchRequest{
		"warp mode must be off, all or domains":      {Mode: "half"},
		"list the sites that should go through WARP": {Mode: WarpDomains, Domains: []string{" "}},
	}
	for want, req := range cases {
		if err := normalizeWarpRequest(req); err == nil || strings.TrimSpace(err.Error()) != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}
