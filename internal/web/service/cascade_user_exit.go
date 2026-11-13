package service

import (
	"context"
	"math"
	"slices"
	"sort"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// UserExit is one exit server a user's traffic can leave from, as the user
// is offered it when choosing a permanent exit IP.
type UserExit struct {
	NodeId  int
	Name    string
	Address string
	// Saturated is true when the server's known capacity leaves no speed for
	// one more user (below their minimum, or nothing at all).
	Saturated bool
	FreeMbps  float64
	Capacity  bool
	Online    int
	Pinned    bool
}

// UserExitGroup is every healthy exit of one country the user can reach.
type UserExitGroup struct {
	Country string
	Exits   []UserExit
}

func (g UserExitGroup) nodeIDs() []int {
	ids := make([]int, len(g.Exits))
	for i, e := range g.Exits {
		ids[i] = e.NodeId
	}
	return ids
}

// Pinned is the group's exit the user is pinned to.
func (g UserExitGroup) Pinned() (UserExit, bool) {
	i := slices.IndexFunc(g.Exits, func(e UserExit) bool { return e.Pinned })
	if i < 0 {
		return UserExit{}, false
	}
	return g.Exits[i], true
}

// Open lists the exits that may still be offered to the user.
func (g UserExitGroup) Open() []UserExit {
	var out []UserExit
	for _, e := range g.Exits {
		if !e.Saturated {
			out = append(out, e)
		}
	}
	return out
}

// Best is the open exit a new user does best on: the most free speed, and
// among servers without a known capacity the one with the fewest users online.
func (g UserExitGroup) Best() (UserExit, bool) {
	open := g.Open()
	if len(open) == 0 {
		return UserExit{}, false
	}
	free := func(e UserExit) float64 {
		if e.Capacity {
			return e.FreeMbps
		}
		return math.Inf(1)
	}
	sort.SliceStable(open, func(i, j int) bool {
		if free(open[i]) != free(open[j]) {
			return free(open[i]) > free(open[j])
		}
		return open[i].Online < open[j].Online
	})
	return open[0], true
}

// UserExitGroups lists, per country, the healthy exits of every enabled
// cascade the users' inbounds enter; several emails are one person's subscriptions.
func (s *CascadeService) UserExitGroups(emails []string) ([]UserExitGroup, error) {
	exits, err := userCascadeExits(emails)
	if err != nil || len(exits) == 0 {
		return nil, err
	}
	world, err := loadSpeedWorld()
	if err != nil {
		return nil, err
	}
	loads, err := world.loads()
	if err != nil {
		return nil, err
	}
	mins, err := minSpeeds()
	if err != nil {
		return nil, err
	}
	need := 0
	for _, e := range emails {
		need = max(need, mins[e])
	}
	var pins []model.CascadeFixedExit
	if err := database.GetDB().Where("email IN ?", emails).Find(&pins).Error; err != nil {
		return nil, err
	}
	nodes := map[int]*model.Node{}
	for _, n := range world.nodes {
		nodes[n.Id] = n
	}
	byCountry := map[string][]UserExit{}
	for _, id := range exits {
		exit := UserExit{NodeId: id, Online: len(world.online(id))}
		country := ""
		if id != 0 {
			n := nodes[id]
			if n == nil || !n.Enable || n.Status != "online" {
				continue
			}
			exit.Name, exit.Address, country = n.Name, n.Address, n.Country
		}
		if l, known := loads[id]; known {
			exit.Capacity, exit.FreeMbps = true, l.freeForNewUser()
			exit.Saturated = exit.FreeMbps <= 0 || exit.FreeMbps < float64(need)
		}
		exit.Pinned = slices.ContainsFunc(pins, func(p model.CascadeFixedExit) bool { return p.NodeId == id })
		byCountry[country] = append(byCountry[country], exit)
	}
	out := make([]UserExitGroup, 0, len(byCountry))
	for country, list := range byCountry {
		out = append(out, UserExitGroup{Country: country, Exits: list})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Country < out[j].Country })
	return out, nil
}

// userCascadeExits are the exit node ids of the enabled cascades whose entry
// serves one of the emails' inbounds, sorted.
func userCascadeExits(emails []string) ([]int, error) {
	db := database.GetDB()
	var inbounds []model.Inbound
	err := db.Model(&model.Inbound{}).Select("inbounds.id, inbounds.tag, inbounds.node_id").
		Joins("JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email IN ?", emails).Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	var cascades []model.Cascade
	if err := db.Where("enable = ?", true).Find(&cascades).Error; err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []int
	for _, c := range cascades {
		if !slices.ContainsFunc(inbounds, func(ib model.Inbound) bool { return cascadeEnters(&c, &ib) }) {
			continue
		}
		for _, l := range c.Links {
			if !seen[l.NodeId] {
				seen[l.NodeId] = true
				out = append(out, l.NodeId)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// cascadeEnters reports whether traffic of inbound ib leaves through cascade c.
func cascadeEnters(c *model.Cascade, ib *model.Inbound) bool {
	return CascadeRoute(c, ib) != ""
}

// CascadeRoute is how cascade c carries inbound ib's traffic: CascadeModeSplit,
// CascadeModeRelay, or "" when ib does not enter c.
func CascadeRoute(c *model.Cascade, ib *model.Inbound) string {
	entry := 0
	if ib.NodeID != nil {
		entry = *ib.NodeID
	}
	if entry != c.EntryNodeId {
		return ""
	}
	mode := cascadeMode(c)
	switch {
	case mode == CascadeModeBoth && slices.Contains(c.RelayInboundTags, ib.Tag):
		return CascadeModeRelay
	case len(c.EntryInboundTags) > 0 && !slices.Contains(c.EntryInboundTags, ib.Tag):
		return ""
	case mode == CascadeModeRelay:
		return CascadeModeRelay
	}
	return CascadeModeSplit
}

// PinUserExit pins every one of the emails to nodeID in place of their pin in
// the same group; a saturated server is refused unless it is the only one.
func (s *CascadeService) PinUserExit(ctx context.Context, emails []string, nodeID int) (UserExit, error) {
	groups, err := s.UserExitGroups(emails)
	if err != nil {
		return UserExit{}, err
	}
	for _, g := range groups {
		i := slices.IndexFunc(g.Exits, func(e UserExit) bool { return e.NodeId == nodeID })
		if i < 0 {
			continue
		}
		exit := g.Exits[i]
		if exit.Saturated && len(g.Exits) > 1 && !exit.Pinned {
			return UserExit{}, common.NewErrorf("server %d is saturated", nodeID)
		}
		group := g.nodeIDs()
		for _, email := range emails {
			if err := replaceFixedExit(email, nodeID, func(id int) bool { return slices.Contains(group, id) }); err != nil {
				return UserExit{}, err
			}
		}
		s.StickCascadeUsers(ctx)
		exit.Pinned = true
		return exit, nil
	}
	return UserExit{}, common.NewErrorf("server %d is not one of your exits", nodeID)
}

// PinSoleExits pins the emails in every country that has a single exit and no
// pin yet, and returns those exits: there is nothing to choose there.
func (s *CascadeService) PinSoleExits(ctx context.Context, emails []string) ([]UserExit, error) {
	groups, err := s.UserExitGroups(emails)
	if err != nil {
		return nil, err
	}
	var pinned []UserExit
	for _, g := range groups {
		if len(g.Exits) != 1 || g.Exits[0].Pinned {
			continue
		}
		for _, email := range emails {
			if err := replaceFixedExit(email, g.Exits[0].NodeId, func(int) bool { return false }); err != nil {
				return pinned, err
			}
		}
		pinned = append(pinned, g.Exits[0])
	}
	if len(pinned) > 0 {
		s.StickCascadeUsers(ctx)
	}
	return pinned, nil
}

// replaceFixedExit pins email to nodeID, dropping their pins on the other
// nodes sameGroup accepts.
func replaceFixedExit(email string, nodeID int, sameGroup func(int) bool) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		var mine []model.CascadeFixedExit
		if err := tx.Where("email = ?", email).Find(&mine).Error; err != nil {
			return err
		}
		for _, r := range mine {
			if r.NodeId == nodeID {
				return nil
			}
			if sameGroup(r.NodeId) {
				if err := tx.Delete(&model.CascadeFixedExit{}, r.Id).Error; err != nil {
					return err
				}
			}
		}
		return tx.Create(&model.CascadeFixedExit{Email: email, NodeId: nodeID}).Error
	})
}
