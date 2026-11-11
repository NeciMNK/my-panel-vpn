package sub

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const rwAdminSquad = "c0000000-0000-4000-8000-000000000002"

// rwSquadSnapshot extends the base snapshot with an Admin squad, an admin-only
// Reality host and dave, who is in both squads; edit adjusts the hosts.
func rwSquadSnapshot(t *testing.T, edit func(hosts []map[string]any)) *service.RemnawaveSnapshot {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(rwSnapshotJSON), &raw); err != nil {
		t.Fatal(err)
	}
	squads := raw["internalSquads"].([]any)
	raw["internalSquads"] = append(squads, map[string]any{
		"uuid": rwAdminSquad, "name": "Admin-Squad",
		"inbounds": []any{map[string]any{"uuid": "b0000000-0000-4000-8000-000000000001"}, map[string]any{"uuid": "b0000000-0000-4000-8000-000000000002"}},
	})
	hostList := raw["hosts"].([]any)
	adminHost := map[string]any{}
	for k, v := range hostList[0].(map[string]any) {
		adminHost[k] = v
	}
	adminHost["uuid"], adminHost["viewPosition"], adminHost["remark"] = "e0000000-0000-4000-8000-000000000009", 3, "🇩🇪 Admin"
	adminHost["internalSquads"] = map[string]any{"mode": "ALLOW_ONLY", "squads": []any{rwAdminSquad}}
	hostList = append(hostList, adminHost)
	hosts := make([]map[string]any, len(hostList))
	for i, h := range hostList {
		hosts[i] = h.(map[string]any)
		if hosts[i]["internalSquads"] == nil {
			hosts[i]["internalSquads"] = map[string]any{"mode": "EXCLUDE", "squads": []any{}}
		}
	}
	if edit != nil {
		edit(hosts)
	}
	raw["hosts"] = hosts
	users := raw["users"].([]any)
	dave := map[string]any{}
	for k, v := range users[0].(map[string]any) {
		dave[k] = v
	}
	dave["id"], dave["username"], dave["shortUuid"] = 3, "dave", "Dv7aVe0000000003"
	dave["vlessUuid"], dave["trojanPassword"] = "8c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f", "tr0janDave"
	dave["subscriptionUrl"] = "https://sub.example.com/Dv7aVe0000000003"
	dave["activeInternalSquads"] = []any{
		map[string]any{"uuid": "c0000000-0000-4000-8000-000000000001"},
		map[string]any{"uuid": rwAdminSquad},
	}
	raw["users"] = append(users, dave)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := service.ParseRemnawaveSnapshot(b)
	if err != nil {
		t.Fatalf("ParseRemnawaveSnapshot: %v", err)
	}
	return snap
}

func subRemarks(t *testing.T, subID string) []string {
	t.Helper()
	links, _, _, _, err := NewSubService("{{HOST}}").GetSubs(subID, "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs(%s): %v", subID, err)
	}
	var remarks []string
	for _, l := range strings.Split(strings.Join(links, "\n"), "\n") {
		u, err := url.Parse(l)
		if err != nil {
			t.Fatalf("parse %q: %v", l, err)
		}
		remarks = append(remarks, u.Fragment)
	}
	slices.Sort(remarks)
	return remarks
}

// Remnawave shows a host only to the squads its filter lets through; after the
// import every user must get exactly the configs Remnawave gave them.
func TestRemnawaveImport_HostSquadFilters(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(hosts []map[string]any)
		alice []string
		dave  []string
	}{
		{
			name:  "allow-only host hidden from other squads",
			alice: []string{"Trojan DE", "🇩🇪 Germany"},
			dave:  []string{"Trojan DE", "🇩🇪 Admin", "🇩🇪 Germany"},
		},
		{
			name: "exclude host hidden from listed squad",
			edit: func(hosts []map[string]any) {
				hosts[3]["internalSquads"] = map[string]any{"mode": "EXCLUDE", "squads": []any{"c0000000-0000-4000-8000-000000000001"}}
				hosts[0]["internalSquads"] = map[string]any{"mode": "EXCLUDE", "squads": []any{rwAdminSquad}}
			},
			alice: []string{"Trojan DE", "🇩🇪 Germany"},
			dave:  []string{"Trojan DE"},
		},
		{
			name: "every host of an inbound hidden",
			edit: func(hosts []map[string]any) {
				hosts[2]["internalSquads"] = map[string]any{"mode": "ALLOW_ONLY", "squads": []any{rwAdminSquad}}
			},
			alice: []string{"🇩🇪 Germany"},
			dave:  []string{"Trojan DE", "🇩🇪 Admin", "🇩🇪 Germany"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedSubDB(t)
			targets := map[string]string{rwGermany: service.MigrationTargetLocal, rwNetherlands: service.MigrationTargetSkip}
			res, err := (&service.MigrationService{}).ApplyRemnawave(rwSquadSnapshot(t, tc.edit), targets, false, 1)
			if err != nil {
				t.Fatalf("ApplyRemnawave: %v", err)
			}
			if res.Clients.Created != 3 || len(res.HostErrors) != 0 {
				t.Fatalf("result = %+v", res)
			}
			if got := subRemarks(t, "Zx3pQ9tLmN2kVb7c"); !slices.Equal(got, tc.alice) {
				t.Errorf("alice sees %q, want %q", got, tc.alice)
			}
			if got := subRemarks(t, "Dv7aVe0000000003"); !slices.Equal(got, tc.dave) {
				t.Errorf("dave sees %q, want %q", got, tc.dave)
			}
		})
	}
}
