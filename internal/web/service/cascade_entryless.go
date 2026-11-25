package service

import (
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// EntrylessPin is how a client's own auto-select (no entry server) honours
// their exit pin: NodeId carries AIDomains (and leads with AllTraffic), or AIBlock is cut.
type EntrylessPin struct {
	NodeId     int
	AIDomains  []string
	AllTraffic bool
	AIBlock    []string
	AllOff     bool
}

// EntrylessPinOf reads the emails' first pin on one of the balancer's servers;
// while it is down the bot outage choice decides, as on a cascade entry.
func (s *CascadeService) EntrylessPinOf(emails []string, memberNodes []int) (EntrylessPin, error) {
	db := database.GetDB()
	var pins []model.CascadeFixedExit
	if err := db.Where("email IN ? AND node_id IN ?", emails, memberNodes).Order("id asc").
		Limit(1).Find(&pins).Error; err != nil || len(pins) == 0 {
		return EntrylessPin{}, err
	}
	pin := pins[0]
	var nodes []*model.Node
	if err := db.Select("id, country, enable, status").Where("id IN ?", memberNodes).
		Order("id asc").Find(&nodes).Error; err != nil {
		return EntrylessPin{}, err
	}
	w := newExitWorld(nodes, nil)
	sites := (&SettingService{}).GetTgAIExitDomains()
	// As on a cascade entry, a pin in the user's AI country carries AI only unless all-traffic is set.
	var ai model.CascadeAICountry
	if err := db.Where("email = ?", pin.Email).Limit(1).Find(&ai).Error; err != nil {
		return EntrylessPin{}, err
	}
	allTraffic := ai.Email == "" || ai.AllTraffic || ai.Country != w.countryOf[pin.NodeId]
	if !w.down(pin.NodeId) {
		return EntrylessPin{NodeId: pin.NodeId, AIDomains: sites, AllTraffic: allTraffic}, nil
	}
	choice := s.AIOutageChoiceOf([]string{pin.Email})
	switch choice.Action {
	case AIOutageOff:
		return EntrylessPin{AllOff: true}, nil
	case AIOutageOther:
		for _, country := range []string{w.countryOf[pin.NodeId], strings.ToUpper(choice.Country)} {
			for _, n := range nodes {
				if country != "" && n.Country == country && !w.down(n.Id) {
					return EntrylessPin{NodeId: n.Id, AIDomains: sites, AllTraffic: allTraffic}, nil
				}
			}
		}
	case AIOutageBlock:
		return EntrylessPin{AIBlock: aiServiceDomains(sites, choice.Services)}, nil
	}
	return EntrylessPin{AIBlock: slices.Clone(sites)}, nil
}
