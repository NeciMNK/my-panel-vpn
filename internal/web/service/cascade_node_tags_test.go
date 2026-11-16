package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A node's Xray knows its inbounds by the bare tag (wireInbound strips n<id>-),
// so a master-form tag in a node's cascade rule never matches and traffic leaks.
func TestCascadeRulesOnANodeUseTheNodesInboundTags(t *testing.T) {
	cfg := baseTemplate(t)
	c := &model.Cascade{
		Id: 4, Mode: CascadeModeBoth, Strategy: "leastPing",
		EntryInboundTags: []string{"n3-in-2053-udp", "in-2053-udp"},
		RelayInboundTags: []string{"n3-in-2083-tcp"},
		Links:            []model.CascadeLink{{NodeId: 2}, {NodeId: 5}},
	}
	pins := []model.CascadePin{{CascadeId: 4, NodeId: 2, Email: "a@x"}}
	applyCascadeToTemplate(cfg, c, 3, pins, aiExitPlan{}, cascadeZone{})

	byTag := map[string][]string{}
	for _, r := range templateRouting(cfg)["rules"].([]any) {
		m := r.(map[string]any)
		if tag, _ := m["ruleTag"].(string); tag != "" {
			byTag[tag], _ = m["inboundTag"].([]string)
		}
	}
	want := map[string][]string{
		"mp-cascade-4-route":         {"in-2053-udp"},
		"mp-cascade-4-direct-domain": {"in-2053-udp"},
		"mp-cascade-4-pin-n2":        {"in-2053-udp"},
		"mp-cascade-4-relay":         {"in-2083-tcp"},
		"mp-cascade-4-relay-pin-n2":  {"in-2083-tcp"},
	}
	for tag, inbounds := range want {
		if !slices.Equal(byTag[tag], inbounds) {
			t.Errorf("rule %s inboundTag = %v, want %v", tag, byTag[tag], inbounds)
		}
	}
	if !slices.Equal(c.EntryInboundTags, []string{"n3-in-2053-udp", "in-2053-udp"}) {
		t.Errorf("applying rewrote the stored tags: %v", c.EntryInboundTags)
	}
}

// The bot, pins and exit choice match a cascade by the master's inbound tag
// (cascadeEnters), so a node-form tag typed in the form must be stored as that.
func TestCascadeStoresTheMastersInboundTags(t *testing.T) {
	initJoinDB(t)
	db := database.GetDB()
	for _, n := range []*model.Node{
		{Id: 2, Name: "de", Address: "2.2.2.2", Port: 1, Enable: true},
		{Id: 3, Name: "ru", Address: "3.3.3.3", Port: 1, Enable: true},
	} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	ru := 3
	udp := &model.Inbound{Tag: "n3-in-2053-udp", Port: 2053, NodeID: &ru, Protocol: model.VLESS}
	tcp := &model.Inbound{Tag: "n3-in-2083-tcp", Port: 2083, NodeID: &ru, Protocol: model.VLESS}
	for _, ib := range []*model.Inbound{udp, tcp} {
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
	}

	t.Run("node form and both forms collapse to the master form", func(t *testing.T) {
		req := &CascadeRequest{
			EntryNodeId: 3, ExitNodeIds: []int{2}, Mode: CascadeModeBoth,
			EntryInboundTags: []string{"in-2053-udp", "n3-in-2053-udp", "custom"},
			RelayInboundTags: []string{"in-2083-tcp"},
		}
		if err := (&CascadeService{}).validate(req); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !slices.Equal(req.EntryInboundTags, []string{"n3-in-2053-udp", "custom"}) ||
			!slices.Equal(req.RelayInboundTags, []string{"n3-in-2083-tcp"}) {
			t.Fatalf("stored entry %v relay %v", req.EntryInboundTags, req.RelayInboundTags)
		}
		c := &model.Cascade{EntryNodeId: 3, EntryInboundTags: req.EntryInboundTags}
		if !cascadeEnters(c, udp) {
			t.Fatal("cascadeEnters misses the inbound the cascade was saved with")
		}
	})

	t.Run("one inbound in both lists is caught across tag forms", func(t *testing.T) {
		req := &CascadeRequest{
			EntryNodeId: 3, ExitNodeIds: []int{2}, Mode: CascadeModeBoth,
			EntryInboundTags: []string{"n3-in-2053-udp"},
			RelayInboundTags: []string{"in-2053-udp"},
		}
		err := (&CascadeService{}).validate(req)
		if err == nil || strings.TrimSpace(err.Error()) != "inbound n3-in-2053-udp is listed both for relay and for split" {
			t.Fatalf("validate = %v", err)
		}
	})
}
