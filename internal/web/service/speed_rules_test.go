package service

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestSpeedLimitsFloatFreeUsersOnWhatGuaranteesLeave(t *testing.T) {
	free := speedRule{Floor: 50, Max: 100}
	paid := speedRule{Min: 100}
	rules := map[string]speedRule{"bob": paid, "eve": paid, "dina": {Max: 30}}
	for _, e := range []string{"anna", "carl", "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8"} {
		rules[e] = free
	}
	cases := []struct {
		name  string
		users map[int][]string
		caps  map[int]int
		want  map[string]speedLimit
	}{
		{
			name:  "ten free users split what two guarantees leave of 1 Gbit/s",
			users: map[int][]string{0: {"bob", "eve", "anna", "carl", "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8"}},
			caps:  map[int]int{0: 1000},
			want: map[string]speedLimit{
				"bob": {Min: 100}, "eve": {Min: 100}, "anna": {Max: 80}, "carl": {Max: 80},
				"f1": {Max: 80}, "f2": {Max: 80}, "f3": {Max: 80}, "f4": {Max: 80}, "f5": {Max: 80}, "f6": {Max: 80}, "f7": {Max: 80}, "f8": {Max: 80},
			},
		},
		{
			name:  "a crowded server never drops free users below the floor",
			users: map[int][]string{0: {"bob", "anna", "carl", "f1"}},
			caps:  map[int]int{0: 200},
			want:  map[string]speedLimit{"bob": {Min: 100}, "anna": {Max: 50}, "carl": {Max: 50}, "f1": {Max: 50}},
		},
		{
			name:  "a quiet server gives free users the top of their range",
			users: map[int][]string{0: {"anna"}},
			caps:  map[int]int{0: 1000},
			want:  map[string]speedLimit{"anna": {Max: 100}},
		},
		{
			name:  "a cascade user gets the smaller share of the entry and the exit, in 10 Mbit/s steps",
			users: map[int][]string{0: {"anna"}, 3: {"anna", "carl", "bob"}},
			caps:  map[int]int{0: 1000, 3: 250},
			want:  map[string]speedLimit{"anna": {Max: 70}, "carl": {Max: 70}, "bob": {Min: 100}},
		},
		{
			name:  "a server without a known link speed leaves free users at their top speed",
			users: map[int][]string{5: {"anna", "carl"}},
			caps:  map[int]int{},
			want:  map[string]speedLimit{"anna": {Max: 100}, "carl": {Max: 100}},
		},
		{
			name:  "a client's own cap is fixed and does not take a free user's share",
			users: map[int][]string{0: {"dina", "anna"}},
			caps:  map[int]int{0: 120},
			want:  map[string]speedLimit{"dina": {Max: 30}, "anna": {Max: 100}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := speedLimits(rules, tc.users, tc.caps); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("limits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSpeedRulesPreferTheClientsOwnSpeedOverTheirGroups(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	for _, c := range []model.ClientRecord{
		{Email: "anna", Group: model.ClientGroupUsers},
		{Email: "bob", Group: model.ClientGroupPaid},
		{Email: "gran", Group: "family"},
		{Email: "kid", Group: "family"},
		{Email: "mp-link", Group: model.ClientGroupTech},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &SpeedService{}
	if err := s.SetGroup(&GroupSpeedRequest{Group: "family", MinMbps: 200}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClient(&ClientSpeedRequest{Email: "kid", MaxMbps: 20}); err != nil {
		t.Fatal(err)
	}
	got, err := speedRules()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]speedRule{
		"anna": {Floor: 50, Max: 100},
		"bob":  {Min: 100},
		"gran": {Min: 200},
		"kid":  {Max: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %+v, want %+v", got, want)
	}
}

func TestSetGroupSpeedValidatesAndRemoves(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	s := &SpeedService{}
	for _, bad := range []struct {
		req  GroupSpeedRequest
		want string
	}{
		{GroupSpeedRequest{Group: " "}, "pick a group"},
		{GroupSpeedRequest{Group: "family", MaxMbps: -1}, "speeds cannot be negative"},
		{GroupSpeedRequest{Group: "family", MinMbps: 50, MaxMbps: 20}, "the minimum speed cannot be above the maximum"},
		{GroupSpeedRequest{Group: "family", FloorMbps: 50}, "a floating cap needs a top speed at or above its floor"},
		{GroupSpeedRequest{Group: "family", FloorMbps: 80, MaxMbps: 60}, "a floating cap needs a top speed at or above its floor"},
		{GroupSpeedRequest{Group: "family", MinMbps: 70, FloorMbps: 50, MaxMbps: 100}, "the minimum speed cannot be above the floating cap's floor"},
	} {
		if err := s.SetGroup(&bad.req); err == nil || strings.TrimSpace(err.Error()) != bad.want {
			t.Fatalf("%+v: err = %v, want %q", bad.req, err, bad.want)
		}
	}
	if err := s.SetGroup(&GroupSpeedRequest{Group: " users ", FloorMbps: 40, MaxMbps: 80}); err != nil {
		t.Fatal(err)
	}
	var row model.GroupSpeed
	database.GetDB().Where("group_name = ?", "users").First(&row)
	if row.FloorMbps != 40 || row.MaxMbps != 80 || row.MinMbps != 0 {
		t.Fatalf("users after edit = %+v", row)
	}
	if err := s.SetGroup(&GroupSpeedRequest{Group: "users"}); err != nil {
		t.Fatal(err)
	}
	var left int64
	database.GetDB().Model(&model.GroupSpeed{}).Where("group_name = ?", "users").Count(&left)
	if left != 0 {
		t.Fatalf("all speeds 0 must remove the group's row, %d left", left)
	}
}

func TestCascadeExitUsersFollowPinsAndOneExitCascades(t *testing.T) {
	cascades := []model.Cascade{
		{Id: 1, EntryNodeId: 0, Links: []model.CascadeLink{{NodeId: 3}}},
		{Id: 2, EntryNodeId: 4, Links: []model.CascadeLink{{NodeId: 5}, {NodeId: 6}}},
	}
	online := map[int][]string{0: {"anna", "bob"}, 4: {"carl", "dina"}}
	entered := map[int]map[string]bool{1: {"anna": true}, 2: {"carl": true, "dina": true}}
	pins := map[int]map[string]int{2: {"carl": 6}}
	got := cascadeExitUsers(cascades, online, entered, pins)
	want := map[int][]string{3: {"anna"}, 6: {"carl"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exit users = %+v, want %+v (bob's inbound is not in the cascade, dina has no pin yet)", got, want)
	}
}
