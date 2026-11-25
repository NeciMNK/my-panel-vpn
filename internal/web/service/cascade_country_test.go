package service

import (
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestCountryCascadeResolvesEnabledNodesOfThatCountry(t *testing.T) {
	initJoinDB(t)
	db := database.GetDB()
	for _, n := range []*model.Node{
		{Id: 1, Name: "de-1", Address: "1.1.1.1", Port: 1, Enable: true, Country: "DE"},
		{Id: 2, Name: "de-2", Address: "2.2.2.2", Port: 1, Enable: true, Country: "DE"},
		{Id: 3, Name: "de-off", Address: "3.3.3.3", Port: 1, Enable: false, Country: "DE"},
		{Id: 4, Name: "nl-1", Address: "4.4.4.4", Port: 1, Enable: true, Country: "NL"},
	} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	// gorm skips a false bool on create; force the disabled row.
	db.Model(model.Node{}).Where("id = 3").Update("enable", false)

	req := &CascadeRequest{EntryNodeId: 2, ExitCountry: " de ", ExitNodeIds: []int{4}}
	if err := (&CascadeService{}).validate(req); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if req.ExitCountry != "DE" || !slices.Equal(req.ExitNodeIds, []int{1}) {
		t.Fatalf("resolved %q %v, want DE [1] (entry and disabled nodes excluded, picked exits ignored)", req.ExitCountry, req.ExitNodeIds)
	}

	if err := (&CascadeService{}).validate(&CascadeRequest{ExitCountry: "Germany"}); err == nil {
		t.Fatal("a non-ISO country was accepted")
	}
}

func TestNodeCountryIsNormalised(t *testing.T) {
	n := &model.Node{Name: "x", Address: "5.5.5.5", Port: 1, Country: " nl "}
	if err := (&NodeService{}).normalize(n); err != nil || n.Country != "NL" {
		t.Fatalf("country = %q, err = %v", n.Country, err)
	}
	n.Country = "NLD"
	if err := (&NodeService{}).normalize(n); err == nil {
		t.Fatal("three-letter code accepted")
	}
}

func TestForeignCascadeResolvesEveryEnabledNodeOutsideEntryCountry(t *testing.T) {
	initJoinDB(t)
	db := database.GetDB()
	for _, n := range []*model.Node{
		{Id: 1, Name: "ru-entry", Address: "1.1.1.1", Port: 1, Enable: true, Country: "RU"},
		{Id: 2, Name: "ru-2", Address: "2.2.2.2", Port: 1, Enable: true, Country: "RU"},
		{Id: 3, Name: "de-1", Address: "3.3.3.3", Port: 1, Enable: true, Country: "DE"},
		{Id: 4, Name: "nl-off", Address: "4.4.4.4", Port: 1, Enable: true, Country: "NL"},
		{Id: 5, Name: "no-country", Address: "5.5.5.5", Port: 1, Enable: true},
		{Id: 6, Name: "fi-1", Address: "6.6.6.6", Port: 1, Enable: true, Country: "FI"},
	} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	db.Model(model.Node{}).Where("id = 4").Update("enable", false)

	for _, tc := range []struct {
		name  string
		entry int
		want  []int
	}{
		{"node entry uses its own country", 1, []int{3, 6}},
		{"panel entry counts as Russia, like the split", 0, []int{3, 6}},
		{"a foreign entry treats Russia as abroad", 3, []int{1, 2, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &CascadeRequest{EntryNodeId: tc.entry, ExitCountry: " foreign ", ExitNodeIds: []int{5}}
			if err := (&CascadeService{}).validate(req); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if req.ExitCountry != CascadeExitForeign || !slices.Equal(req.ExitNodeIds, tc.want) {
				t.Fatalf("resolved %q %v, want %q %v", req.ExitCountry, req.ExitNodeIds, CascadeExitForeign, tc.want)
			}
		})
	}

	db.Model(model.Node{}).Where("id = 4").Update("enable", true)
	c := &model.Cascade{Name: "ru-abroad", EntryNodeId: 1, ExitCountry: CascadeExitForeign, ExitNodeIds: []int{3, 6}}
	if err := db.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	exits, err := cascadeExits(c.ExitCountry, c.EntryNodeId)
	if err != nil || !slices.Equal(exits, []int{3, 4, 6}) {
		t.Fatalf("re-resolved %v, %v; want the re-enabled NL node to join: [3 4 6]", exits, err)
	}
}
