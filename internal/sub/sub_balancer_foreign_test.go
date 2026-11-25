package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// An auto-select without an entry server: the client's own balancer holds its
// inbounds on foreign nodes, so a Russian node never becomes an exit.
func TestSubJson_ForeignBalancerTakesOnlyForeignNodeInbounds(t *testing.T) {
	seedSubDB(t)
	db := database.GetDB()
	nodes := []*model.Node{
		{Name: "ru", Address: "10.0.0.1", Port: 1, Enable: true, Country: "RU"},
		{Name: "de", Address: "10.0.0.2", Port: 1, Enable: true, Country: "DE"},
		{Name: "nl", Address: "10.0.0.3", Port: 1, Enable: true, Country: "NL"},
	}
	for _, n := range nodes {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	stream := `{"network":"tcp","security":"none"}`
	for i, tag := range []string{"ru-in", "de-in", "nl-in"} {
		ib := seedSubInbound(t, "s1", tag, 4801+i, i+1, stream)
		if err := db.Model(ib).Update("node_id", nodes[i].Id).Error; err != nil {
			t.Fatal(err)
		}
	}
	panelIn := seedSubInbound(t, "s1", "panel-in", 4810, 4, stream)

	for _, tc := range []struct {
		name string
		bal  model.SubBalancer
		want string
	}{
		{"all foreign servers", model.SubBalancer{Remark: "abroad", ExitForeign: true}, "4802,4803"},
		{"picked foreign server plus an inbound", model.SubBalancer{Remark: "picked", ExitNodeIds: []int{nodes[2].Id}, InboundIds: []int{panelIn.Id}}, "4803,4810"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db.Where("1 = 1").Delete(&model.SubBalancer{})
			tc.bal.Strategy, tc.bal.SortOrder, tc.bal.Enabled = "leastPing", 1, true
			seedSubBalancer(t, &tc.bal)
			js := NewSubJsonService("", "", "", "", NewSubService(""))
			out, _, err := js.GetJson("s1", "req.example.com", true)
			if err != nil {
				t.Fatalf("GetJson: %v", err)
			}
			doc := findDocByRemarks(parseSubJsonDocs(t, out), tc.bal.Remark)
			if doc == nil {
				t.Fatalf("balancer doc %q missing:\n%s", tc.bal.Remark, out)
			}
			var members []string
			for _, ob := range doc["outbounds"].([]any) {
				m := ob.(map[string]any)
				if !strings.HasPrefix(m["tag"].(string), "bal-") {
					continue
				}
				raw, _ := json.Marshal(m)
				for _, port := range []string{"4801", "4802", "4803", "4810"} {
					if strings.Contains(string(raw), `"port":`+port) {
						members = append(members, port)
					}
				}
			}
			if strings.Join(members, ",") != tc.want {
				t.Fatalf("balancer members by port = %v, want %s", members, tc.want)
			}
		})
	}
}
