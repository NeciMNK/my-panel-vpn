package service

import (
	"context"
	"encoding/json"
	"reflect"
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

func pinRuleOutbounds(t *testing.T, c *model.Cascade) map[string]string {
	t.Helper()
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range templateRouting(tmpl.Config)["rules"].([]any) {
		rule := r.(map[string]any)
		tag, _ := rule["ruleTag"].(string)
		if !strings.HasPrefix(tag, cascadePrefix(c.Id)+"pin-") {
			continue
		}
		var users []string
		if err := json.Unmarshal(mustJSON(t, rule["user"]), &users); err != nil {
			t.Fatal(err)
		}
		for _, u := range users {
			out[u] = rule["outboundTag"].(string)
		}
	}
	return out
}

// A user pinned in both countries of an auto cascade left through whichever
// pin came last; with "AI only in NL" the NL pin must win.
func TestAICountryPinWinsInAutoCascade(t *testing.T) {
	initJoinDB(t)
	c := seedAutoCascade(t)
	s := &CascadeService{}
	ctx := context.Background()
	for _, id := range []int{4, 3} {
		if err := s.SetFixedExit(ctx, &CascadeFixedExitRequest{Email: "anna", NodeId: id}); err != nil {
			t.Fatal(err)
		}
	}
	if got := pinRuleOutbounds(t, c)["anna"]; got != "mp-cascade-1-n3" {
		t.Fatalf("without an AI country anna leaves through %q, want the latest pin n3", got)
	}

	exit, pinned, err := s.SetAICountry(ctx, []string{"anna"}, "nl")
	if err != nil || !pinned || exit.NodeId != 4 {
		t.Fatalf("SetAICountry = %+v %v %v, want NL node 4 pinned", exit, pinned, err)
	}
	if got := pinRuleOutbounds(t, c)["anna"]; got != "mp-cascade-1-n4" {
		t.Fatalf("with AI only in NL anna leaves through %q, want n4", got)
	}
	if got := s.AICountry([]string{"anna"}); got != "NL" {
		t.Fatalf("AICountry = %q, want NL", got)
	}

	if _, _, err := s.SetAICountry(ctx, []string{"anna"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := pinRuleOutbounds(t, c)["anna"]; got != "mp-cascade-1-n3" {
		t.Fatalf("after clearing the AI country anna leaves through %q, want the latest pin n3 again", got)
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
