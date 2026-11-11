package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// seedUserExitWorld: anna's inbound enters a master cascade to DE (2, 3) and
// NL (4); a cascade from node 5 to FR (6) carries other users only.
func seedUserExitWorld(t *testing.T) {
	t.Helper()
	db := database.GetDB()
	five := 5
	rows := []any{
		&model.Node{Id: 2, Name: "de-1", Address: "2.2.2.2", Port: 1, Enable: true, Status: "online", Country: "DE"},
		&model.Node{Id: 3, Name: "de-2", Address: "3.3.3.3", Port: 1, Enable: true, Status: "online", Country: "DE"},
		&model.Node{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Status: "online", Country: "NL"},
		&model.Node{Id: 5, Name: "ru-1", Address: "5.5.5.5", Port: 1, Enable: true, Status: "online", Country: "RU"},
		&model.Node{Id: 6, Name: "fr-1", Address: "6.6.6.6", Port: 1, Enable: true, Status: "online", Country: "FR"},
		&model.Inbound{Id: 1, Tag: "in-anna", Protocol: model.VLESS, Port: 443, Enable: true},
		&model.Inbound{Id: 2, Tag: "in-ru", Protocol: model.VLESS, Port: 8443, Enable: true, NodeID: &five},
		&model.ClientRecord{Id: 1, Email: "anna"},
		&model.ClientInbound{ClientId: 1, InboundId: 1},
		&model.Cascade{Name: "eu", Enable: true, EntryNodeId: 0, Links: []model.CascadeLink{{NodeId: 2}, {NodeId: 3}, {NodeId: 4}}},
		&model.Cascade{Name: "fr", Enable: true, EntryNodeId: 5, Links: []model.CascadeLink{{NodeId: 6}}},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func setNodeLoad(t *testing.T, nodeID, capacityMbps int, usedMbps uint64) {
	t.Helper()
	if err := database.GetDB().Create(&model.ServerCapacity{NodeId: nodeID, CapacityMbps: capacityMbps}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", nodeID).Update("net_down", usedMbps*1e6/8).Error; err != nil {
		t.Fatal(err)
	}
}

func groupSummary(groups []UserExitGroup) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, g := range groups {
		out[g.Country] = map[int]bool{}
		for _, e := range g.Exits {
			out[g.Country][e.NodeId] = e.Saturated
		}
	}
	return out
}

func TestUserExitGroupsOffersOnlyTheUsersCascadesAndMarksFullServers(t *testing.T) {
	initJoinDB(t)
	seedUserExitWorld(t)
	setNodeLoad(t, 3, 100, 100)
	setNodeLoad(t, 2, 1000, 100)

	groups, err := (&CascadeService{}).UserExitGroups([]string{"anna"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[int]bool{"DE": {2: false, 3: true}, "NL": {4: false}}
	if got := groupSummary(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("groups (node -> saturated) = %v, want %v", got, want)
	}
	if best, ok := groups[0].Best(); !ok || best.NodeId != 2 {
		t.Fatalf("best DE exit = %+v %v, want node 2 (node 3 is full)", best, ok)
	}
}

func TestUserExitGroupsHonoursTheUsersMinimumSpeed(t *testing.T) {
	initJoinDB(t)
	seedUserExitWorld(t)
	setNodeLoad(t, 2, 100, 70)
	if err := (&SpeedService{}).SetClient(&ClientSpeedRequest{Email: "anna", MinMbps: 50}); err != nil {
		t.Fatal(err)
	}
	groups, err := (&CascadeService{}).UserExitGroups([]string{"anna"})
	if err != nil {
		t.Fatal(err)
	}
	if got := groupSummary(groups)["DE"]; !reflect.DeepEqual(got, map[int]bool{2: true, 3: false}) {
		t.Fatalf("DE = %v, want node 2 saturated (30 Mbit/s free < 50 minimum)", got)
	}
}

func TestPinSoleExitsPinsOnlyCountriesWithoutAChoice(t *testing.T) {
	initJoinDB(t)
	seedUserExitWorld(t)
	s := &CascadeService{}
	pinned, err := s.PinSoleExits(context.Background(), []string{"anna"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || pinned[0].NodeId != 4 {
		t.Fatalf("auto-pinned = %+v, want only NL node 4", pinned)
	}
	if got := fixedExitMap(t); !reflect.DeepEqual(got, map[string][]int{"anna": {4}}) {
		t.Fatalf("fixed exits = %v", got)
	}
	again, err := s.PinSoleExits(context.Background(), []string{"anna"})
	if err != nil || len(again) != 0 {
		t.Fatalf("second pass pinned %+v (%v), want nothing new", again, err)
	}
}

func TestPinUserExitRefusesAFullServerAndReplacesTheCountryPin(t *testing.T) {
	initJoinDB(t)
	seedUserExitWorld(t)
	setNodeLoad(t, 3, 100, 100)
	s := &CascadeService{}
	ctx := context.Background()

	if _, err := s.PinUserExit(ctx, []string{"anna"}, 3); err == nil || err.Error() != "server 3 is saturated" {
		t.Fatalf("pin to full node: err = %v", err)
	}
	if _, err := s.PinUserExit(ctx, []string{"anna"}, 6); err == nil || err.Error() != "server 6 is not one of your exits" {
		t.Fatalf("pin to another user's exit: err = %v", err)
	}
	if err := database.GetDB().Create(&model.CascadeFixedExit{Email: "anna", NodeId: 3}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PinUserExit(ctx, []string{"anna"}, 2); err != nil {
		t.Fatal(err)
	}
	if got := fixedExitMap(t); !reflect.DeepEqual(got, map[string][]int{"anna": {2}}) {
		t.Fatalf("fixed exits = %v, want the DE pin moved to node 2", got)
	}
}

func TestBestExitPrefersMostFreeSpeedThenUnknownCapacityWithFewestUsers(t *testing.T) {
	g := UserExitGroup{Exits: []UserExit{
		{NodeId: 1, Capacity: true, FreeMbps: 50},
		{NodeId: 2, Capacity: true, FreeMbps: 900},
		{NodeId: 3, Capacity: true, FreeMbps: 2000, Saturated: true},
	}}
	if best, _ := g.Best(); best.NodeId != 2 {
		t.Fatalf("best = %d, want 2 (most free speed among open servers)", best.NodeId)
	}
	g.Exits = append(g.Exits, UserExit{NodeId: 4, Online: 3}, UserExit{NodeId: 5, Online: 1})
	if best, _ := g.Best(); best.NodeId != 5 {
		t.Fatalf("best = %d, want 5 (no capacity limit, fewest users)", best.NodeId)
	}
	if _, ok := (UserExitGroup{Exits: []UserExit{{NodeId: 1, Saturated: true}}}).Best(); ok {
		t.Fatal("a group of saturated servers must offer no auto pick")
	}
}
