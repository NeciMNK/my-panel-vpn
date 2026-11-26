package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedFixedExitNodes(t *testing.T) {
	t.Helper()
	for _, n := range []*model.Node{
		{Id: 2, Name: "de-1", Address: "2.2.2.2", Port: 1, Enable: true, Status: "online", Country: "DE"},
		{Id: 3, Name: "de-2", Address: "3.3.3.3", Port: 1, Enable: true, Status: "online", Country: "DE"},
		{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Status: "online", Country: "NL"},
	} {
		if err := database.GetDB().Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func fixedExitMap(t *testing.T) map[string][]int {
	t.Helper()
	rows, err := (&CascadeService{}).ListFixedExits()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]int{}
	for _, r := range rows {
		out[r.Email] = append(out[r.Email], r.NodeId)
	}
	for _, ids := range out {
		slices.Sort(ids)
	}
	return out
}

func TestSetFixedExitKeepsOneExitPerCountry(t *testing.T) {
	initJoinDB(t)
	seedFixedExitNodes(t)
	s := &CascadeService{}
	ctx := context.Background()
	for _, req := range []CascadeFixedExitRequest{
		{Email: "anna", NodeId: 2},
		{Email: "bob", NodeId: 2},
		{Email: "anna", NodeId: 4},
		{Email: "anna", NodeId: 3},
	} {
		if err := s.SetFixedExit(ctx, &req); err != nil {
			t.Fatalf("set %+v: %v", req, err)
		}
	}
	want := map[string][]int{"anna": {3, 4}, "bob": {2}}
	if got := fixedExitMap(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed exits = %v, want %v (a second DE pin replaces the first, NL stays)", got, want)
	}

	if err := s.DeleteFixedExit(ctx, &CascadeFixedExitRequest{Email: "anna", NodeId: 3}); err != nil {
		t.Fatal(err)
	}
	if got := fixedExitMap(t); !reflect.DeepEqual(got, map[string][]int{"anna": {4}, "bob": {2}}) {
		t.Fatalf("after delete = %v", got)
	}

	err := s.SetFixedExit(ctx, &CascadeFixedExitRequest{Email: "anna", NodeId: 9})
	if err == nil || err.Error() != "node 9 does not exist" {
		t.Fatalf("unknown node: err = %v", err)
	}
}

func TestFixedExitsInCascadePrefersTheLatestPin(t *testing.T) {
	rows := []model.CascadeFixedExit{
		{Id: 1, Email: "anna", NodeId: 2},
		{Id: 2, Email: "bob", NodeId: 9},
		{Id: 3, Email: "anna", NodeId: 4},
	}
	got := fixedExitsIn(rows, []int{2, 3, 4}, aiExitPlan{})
	if want := map[string]int{"anna": 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed = %v, want %v", got, want)
	}
}

func TestSetFixedExitRoutesTheUserBeforeTheyConnect(t *testing.T) {
	initJoinDB(t)
	seedFixedExitNodes(t)
	if err := (&XraySettingService{}).SaveXraySetting(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`); err != nil {
		t.Fatal(err)
	}
	c := &model.Cascade{
		Name: "ru-de", Enable: true, EntryNodeId: 0, ExitNodeIds: []int{2, 3}, Strategy: "leastPing",
		Links: []model.CascadeLink{fixedTestLink(t, 2), fixedTestLink(t, 3)},
	}
	if err := database.GetDB().Create(c).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&CascadeService{}).SetFixedExit(context.Background(), &CascadeFixedExitRequest{Email: "anna", NodeId: 3}); err != nil {
		t.Fatal(err)
	}
	tmpl, err := loadLocalTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var pinRules []string
	for _, r := range templateRouting(tmpl.Config)["rules"].([]any) {
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), `"ruleTag":"`+cascadePrefix(c.Id)+"pin-") {
			pinRules = append(pinRules, string(raw))
		}
	}
	want := `{"outboundTag":"mp-cascade-1-n3","ruleTag":"mp-cascade-1-pin-n3","type":"field","user":["anna"]}`
	if len(pinRules) != 1 || pinRules[0] != want {
		t.Fatalf("pin rules = %v, want [%s]", pinRules, want)
	}
}

func fixedTestLink(t *testing.T, nodeID int) model.CascadeLink {
	t.Helper()
	_, pub, err := newRealityKeys()
	if err != nil {
		t.Fatal(err)
	}
	return model.CascadeLink{
		NodeId: nodeID, Address: fmt.Sprintf("%d.%d.%d.%d", nodeID, nodeID, nodeID, nodeID), Port: 443,
		ClientId: "1b4e28ba-2fa1-11d2-883f-0016d3cca427", PublicKey: pub, ShortId: "abcd", ServerName: "www.apple.com",
	}
}

func fixedExitModes(t *testing.T) map[string]string {
	t.Helper()
	rows, err := (&CascadeService{}).ListFixedExits()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[fmt.Sprintf("%s@%d", r.Email, r.NodeId)] = r.TrafficMode
	}
	return out
}

// An admin's pin says what it carries: AI sites only makes its country the
// user's one AI country, so a second AI-only pin moves it there.
func TestSetFixedExitChoosesWhatThePinCarries(t *testing.T) {
	initJoinDB(t)
	seedFixedExitNodes(t)
	s := &CascadeService{}
	ctx := context.Background()
	steps := []struct {
		req          CascadeFixedExitRequest
		modes        map[string]string
		aiIn         string
		aiAllTraffic bool
	}{
		{
			CascadeFixedExitRequest{Email: "anna", NodeId: 2, TrafficMode: FixedExitAIOnly},
			map[string]string{"anna@2": FixedExitAIOnly},
			"DE", false,
		},
		{
			CascadeFixedExitRequest{Email: "anna", NodeId: 4},
			map[string]string{"anna@2": FixedExitAIOnly, "anna@4": FixedExitAllTraffic},
			"DE", false,
		},
		{
			CascadeFixedExitRequest{Email: "anna", NodeId: 2, TrafficMode: FixedExitAllTraffic},
			map[string]string{"anna@2": FixedExitAllTraffic, "anna@4": FixedExitAllTraffic},
			"DE", true,
		},
		{
			CascadeFixedExitRequest{Email: "anna", NodeId: 4, TrafficMode: FixedExitAIOnly},
			map[string]string{"anna@2": FixedExitAllTraffic, "anna@4": FixedExitAIOnly},
			"NL", false,
		},
	}
	for i, st := range steps {
		if err := s.SetFixedExit(ctx, &st.req); err != nil {
			t.Fatalf("step %d set %+v: %v", i, st.req, err)
		}
		if got := fixedExitModes(t); !reflect.DeepEqual(got, st.modes) {
			t.Fatalf("step %d modes = %v, want %v", i, got, st.modes)
		}
		if country, all := s.AIMode([]string{"anna"}); country != st.aiIn || all != st.aiAllTraffic {
			t.Fatalf("step %d AI mode = %q all=%v, want %q all=%v", i, country, all, st.aiIn, st.aiAllTraffic)
		}
	}

	err := s.SetFixedExit(ctx, &CascadeFixedExitRequest{Email: "bob", NodeId: 0, TrafficMode: FixedExitAIOnly})
	if err == nil || err.Error() != "AI sites only needs a server with a country" {
		t.Fatalf("AI-only pin on a server without a country: err = %v", err)
	}
	if got := fixedExitModes(t); got["bob@0"] != "" {
		t.Fatalf("a refused AI-only pin was saved: %v", got)
	}
}
