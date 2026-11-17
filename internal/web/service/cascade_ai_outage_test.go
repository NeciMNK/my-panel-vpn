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

func setNodeStatus(t *testing.T, nodeID int, status string) {
	t.Helper()
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", nodeID).Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
}

// outageRules maps each user of c's outage rules to the rule's outbound and
// domains (nil for a rule that blocks all of the user's traffic).
type outageRule struct {
	Outbound string
	Domains  []string
}

func outageRules(t *testing.T, c *model.Cascade) map[string]outageRule {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]outageRule{}
	for _, r := range templateRouting(tmpl.Config)["rules"].([]any) {
		rule := r.(map[string]any)
		tag, _ := rule["ruleTag"].(string)
		if !strings.HasPrefix(tag, cascadePrefix(c.Id)+"outage-") {
			continue
		}
		var users, domains []string
		if err := json.Unmarshal(mustJSON(t, rule["user"]), &users); err != nil {
			t.Fatal(err)
		}
		if d, ok := rule["domain"]; ok {
			if err := json.Unmarshal(mustJSON(t, d), &domains); err != nil {
				t.Fatal(err)
			}
		}
		for _, u := range users {
			out[u] = outageRule{Outbound: rule["outboundTag"].(string), Domains: domains}
		}
	}
	return out
}

func hasBlackhole(t *testing.T, c *model.Cascade) bool {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	outbounds, _ := tmpl.Config["outbounds"].([]any)
	return slices.ContainsFunc(outbounds, func(o any) bool {
		ob, _ := o.(map[string]any)
		return ob["tag"] == cascadeBlockTag(c.Id) && ob["protocol"] == "blackhole"
	})
}

// pinAnnaToDE gives anna an AI country DE pinned to node 2 (node 3 is full).
func pinAnnaToDE(t *testing.T, all bool) (*model.Cascade, *CascadeService, context.Context) {
	t.Helper()
	initJoinDB(t)
	c := seedAutoCascade(t)
	setNodeLoad(t, 3, 100, 90)
	s, ctx := &CascadeService{}, context.Background()
	if exit, pinned, err := s.SetAICountry(ctx, []string{"anna"}, "DE"); err != nil || !pinned || exit.NodeId != 2 {
		t.Fatalf("SetAICountry(DE) = %+v %v %v, want node 2", exit, pinned, err)
	}
	if all {
		if err := s.SetAIAllTraffic(ctx, []string{"anna"}, true); err != nil {
			t.Fatal(err)
		}
	}
	return c, s, ctx
}

// Before: an AI-sites pin kept routing AI to its dead server, so ChatGPT just
// hung; with no choice made the AI services are cut and the rest flows.
func TestDeadAIPinBlocksAIServicesUntilTheServerIsBack(t *testing.T) {
	c, s, ctx := pinAnnaToDE(t, false)

	setNodeStatus(t, 2, "offline")
	s.StickCascadeUsers(ctx)
	if ai, _ := cascadeRuleOutbounds(t, c, "ai-"); ai["anna"] != "" {
		t.Fatalf("AI still routed to %q while its server is down", ai["anna"])
	}
	got := outageRules(t, c)["anna"]
	if got.Outbound != cascadeBlockTag(c.Id) || !reflect.DeepEqual(got.Domains, defaultAIServiceDomains) {
		t.Fatalf("outage rule = %+v, want every AI domain to %s", got, cascadeBlockTag(c.Id))
	}
	if !hasBlackhole(t, c) {
		t.Fatal("the block outbound the rule points at is missing")
	}

	setNodeStatus(t, 2, "online")
	s.StickCascadeUsers(ctx)
	if rules := outageRules(t, c); len(rules) != 0 {
		t.Fatalf("outage rules left after the server came back: %v", rules)
	}
	if ai, _ := cascadeRuleOutbounds(t, c, "ai-"); ai["anna"] != "mp-cascade-1-n2" {
		t.Fatalf("AI after recovery goes to %q, want the pinned n2", ai["anna"])
	}
}

// Before: an all-traffic pin silently moved the user, AI included, to another
// IP; now only the non-AI traffic moves unless the user asks for another IP.
func TestDeadAllTrafficPinStillBlocksAI(t *testing.T) {
	c, s, ctx := pinAnnaToDE(t, true)

	setNodeStatus(t, 2, "offline")
	s.StickCascadeUsers(ctx)
	if got := outageRules(t, c)["anna"]; got.Outbound != cascadeBlockTag(c.Id) || len(got.Domains) == 0 {
		t.Fatalf("outage rule = %+v, want AI domains blocked", got)
	}
	if pins, _ := cascadeRuleOutbounds(t, c, "pin-"); pins["anna"] == "mp-cascade-1-n2" {
		t.Fatal("the rest of anna's traffic is still pinned to the dead server")
	}
}

func TestOutageChoiceOtherIPUsesALiveServerOfTheSameCountry(t *testing.T) {
	for _, all := range []bool{false, true} {
		c, s, ctx := pinAnnaToDE(t, all)
		if err := s.SetAIOutageChoice(ctx, []string{"anna"}, model.AIOutageChoice{Action: AIOutageOther}); err != nil {
			t.Fatal(err)
		}
		setNodeStatus(t, 2, "offline")
		s.StickCascadeUsers(ctx)
		kind := "ai-"
		if all {
			kind = "pin-"
		}
		if got, _ := cascadeRuleOutbounds(t, c, kind); got["anna"] != "mp-cascade-1-n3" {
			t.Fatalf("all=%v: %s rule = %q, want the other DE server n3", all, kind, got["anna"])
		}
		if rules := outageRules(t, c); len(rules) != 0 {
			t.Fatalf("all=%v: AI blocked although another IP was chosen: %v", all, rules)
		}
		outages, err := s.AIPinOutages([]string{"anna"})
		if err != nil || len(outages) != 1 || outages[0].NodeId != 2 || outages[0].SwapNodeId != 3 {
			t.Fatalf("all=%v: AIPinOutages = %+v %v, want node 2 swapped to 3", all, outages, err)
		}
	}
}

func TestOutageChoiceOtherIPFallsBackToTheChosenCountry(t *testing.T) {
	c, s, ctx := pinAnnaToDE(t, false)
	if err := s.SetAIOutageChoice(ctx, []string{"anna"}, model.AIOutageChoice{Action: AIOutageOther}); err != nil {
		t.Fatal(err)
	}
	setNodeStatus(t, 2, "offline")
	setNodeStatus(t, 3, "offline")
	s.StickCascadeUsers(ctx)
	if got := outageRules(t, c)["anna"]; got.Outbound != cascadeBlockTag(c.Id) {
		t.Fatalf("no live DE server and no fallback country: rule = %+v, want AI blocked", got)
	}

	if err := s.SetAIOutageChoice(ctx, []string{"anna"}, model.AIOutageChoice{Action: AIOutageOther, Country: "nl"}); err != nil {
		t.Fatal(err)
	}
	if ai, _ := cascadeRuleOutbounds(t, c, "ai-"); ai["anna"] != "mp-cascade-1-n4" {
		t.Fatalf("AI rule = %q, want the NL fallback n4", ai["anna"])
	}
	if rules := outageRules(t, c); len(rules) != 0 {
		t.Fatalf("AI still blocked after picking NL: %v", rules)
	}
}

func TestOutageChoiceOffBlocksAllTraffic(t *testing.T) {
	c, s, ctx := pinAnnaToDE(t, false)
	if err := s.SetAIOutageChoice(ctx, []string{"anna"}, model.AIOutageChoice{Action: AIOutageOff}); err != nil {
		t.Fatal(err)
	}
	setNodeStatus(t, 2, "offline")
	s.StickCascadeUsers(ctx)
	if got := outageRules(t, c)["anna"]; got.Outbound != cascadeBlockTag(c.Id) || got.Domains != nil {
		t.Fatalf("outage rule = %+v, want all of anna's traffic blocked", got)
	}
}

func TestOutageChoiceBlocksOnlyTheChosenAIServices(t *testing.T) {
	c, s, ctx := pinAnnaToDE(t, false)
	choice := model.AIOutageChoice{Action: AIOutageBlock, Services: []string{"Claude"}}
	if err := s.SetAIOutageChoice(ctx, []string{"anna"}, choice); err != nil {
		t.Fatal(err)
	}
	setNodeStatus(t, 2, "offline")
	s.StickCascadeUsers(ctx)
	want := []string{"domain:anthropic.com", "domain:claude.ai", "domain:claude.com", "domain:claudeusercontent.com"}
	if got := outageRules(t, c)["anna"]; !reflect.DeepEqual(got.Domains, want) {
		t.Fatalf("blocked domains = %v, want only Claude's %v", got.Domains, want)
	}
	if got := s.AIOutageChoiceOf([]string{"anna"}); got.Action != AIOutageBlock || !reflect.DeepEqual(got.Services, []string{"Claude"}) {
		t.Fatalf("stored choice = %+v", got)
	}
}

func TestSetAIOutageChoiceRejectsUnknownInput(t *testing.T) {
	initJoinDB(t)
	s := &CascadeService{}
	for _, c := range []model.AIOutageChoice{
		{Action: "drop"},
		{Action: AIOutageBlock, Services: []string{"NoSuchAI"}},
	} {
		if err := s.SetAIOutageChoice(context.Background(), []string{"anna"}, c); err == nil {
			t.Fatalf("SetAIOutageChoice(%+v) accepted", c)
		}
	}
	if got := s.AIOutageChoiceOf([]string{"anna"}); got.Action != AIOutageBlock || len(got.Services) != 0 {
		t.Fatalf("a refused choice was stored: %+v", got)
	}
}

func TestAIPinHoldersAreTheUsersPinnedToTheServer(t *testing.T) {
	_, s, _ := pinAnnaToDE(t, false)
	if err := database.GetDB().Create(&model.CascadeFixedExit{Email: "bob", NodeId: 4}).Error; err != nil {
		t.Fatal(err)
	}
	for node, want := range map[int][]string{2: {"anna"}, 3: nil, 4: {"bob"}} {
		got, err := s.AIPinHolders(node)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("AIPinHolders(%d) = %v %v, want %v", node, got, err, want)
		}
	}
}

func TestAIServicesGroupTheDomainListByService(t *testing.T) {
	got := groupAIServices([]string{
		"domain:openai.com", "geosite:openai", "domain:claude.ai", "full:api.deepseek.com", "keyword:poe", "domain:labs.google",
	})
	want := []AIService{
		{Name: "ChatGPT", Domains: []string{"domain:openai.com", "geosite:openai"}},
		{Name: "Claude", Domains: []string{"domain:claude.ai"}},
		{Name: "deepseek.com", Domains: []string{"full:api.deepseek.com"}},
		{Name: "poe", Domains: []string{"keyword:poe"}},
		{Name: "Gemini", Domains: []string{"domain:labs.google"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groupAIServices = %+v\nwant %+v", got, want)
	}
}

// A dead AI-sites pin must not take over the user's other pin: the admin's DE
// pin keeps carrying the rest while only AI is cut.
func TestDeadAIDomainPinKeepsTheAdminPin(t *testing.T) {
	initJoinDB(t)
	c := seedAutoCascade(t)
	s, ctx := &CascadeService{}, context.Background()
	if err := s.SetFixedExit(ctx, &CascadeFixedExitRequest{Email: "anna", NodeId: 3}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetAICountry(ctx, []string{"anna"}, "NL"); err != nil {
		t.Fatal(err)
	}
	setNodeStatus(t, 4, "offline")
	s.StickCascadeUsers(ctx)
	plan, err := loadAIExitPlan()
	if err != nil {
		t.Fatal(err)
	}
	if got := fixedExitsIn(plan.Fixed, []int{2, 3, 4}, plan)["anna"]; got != 3 {
		t.Fatalf("anna's fixed exit = %d, want the admin's 3", got)
	}
	if got := outageRules(t, c)["anna"]; got.Outbound != cascadeBlockTag(c.Id) {
		t.Fatalf("outage rule = %+v, want AI blocked", got)
	}
}
