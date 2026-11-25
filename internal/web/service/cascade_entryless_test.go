package service

import (
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A client's own auto-select (no entry server) honours their exit pin: the
// pinned server leads while up, and the bot outage choice applies once it is down.
func TestEntrylessPinFollowsPinAndOutageChoice(t *testing.T) {
	initJoinDB(t)
	db := database.GetDB()
	for _, n := range []*model.Node{
		{Id: 2, Name: "de-1", Address: "2.2.2.2", Port: 1, Enable: true, Country: "DE", Status: "online"},
		{Id: 3, Name: "de-2", Address: "3.3.3.3", Port: 1, Enable: true, Country: "DE", Status: "online"},
		{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Country: "NL", Status: "online"},
		{Id: 5, Name: "fi-1", Address: "5.5.5.5", Port: 1, Enable: true, Country: "FI", Status: "online"},
	} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.CascadeFixedExit{Email: "anna", NodeId: 2}).Error; err != nil {
		t.Fatal(err)
	}
	sites := (&SettingService{}).GetTgAIExitDomains()
	members := []int{2, 3, 4}
	svc := &CascadeService{}

	check := func(name string, emails []string, want EntrylessPin) {
		t.Helper()
		got, err := svc.EntrylessPinOf(emails, members)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v, want %+v", name, got, want)
		}
	}

	check("no pin", []string{"bob"}, EntrylessPin{})
	db.Create(&model.CascadeFixedExit{Email: "dina", NodeId: 4})
	db.Create(&model.CascadeAICountry{Email: "dina", Country: "NL"})
	check("AI-only pin leaves the rest to auto-select", []string{"dina"}, EntrylessPin{NodeId: 4, AIDomains: sites})
	check("pin up", []string{"anna"}, EntrylessPin{NodeId: 2, AIDomains: sites, AllTraffic: true})

	setNodeStatus(t, 2, "offline")
	check("pin down, no choice blocks AI", []string{"anna"}, EntrylessPin{AIBlock: sites})

	db.Create(&model.AIOutageChoice{Email: "anna", Action: AIOutageOther})
	check("pin down, other picks a live server of its country", []string{"anna"}, EntrylessPin{NodeId: 3, AIDomains: sites, AllTraffic: true})

	setNodeStatus(t, 3, "offline")
	db.Model(&model.AIOutageChoice{}).Where("email = ?", "anna").Update("country", "NL")
	check("other falls back to the chosen country", []string{"anna"}, EntrylessPin{NodeId: 4, AIDomains: sites, AllTraffic: true})

	db.Model(&model.AIOutageChoice{}).Where("email = ?", "anna").Updates(map[string]any{"action": AIOutageOff})
	check("off cuts everything", []string{"anna"}, EntrylessPin{AllOff: true})

	setNodeStatus(t, 2, "online")
	db.Create(&model.CascadeFixedExit{Email: "carl", NodeId: 5})
	check("a pin outside the balancer's servers is ignored", []string{"carl"}, EntrylessPin{})
}
