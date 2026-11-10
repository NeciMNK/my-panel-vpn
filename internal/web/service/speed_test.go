package service

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/shaper"
)

func TestSpeedShortagesOnlyWhenNoServerHasRoom(t *testing.T) {
	online := map[int][]string{0: {"anna", "bob", "carl"}, 3: {"dina"}}
	mins := map[string]int{"anna": 50, "bob": 10, "dina": 80}
	cases := []struct {
		name  string
		loads map[int]serverLoad
		want  []SpeedShortage
	}{
		{
			name:  "a server with room for its users raises nothing",
			loads: map[int]serverLoad{0: {Capacity: 1000, Used: 400, Online: 3}, 3: {Capacity: 1000, Used: 0, Online: 1}},
		},
		{
			name:  "a starved server is not reported while another one has room",
			loads: map[int]serverLoad{0: {Capacity: 100, Used: 70, Online: 3}, 3: {Capacity: 1000, Used: 100, Online: 1}},
		},
		{
			name:  "every server short lists the users below their minimum",
			loads: map[int]serverLoad{0: {Capacity: 100, Used: 70, Online: 3}, 3: {Capacity: 100, Used: 50, Online: 1}},
			want: []SpeedShortage{
				{NodeId: 0, FreePerUserMbps: 10, MinMbps: 50, Users: []string{"anna"}},
				{NodeId: 3, FreePerUserMbps: 50, MinMbps: 80, Users: []string{"dina"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := speedShortages(tc.loads, online, mins); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("shortages = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShapingPlansCapRecentIPsOnTheServerTheyWereSeenOn(t *testing.T) {
	const now = int64(10_000)
	stale := now - int64(clientIPWindow.Seconds()) - 1
	ips := []model.NodeClientIp{
		{NodeGuid: "self", Email: "anna", Ips: `[{"ip":"10.0.0.2","timestamp":9990},{"ip":"10.0.0.3","timestamp":` + strconv.FormatInt(stale, 10) + `}]`},
		{NodeGuid: "de", Email: "anna", Ips: `[{"ip":"10.0.0.9","timestamp":9000}]`},
		{NodeGuid: "de", Email: "bob", Ips: `[{"ip":"10.0.0.7","timestamp":9999}]`},
		{NodeGuid: "gone", Email: "anna", Ips: `[{"ip":"10.0.0.8","timestamp":9999}]`},
	}
	got := shapingPlans(map[string]int{"anna": 20}, ips, map[string]int{"self": 0, "de": 3}, now)
	want := map[int]shaper.Plan{
		0: {Limits: []shaper.Limit{{IP: "10.0.0.2", Mbps: 20}}},
		3: {Limits: []shaper.Limit{{IP: "10.0.0.9", Mbps: 20}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plans = %+v, want %+v", got, want)
	}
}

func TestSetClientSpeed(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	s := &SpeedService{}
	for _, bad := range []struct {
		req  ClientSpeedRequest
		want string
	}{
		{ClientSpeedRequest{Email: " "}, "pick a user"},
		{ClientSpeedRequest{Email: "anna", MinMbps: -1}, "speeds cannot be negative"},
		{ClientSpeedRequest{Email: "anna", MinMbps: 50, MaxMbps: 20}, "the minimum speed cannot be above the maximum"},
	} {
		if err := s.SetClient(&bad.req); err == nil || strings.TrimSpace(err.Error()) != bad.want {
			t.Fatalf("%+v: err = %v, want %q", bad.req, err, bad.want)
		}
	}
	if err := s.SetClient(&ClientSpeedRequest{Email: "anna", MinMbps: 20}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClient(&ClientSpeedRequest{Email: " anna ", MinMbps: 10, MaxMbps: 30}); err != nil {
		t.Fatal(err)
	}
	var rows []model.ClientSpeed
	database.GetDB().Find(&rows)
	if len(rows) != 1 || rows[0].Email != "anna" || rows[0].MinMbps != 10 || rows[0].MaxMbps != 30 {
		t.Fatalf("rows after update = %+v", rows)
	}
	if err := s.SetClient(&ClientSpeedRequest{Email: "anna"}); err != nil {
		t.Fatal(err)
	}
	var left int64
	database.GetDB().Model(&model.ClientSpeed{}).Count(&left)
	if left != 0 {
		t.Fatalf("both speeds 0 must remove the row, %d left", left)
	}
}
