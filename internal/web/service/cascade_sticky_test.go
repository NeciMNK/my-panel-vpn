package service

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func pinMap(pins []model.CascadePin) map[string]int {
	out := map[string]int{}
	for _, p := range pins {
		out[p.Email] = p.NodeId
	}
	return out
}

func emailSet(items ...string) map[string]bool {
	out := map[string]bool{}
	for _, s := range items {
		out[s] = true
	}
	return out
}

func TestNextCascadePins(t *testing.T) {
	const now = int64(100_000)
	hold := int64(CascadePinHold.Seconds())
	allUp := map[int]bool{2: true, 5: true}
	cases := []struct {
		name        string
		pins        []model.CascadePin
		view        stickyView
		want        map[string]int
		wantChanged bool
	}{
		{
			name:        "newcomers spread over the least loaded exits",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: now}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna", "bob", "carl")},
			want:        map[string]int{"anna": 2, "bob": 5, "carl": 2},
			wantChanged: true,
		},
		{
			name:        "auto reconnect inside the hold keeps the exit",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now - hold}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp},
			want:        map[string]int{"anna": 5},
			wantChanged: false,
		},
		{
			name:        "offline past the hold releases the exit",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now - hold - 1}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp},
			want:        map[string]int{},
			wantChanged: true,
		},
		{
			name:        "switching to another server by hand releases at once",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now - 10}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineElsewhere: emailSet("anna")},
			want:        map[string]int{},
			wantChanged: true,
		},
		{
			name: "a second device elsewhere does not break the pin",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now - 10}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp,
				OnlineHere: emailSet("anna"), OnlineElsewhere: emailSet("anna"),
			},
			want:        map[string]int{"anna": 5},
			wantChanged: false,
		},
		{
			name:        "a dead exit hands its users to a live one",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: map[int]bool{2: true}, OnlineHere: emailSet("anna")},
			want:        map[string]int{"anna": 2},
			wantChanged: true,
		},
		{
			name: "a newcomer with a minimum skips the less loaded exit without room",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna", "bob"),
				FreeMbps: map[int]float64{2: 10, 5: 60}, MinMbps: map[string]int{"bob": 50},
			},
			want:        map[string]int{"anna": 5, "bob": 5},
			wantChanged: true,
		},
		{
			name: "an exit with room is not traded for a less loaded one without",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna", "bob"),
				FreeMbps: map[int]float64{2: 60, 5: 10}, MinMbps: map[string]int{"bob": 50},
			},
			want:        map[string]int{"anna": 2, "bob": 2},
			wantChanged: true,
		},
		{
			name: "with no exit left with room the least loaded one still takes the user",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna", "bob"),
				FreeMbps: map[int]float64{2: 5, 5: 10}, MinMbps: map[string]int{"bob": 50},
			},
			want:        map[string]int{"anna": 2, "bob": 5},
			wantChanged: true,
		},
		{
			name:        "a fixed user is pinned before they connect",
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp, Fixed: map[string]int{"anna": 5}},
			want:        map[string]int{"anna": 5},
			wantChanged: true,
		},
		{
			name: "a fixed pin outlives the hold and other servers",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now - hold - 1}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineElsewhere: emailSet("anna"),
				Fixed: map[string]int{"anna": 5},
			},
			want:        map[string]int{"anna": 5},
			wantChanged: false,
		},
		{
			name: "a fixed user ignores load and minimum speed",
			pins: []model.CascadePin{{Id: 1, Email: "bob", NodeId: 5, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna", "bob"),
				FreeMbps: map[int]float64{2: 60, 5: 1}, MinMbps: map[string]int{"anna": 50},
				Fixed: map[string]int{"anna": 5},
			},
			want:        map[string]int{"anna": 5, "bob": 5},
			wantChanged: true,
		},
		{
			name: "a fixed user falls back to a live exit while theirs is down",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 5, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: map[int]bool{2: true}, OnlineHere: emailSet("anna"),
				Fixed: map[string]int{"anna": 5},
			},
			want:        map[string]int{"anna": 2},
			wantChanged: true,
		},
		{
			name: "a fixed user returns to their exit once it is back",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna"),
				Fixed: map[string]int{"anna": 5},
			},
			want:        map[string]int{"anna": 5},
			wantChanged: true,
		},
		{
			name: "a fixed exit outside the cascade changes nothing",
			pins: []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: now}},
			view: stickyView{
				Now: now, Exits: []int{2, 5}, Healthy: allUp, OnlineHere: emailSet("anna"),
				Fixed: map[string]int{"anna": 9},
			},
			want:        map[string]int{"anna": 2},
			wantChanged: false,
		},
		{
			name:        "a removed exit drops its pins",
			pins:        []model.CascadePin{{Id: 1, Email: "anna", NodeId: 9, LastSeen: now}},
			view:        stickyView{Now: now, Exits: []int{2, 5}, Healthy: allUp},
			want:        map[string]int{},
			wantChanged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := nextCascadePins(tc.pins, tc.view)
			if m := pinMap(got); !reflect.DeepEqual(m, tc.want) {
				t.Fatalf("pins = %v, want %v", m, tc.want)
			}
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

func TestNextCascadePinsRefreshesLastSeenOfOnlineUsers(t *testing.T) {
	pins := []model.CascadePin{{Id: 1, Email: "anna", NodeId: 2, LastSeen: 10}}
	got, _ := nextCascadePins(pins, stickyView{Now: 500, Exits: []int{2}, Healthy: map[int]bool{2: true}, OnlineHere: emailSet("anna")})
	if len(got) != 1 || got[0].LastSeen != 500 || got[0].Id != 1 {
		t.Fatalf("pins = %+v", got)
	}
}

func TestCascadePinRulesGoRightBeforeTheBalancerRule(t *testing.T) {
	cfg := baseTemplate(t)
	c := &model.Cascade{Id: 7, Strategy: "leastPing", EntryInboundTags: []string{"inbound-443"}, Links: []model.CascadeLink{
		{NodeId: 2, Address: "a", Port: 1}, {NodeId: 5, Address: "b", Port: 2},
	}}
	pins := []model.CascadePin{{Email: "bob", NodeId: 5}, {Email: "anna", NodeId: 5}, {Email: "carl", NodeId: 9}}
	applyCascadeToTemplate(cfg, c, pins, cascadeZone{})

	tags := ruleTags(cfg)
	want := []string{"->api", "->blocked", "mp-cascade-7-pin-n5", "mp-cascade-7-route"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("rules = %v, want %v", tags, want)
	}
	raw, _ := json.Marshal(templateRouting(cfg)["rules"].([]any)[2])
	if string(raw) != `{"inboundTag":["inbound-443"],"outboundTag":"mp-cascade-7-n5","ruleTag":"mp-cascade-7-pin-n5","type":"field","user":["anna","bob"]}` {
		t.Fatalf("pin rule = %s", raw)
	}

	stripManaged(cfg, cascadePrefix(7))
	if tags := ruleTags(cfg); !reflect.DeepEqual(tags, []string{"->api", "->blocked"}) {
		t.Fatalf("strip left pin rules: %v", tags)
	}
}

func TestCascadeOnlineSplitsEntryFromOtherServers(t *testing.T) {
	byGuid := map[string][]string{
		"g-entry": {"anna", "mp-cascade-1-n2"},
		"g-other": {"bob"},
	}
	here, elsewhere := cascadeOnline(3, byGuid, map[int]string{0: "g-self", 3: "g-entry", 4: "g-other"})
	keys := func(m map[string]bool) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if got := keys(here); !reflect.DeepEqual(got, []string{"anna"}) {
		t.Fatalf("here = %v", got)
	}
	if got := keys(elsewhere); !reflect.DeepEqual(got, []string{"bob"}) {
		t.Fatalf("elsewhere = %v", got)
	}
}

func TestSaveCascadePinsStoresTheNextSet(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	first := []model.CascadePin{{Email: "anna", NodeId: 2, LastSeen: 1}, {Email: "bob", NodeId: 5, LastSeen: 1}}
	if err := saveCascadePins(4, nil, first); err != nil {
		t.Fatal(err)
	}
	old, err := loadCascadePins(4)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := nextCascadePins(old, stickyView{Now: 2, Exits: []int{2, 5}, Healthy: map[int]bool{2: true}, OnlineHere: emailSet("anna", "bob")})
	if err := saveCascadePins(4, old, next); err != nil {
		t.Fatal(err)
	}
	stored, err := loadCascadePins(4)
	if err != nil {
		t.Fatal(err)
	}
	if m := pinMap(stored); !reflect.DeepEqual(m, map[string]int{"anna": 2, "bob": 2}) || len(stored) != 2 {
		t.Fatalf("stored pins = %+v", stored)
	}
}
