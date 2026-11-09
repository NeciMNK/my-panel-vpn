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
