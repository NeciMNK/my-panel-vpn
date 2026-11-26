package sub

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedSubNode(t *testing.T, name, address, status string) int {
	t.Helper()
	n := &model.Node{Name: name, Scheme: "https", Address: address, Port: 2053, BasePath: "/", Enable: true, Status: status}
	if err := database.GetDB().Create(n).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	return n.Id
}

func placeOnNode(t *testing.T, ib *model.Inbound, nodeID int) {
	t.Helper()
	if err := database.GetDB().Model(ib).Update("node_id", nodeID).Error; err != nil {
		t.Fatalf("move %s to node %d: %v", ib.Tag, nodeID, err)
	}
}

// A node nobody can reach must leave the subscription; one only the master lost
// still serves users, and a sub is never emptied by every node being down at once.
func TestSubscriptionDropsDeadNodeLinks(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     []string
	}{
		{"offline node is dropped", []string{model.NodeStatusOnline, model.NodeStatusOffline}, []string{"nl.example.com"}},
		{"node only the master lost stays", []string{model.NodeStatusOnline, model.NodeStatusIsolated}, []string{"nl.example.com", "de.example.com"}},
		{"every node offline keeps them all", []string{model.NodeStatusOffline, model.NodeStatusOffline}, []string{"nl.example.com", "de.example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedSubDB(t)
			stream := `{"network":"tcp","security":"none"}`
			nl := seedSubInbound(t, "s1", "nl", 4431, 0, stream)
			de := seedSubInbound(t, "s1", "de", 4432, 1, stream)
			placeOnNode(t, nl, seedSubNode(t, "nl", "nl.example.com", tc.statuses[0]))
			placeOnNode(t, de, seedSubNode(t, "de", "de.example.com", tc.statuses[1]))

			links, _, _, _, err := NewSubService("").GetSubs("s1", "sub.example.com")
			if err != nil {
				t.Fatalf("GetSubs: %v", err)
			}
			var hosts []string
			for _, l := range links {
				for _, h := range []string{"nl.example.com", "de.example.com"} {
					if strings.Contains(l, "@"+h+":") {
						hosts = append(hosts, h)
					}
				}
			}
			if strings.Join(hosts, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("subscription hosts = %v, want %v\nlinks: %v", hosts, tc.want, links)
			}
		})
	}
}
