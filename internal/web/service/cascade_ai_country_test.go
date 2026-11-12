package service

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// seedAutoCascade: anna's master inbound enters one leastPing cascade mixing
// DE (2, 3) and NL (4) exits, the "auto" server choice.
func seedAutoCascade(t *testing.T) *model.Cascade {
	t.Helper()
	seedFixedExitNodes(t)
	if err := (&XraySettingService{}).SaveXraySetting(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`); err != nil {
		t.Fatal(err)
	}
	c := &model.Cascade{
		Name: "auto", Enable: true, EntryNodeId: 0, ExitNodeIds: []int{2, 3, 4}, Strategy: "leastPing",
		Links: []model.CascadeLink{fixedTestLink(t, 2), fixedTestLink(t, 3), fixedTestLink(t, 4)},
	}
	for _, r := range []any{
		&model.Inbound{Id: 1, Tag: "in-anna", Protocol: model.VLESS, Port: 443, Enable: true},
		&model.ClientRecord{Id: 1, Email: "anna"},
		&model.ClientInbound{ClientId: 1, InboundId: 1},
		c,
	} {
		if err := database.GetDB().Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// cascadeRuleOutbounds maps each user of c's rules whose tag starts with kind
// ("pin-" or "ai-") to the rule's outbound, and returns the rules' domains.
func cascadeRuleOutbounds(t *testing.T, c *model.Cascade, kind string) (map[string]string, []string) {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	var domains []string
	for _, r := range templateRouting(tmpl.Config)["rules"].([]any) {
		rule := r.(map[string]any)
		tag, _ := rule["ruleTag"].(string)
		if !strings.HasPrefix(tag, cascadePrefix(c.Id)+kind) {
			continue
		}
		var users []string
		if err := json.Unmarshal(mustJSON(t, rule["user"]), &users); err != nil {
			t.Fatal(err)
		}
		for _, u := range users {
			out[u] = rule["outboundTag"].(string)
		}
		if err := json.Unmarshal(mustJSON(t, rule["domain"]), &domains); err != nil {
			t.Fatal(err)
		}
	}
	return out, domains
}

// "AI only in NL" sends only AI services through the NL pin while the rest keeps
// the auto choice or the admin's pin, unless the user chose all traffic there.
func TestAICountryPinRoutesOnlyAIDomains(t *testing.T) {
	initJoinDB(t)
	c := seedAutoCascade(t)
	s := &CascadeService{}
	ctx := context.Background()

	exit, pinned, err := s.SetAICountry(ctx, []string{"anna"}, "nl")
	if err != nil || !pinned || exit.NodeId != 4 {
		t.Fatalf("SetAICountry = %+v %v %v, want NL node 4 pinned", exit, pinned, err)
	}
	if got := s.AICountry([]string{"anna"}); got != "NL" {
		t.Fatalf("AICountry = %q, want NL", got)
	}
	if pins, _ := cascadeRuleOutbounds(t, c, "pin-"); pins["anna"] != "" {
		t.Fatalf("all of anna's traffic is pinned to %q; only AI domains may be", pins["anna"])
	}
	ai, domains := cascadeRuleOutbounds(t, c, "ai-")
	if ai["anna"] != "mp-cascade-1-n4" || !slices.Contains(domains, "domain:openai.com") || !slices.Contains(domains, "domain:claude.ai") {
		t.Fatalf("AI rule = %v %v, want anna's AI domains to n4", ai, domains)
	}

	if err := s.SetFixedExit(ctx, &CascadeFixedExitRequest{Email: "anna", NodeId: 3}); err != nil {
		t.Fatal(err)
	}
	pins, _ := cascadeRuleOutbounds(t, c, "pin-")
	ai, _ = cascadeRuleOutbounds(t, c, "ai-")
	if pins["anna"] != "mp-cascade-1-n3" || ai["anna"] != "mp-cascade-1-n4" {
		t.Fatalf("admin pin DE + AI in NL: pin %q, AI %q; want n3 and n4", pins["anna"], ai["anna"])
	}

	if err := s.SetAIAllTraffic(ctx, []string{"anna"}, true); err != nil {
		t.Fatal(err)
	}
	pins, _ = cascadeRuleOutbounds(t, c, "pin-")
	ai, _ = cascadeRuleOutbounds(t, c, "ai-")
	if pins["anna"] != "mp-cascade-1-n4" || len(ai) != 0 {
		t.Fatalf("all traffic through the AI pin: pin %q, AI rules %v; want n4 and none", pins["anna"], ai)
	}

	if _, _, err := s.SetAICountry(ctx, []string{"anna"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAIAllTraffic(ctx, []string{"anna"}, false); err == nil {
		t.Fatal("the traffic mode was set without an AI country")
	}
	if ai, _ := cascadeRuleOutbounds(t, c, "ai-"); len(ai) != 0 {
		t.Fatalf("AI rules left after clearing the AI country: %v", ai)
	}
}

func TestSetAICountryPinsTheBestServerOfThatCountryOnly(t *testing.T) {
	initJoinDB(t)
	seedAutoCascade(t)
	setNodeLoad(t, 2, 100, 100)
	s := &CascadeService{}
	ctx := context.Background()

	exit, pinned, err := s.SetAICountry(ctx, []string{"anna"}, "DE")
	if err != nil || !pinned || exit.NodeId != 3 {
		t.Fatalf("SetAICountry(DE) = %+v %v %v, want node 3 (node 2 is full)", exit, pinned, err)
	}
	if got := fixedExitMap(t); !reflect.DeepEqual(got, map[string][]int{"anna": {3}}) {
		t.Fatalf("fixed exits = %v, want only DE node 3 (NL is not an AI country)", got)
	}
	if _, _, err := s.SetAICountry(ctx, []string{"anna"}, "FR"); err == nil || err.Error() != "none of your exits is in FR" {
		t.Fatalf("SetAICountry(FR): err = %v", err)
	}
	if got := s.AICountry([]string{"anna"}); got != "DE" {
		t.Fatalf("a refused country replaced the AI country: %q", got)
	}

	setNodeLoad(t, 4, 100, 100)
	exit, pinned, err = s.SetAICountry(ctx, []string{"anna"}, "NL")
	if err != nil || pinned {
		t.Fatalf("SetAICountry(NL) with NL full = %+v %v %v, want no pin", exit, pinned, err)
	}
	if got := s.AICountry([]string{"anna"}); got != "NL" {
		t.Fatalf("AICountry = %q, want NL kept so the pin is made once NL has room", got)
	}
}
