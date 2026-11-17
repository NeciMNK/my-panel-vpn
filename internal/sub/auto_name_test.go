package sub

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func autoNameNodes() map[int]*model.Node {
	return map[int]*model.Node{
		1: {Id: 1, Country: "RU"}, 2: {Id: 2, Country: "NL"}, 3: {Id: 3, Country: "DE"},
		4: {Id: 4, Country: "NL"}, 5: {Id: 5},
	}
}

func onNode(id int, protocol model.Protocol, tag string) *model.Inbound {
	return &model.Inbound{NodeID: &id, Protocol: protocol, Tag: tag, Remark: "manual"}
}

// The names the subscription shows in a client app, as Nikita set them by hand.
func TestAutoServerName(t *testing.T) {
	split := func(exits ...int) model.Cascade {
		return model.Cascade{EntryNodeId: 1, ExitNodeIds: exits, Mode: "split", Strategy: "leastPing"}
	}
	cases := []struct {
		name     string
		cascades []model.Cascade
		inbound  *model.Inbound
		want     string
	}{
		{"pure vless", nil, onNode(2, model.VLESS, "a"), "🇳🇱 Pure NL"},
		{"pure hysteria", nil, onNode(3, model.Hysteria, "a"), "🇩🇪 Hysteria Pure DE"},
		{"pure russia warns", nil, onNode(1, model.VLESS, "a"), "🇷🇺 Pure RU (Direct Russia)"},
		{"split vless", []model.Cascade{split(2)}, onNode(1, model.VLESS, "a"), "🇷🇺 / 🇳🇱 Split RU / NL"},
		{"split hysteria", []model.Cascade{split(3)}, onNode(1, model.Hysteria, "a"), "🇷🇺 / 🇩🇪 Hysteria Split (DE)"},
		{"one country, many exits", []model.Cascade{split(2, 4)}, onNode(1, model.VLESS, "a"), "🇷🇺 / 🇳🇱 Split RU / NL"},
		{"auto best ping", []model.Cascade{split(2, 3)}, onNode(1, model.VLESS, "a"), "🇷🇺 / ⚡ Split Auto (Best Ping)"},
		{"auto hysteria", []model.Cascade{split(2, 3)}, onNode(1, model.Hysteria, "a"), "🇷🇺 / ⚡ Hysteria Split Auto (Best Ping)"},
		{
			"auto least load",
			[]model.Cascade{{EntryNodeId: 1, ExitNodeIds: []int{2, 3}, Mode: "split", Strategy: "leastLoad"}},
			onNode(1, model.VLESS, "a"), "🇷🇺 / ⚡ Split Auto (Least Load)",
		},
		{
			"relay mode",
			[]model.Cascade{{EntryNodeId: 1, ExitNodeIds: []int{2}, Mode: "relay"}},
			onNode(1, model.VLESS, "a"), "🇷🇺 / 🇳🇱 Relay RU / NL",
		},
		{
			"both mode relays only its relay inbounds",
			[]model.Cascade{{EntryNodeId: 1, ExitNodeIds: []int{2}, Mode: "both", RelayInboundTags: []string{"r"}}},
			onNode(1, model.VLESS, "r"), "🇷🇺 / 🇳🇱 Relay RU / NL",
		},
		{
			"both mode splits the rest",
			[]model.Cascade{{EntryNodeId: 1, ExitNodeIds: []int{2}, Mode: "both", RelayInboundTags: []string{"r"}}},
			onNode(1, model.VLESS, "s"), "🇷🇺 / 🇳🇱 Split RU / NL",
		},
		{
			"inbound left out of the cascade is pure",
			[]model.Cascade{{EntryNodeId: 1, ExitNodeIds: []int{2}, Mode: "split", EntryInboundTags: []string{"other"}}},
			onNode(1, model.VLESS, "a"), "🇷🇺 Pure RU (Direct Russia)",
		},
		{"cascade of another entry is ignored", []model.Cascade{{EntryNodeId: 3, ExitNodeIds: []int{2}}}, onNode(1, model.VLESS, "a"), "🇷🇺 Pure RU (Direct Russia)"},
		{"first cascade wins", []model.Cascade{split(3), split(2)}, onNode(1, model.VLESS, "a"), "🇷🇺 / 🇩🇪 Split RU / DE"},
		{"entry without country keeps remark", nil, onNode(5, model.VLESS, "a"), "manual"},
		{"master without country keeps remark", nil, &model.Inbound{Protocol: model.VLESS, Remark: "manual"}, "manual"},
		{"exit without country keeps remark", []model.Cascade{split(5)}, onNode(1, model.VLESS, "a"), "manual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &SubService{nodesByID: autoNameNodes(), cascades: tc.cascades}
			if got := s.autoServerName(tc.inbound); got != tc.want {
				t.Fatalf("autoServerName = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two links of one name on different transports must stay apart in the client
// app, as "Pure NL" and "Pure NL (XHTTP)"; re-rendering a link keeps its name.
func TestAutoNameTemplate_TransportSuffixOnCollision(t *testing.T) {
	s := &SubService{remarkTemplate: "{{AUTO_NAME}}", subscriptionBody: true, nodesByID: autoNameNodes()}
	grpc, xhttp := onNode(2, model.VLESS, "g"), onNode(2, model.VLESS, "x")
	john, anna := model.Client{Email: "john"}, model.Client{Email: "anna"}
	steps := []struct {
		inbound   *model.Inbound
		client    model.Client
		transport string
		want      string
	}{
		{grpc, john, "grpc", "🇳🇱 Pure NL"},
		{xhttp, john, "xhttp", "🇳🇱 Pure NL (XHTTP)"},
		{xhttp, john, "xhttp", "🇳🇱 Pure NL (XHTTP)"},
		{grpc, john, "grpc", "🇳🇱 Pure NL"},
		{xhttp, anna, "xhttp", "🇳🇱 Pure NL"},
	}
	for i, st := range steps {
		if got := s.genTemplatedRemark(st.inbound, st.client, "", st.transport); got != st.want {
			t.Fatalf("step %d: remark = %q, want %q", i, got, st.want)
		}
	}
}

// A disabled cascade no longer carries traffic, so its entry is named Pure again.
func TestPrepareForRequest_LoadsOnlyEnabledCascades(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	for _, n := range []*model.Node{
		{Name: "ru", Address: "ru.example.com", Port: 2053, ApiToken: "t", Country: "RU"},
		{Name: "nl", Address: "nl.example.com", Port: 2053, ApiToken: "t", Country: "NL"},
		{Name: "de", Address: "de.example.com", Port: 2053, ApiToken: "t", Country: "DE"},
	} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	off := &model.Cascade{Name: "off", EntryNodeId: 1, ExitNodeIds: []int{3}, Mode: "split"}
	on := &model.Cascade{Name: "on", Enable: true, EntryNodeId: 1, ExitNodeIds: []int{2}, Mode: "split"}
	for _, c := range []*model.Cascade{off, on} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(off).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	s := &SubService{}
	s.PrepareForRequest("sub.example.com")
	if got, want := s.autoServerName(onNode(1, model.VLESS, "a")), "🇷🇺 / 🇳🇱 Split RU / NL"; got != want {
		t.Fatalf("autoServerName = %q, want %q", got, want)
	}
}
