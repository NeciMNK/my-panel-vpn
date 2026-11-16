package service

import (
	"encoding/json"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func baseTemplate(t *testing.T) map[string]any {
	t.Helper()
	cfg := map[string]any{}
	raw := `{
	  "outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "blocked", "protocol": "blackhole"}],
	  "routing": {"rules": [
	    {"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
	    {"type": "field", "ip": ["geoip:private"], "outboundTag": "blocked"}
	  ]}
	}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func ruleTags(cfg map[string]any) []string {
	var out []string
	for _, r := range templateRouting(cfg)["rules"].([]any) {
		m := r.(map[string]any)
		switch {
		case m["ruleTag"] != nil:
			out = append(out, m["ruleTag"].(string))
		case m["outboundTag"] != nil:
			out = append(out, "->"+m["outboundTag"].(string))
		}
	}
	return out
}

func TestCascadeSingleExitIsATailRuleToItsOutbound(t *testing.T) {
	cfg := baseTemplate(t)
	c := &model.Cascade{Id: 3, Strategy: "leastPing", Links: []model.CascadeLink{
		{NodeId: 2, Address: "de.example.com", Port: 31000, ClientId: "uuid-2", PublicKey: "pub", ShortId: "ab12", ServerName: "www.apple.com"},
	}}
	applyCascadeToTemplate(cfg, c, 0, nil, aiExitPlan{}, cascadeZone{})

	tags := ruleTags(cfg)
	want := []string{"->api", "->blocked", "mp-cascade-3-route"}
	if len(tags) != len(want) || tags[2] != want[2] || tags[0] != want[0] {
		t.Fatalf("rules = %v, want %v", tags, want)
	}
	rule := templateRouting(cfg)["rules"].([]any)[2].(map[string]any)
	if rule["outboundTag"] != "mp-cascade-3-n2" || rule["inboundTag"] != nil {
		t.Fatalf("cascade rule = %v", rule)
	}
	obs := cfg["outbounds"].([]any)
	if len(obs) != 3 || tagOf(obs[2], "tag") != "mp-cascade-3-n2" {
		t.Fatalf("outbounds = %v", obs)
	}
	if _, ok := templateRouting(cfg)["balancers"]; ok {
		t.Fatal("single exit must not create a balancer")
	}
}

func TestCascadeSeveralExitsBalanceAndStripCleanly(t *testing.T) {
	cfg := baseTemplate(t)
	before, _ := json.Marshal(cfg)
	c := &model.Cascade{Id: 7, Strategy: "leastPing", EntryInboundTags: []string{"inbound-443"}, Links: []model.CascadeLink{
		{NodeId: 2, Address: "a", Port: 1}, {NodeId: 5, Address: "b", Port: 2},
	}}
	applyCascadeToTemplate(cfg, c, 0, nil, aiExitPlan{}, cascadeZone{})

	rule := templateRouting(cfg)["rules"].([]any)[2].(map[string]any)
	if rule["balancerTag"] != "mp-cascade-7-route" {
		t.Fatalf("rule = %v", rule)
	}
	if tags, _ := rule["inboundTag"].([]string); len(tags) != 1 || tags[0] != "inbound-443" {
		t.Fatalf("rule must chain only the picked inbound: %v", rule["inboundTag"])
	}
	if sel := cfg["observatory"].(map[string]any)["subjectSelector"].([]any); len(sel) != 1 || sel[0] != "mp-cascade-7-n" {
		t.Fatalf("observatory = %v", cfg["observatory"])
	}

	// Round-trip through JSON like a real save, then strip: the template is unchanged.
	raw, _ := json.Marshal(cfg)
	again := map[string]any{}
	_ = json.Unmarshal(raw, &again)
	stripManaged(again, cascadePrefix(7))
	after, _ := json.Marshal(again)
	if string(after) != string(before) {
		t.Fatalf("strip left traces:\n before %s\n after  %s", before, after)
	}
}

func TestStripManagedKeepsOtherCascades(t *testing.T) {
	cfg := baseTemplate(t)
	applyCascadeToTemplate(cfg, &model.Cascade{Id: 1, Links: []model.CascadeLink{{NodeId: 2}}}, 0, nil, aiExitPlan{}, cascadeZone{})
	applyCascadeToTemplate(cfg, &model.Cascade{Id: 10, Links: []model.CascadeLink{{NodeId: 3}}}, 0, nil, aiExitPlan{}, cascadeZone{})
	stripManaged(cfg, cascadePrefix(1))
	tags := ruleTags(cfg)
	if tags[len(tags)-1] != "mp-cascade-10-route" || len(tags) != 3 {
		t.Fatalf("rules after stripping cascade 1 = %v", tags)
	}
}

func TestInsertHeadRulesGoesRightAfterAPI(t *testing.T) {
	cfg := baseTemplate(t)
	insertHeadRules(cfg, map[string]any{"type": "field", "ruleTag": "mp-home-1"})
	tags := ruleTags(cfg)
	if tags[0] != "->api" || tags[1] != "mp-home-1" || tags[2] != "->blocked" {
		t.Fatalf("rules = %v", tags)
	}
}
