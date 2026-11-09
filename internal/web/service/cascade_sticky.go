package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// CascadePinHold is how long a user who went offline keeps their exit, so an
// automatic reconnect comes back out of the same IP.
const CascadePinHold = 15 * time.Minute

func cascadePinTag(cascadeID, nodeID int) string {
	return fmt.Sprintf("%spin-n%d", cascadePrefix(cascadeID), nodeID)
}

// stickyView is what one pin pass knows about the world.
type stickyView struct {
	Now             int64
	Exits           []int
	Healthy         map[int]bool
	OnlineHere      map[string]bool
	OnlineElsewhere map[string]bool
}

// nextCascadePins returns the pins after one pass and whether any user moved,
// which is when the entry's routing must be rewritten.
func nextCascadePins(pins []model.CascadePin, v stickyView) ([]model.CascadePin, bool) {
	hold := int64(CascadePinHold / time.Second)
	usable := map[int]bool{}
	for _, id := range v.Exits {
		if v.Healthy[id] {
			usable[id] = true
		}
	}
	changed := false
	kept := make([]model.CascadePin, 0, len(pins))
	pinned := map[string]bool{}
	load := map[int]int{}
	for _, p := range pins {
		here := v.OnlineHere[p.Email]
		switch {
		case !usable[p.NodeId]:
		case !here && v.OnlineElsewhere[p.Email]:
			// The user picked another server themselves: no hold applies.
		case !here && v.Now-p.LastSeen > hold:
		default:
			if here {
				p.LastSeen = v.Now
			}
			kept = append(kept, p)
			pinned[p.Email] = true
			load[p.NodeId]++
			continue
		}
		changed = true
	}
	if len(usable) == 0 {
		return kept, changed
	}
	newcomers := make([]string, 0)
	for email := range v.OnlineHere {
		if !pinned[email] {
			newcomers = append(newcomers, email)
		}
	}
	sort.Strings(newcomers)
	for _, email := range newcomers {
		best := -1
		for _, id := range v.Exits {
			if usable[id] && (best < 0 || load[id] < load[best]) {
				best = id
			}
		}
		kept = append(kept, model.CascadePin{Email: email, NodeId: best, LastSeen: v.Now})
		load[best]++
		changed = true
	}
	return kept, changed
}

// cascadePinRules routes each pinned user of the cascade to their exit; they
// sit right before the cascade's catch-all, so the user's own rules still win.
func cascadePinRules(c *model.Cascade, pins []model.CascadePin) []map[string]any {
	byNode := map[int][]string{}
	for _, p := range pins {
		byNode[p.NodeId] = append(byNode[p.NodeId], p.Email)
	}
	var rules []map[string]any
	for _, l := range c.Links {
		emails := byNode[l.NodeId]
		if len(emails) == 0 {
			continue
		}
		slices.Sort(emails)
		rule := map[string]any{
			"type":        "field",
			"ruleTag":     cascadePinTag(c.Id, l.NodeId),
			"user":        emails,
			"outboundTag": cascadeLinkTag(c.Id, l.NodeId),
		}
		if len(c.EntryInboundTags) > 0 {
			rule["inboundTag"] = c.EntryInboundTags
		}
		rules = append(rules, rule)
	}
	return rules
}

func loadCascadePins(cascadeID int) ([]model.CascadePin, error) {
	var pins []model.CascadePin
	err := database.GetDB().Where("cascade_id = ?", cascadeID).Order("id asc").Find(&pins).Error
	return pins, err
}

// saveCascadePins deletes released pins before inserting new ones: a user
// moved off a dead exit comes back as a new row with the same email.
func saveCascadePins(cascadeID int, old, next []model.CascadePin) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		kept := map[int]model.CascadePin{}
		for _, p := range next {
			if p.Id != 0 {
				kept[p.Id] = p
			}
		}
		for _, p := range old {
			if _, ok := kept[p.Id]; !ok {
				if err := tx.Delete(&model.CascadePin{}, p.Id).Error; err != nil {
					return err
				}
			}
		}
		before := make(map[int]model.CascadePin, len(old))
		for _, p := range old {
			before[p.Id] = p
		}
		for i := range next {
			next[i].CascadeId = cascadeID
			if next[i].Id != 0 && before[next[i].Id] == next[i] {
				continue
			}
			if err := tx.Save(&next[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// cascadeOnline splits who is online into "on this entry" and "on any other
// server", by the panel GUIDs the online tracker keys its sets with.
func cascadeOnline(entryNodeID int, byGuid map[string][]string, guidOf map[int]string) (here, elsewhere map[string]bool) {
	here, elsewhere = map[string]bool{}, map[string]bool{}
	entryGuid := guidOf[entryNodeID]
	for guid, emails := range byGuid {
		target := elsewhere
		if guid == entryGuid {
			target = here
		}
		for _, e := range emails {
			if !strings.HasPrefix(e, managedTagPrefix) {
				target[e] = true
			}
		}
	}
	return here, elsewhere
}

// StickCascadeUsers keeps every online user of a multi-exit cascade on one exit
// and rewrites the entry's pin rules (hot-applied, no reconnects) when they move.
func (s *CascadeService) StickCascadeUsers(ctx context.Context) {
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	var rows []*model.Cascade
	if err := database.GetDB().Where("enable = ?", true).Find(&rows).Error; err != nil {
		logger.Warning("cascade sticky: list cascades:", err)
		return
	}
	var nodes []*model.Node
	if err := database.GetDB().Find(&nodes).Error; err != nil {
		logger.Warning("cascade sticky: list nodes:", err)
		return
	}
	selfGuid, _ := (&SettingService{}).GetPanelGuid()
	ambiguous := ambiguousNodeGuids(nodes, selfGuid)
	guidOf := map[int]string{0: selfGuid}
	healthy := map[int]bool{0: true}
	for _, n := range nodes {
		guidOf[n.Id] = effectiveNodeGuid(n, ambiguous)
		healthy[n.Id] = n.Enable && n.Status == "online"
	}
	byGuid := (&InboundService{}).GetOnlineClientsByGuid()
	now := time.Now().Unix()

	for _, c := range rows {
		old, err := loadCascadePins(c.Id)
		if err != nil {
			logger.Warning("cascade", c.Id, ": load pins:", err)
			continue
		}
		if len(c.Links) < 2 && len(old) == 0 {
			continue
		}
		view := stickyView{Now: now, Healthy: healthy}
		if len(c.Links) >= 2 {
			for _, l := range c.Links {
				view.Exits = append(view.Exits, l.NodeId)
			}
			view.OnlineHere, view.OnlineElsewhere = cascadeOnline(c.EntryNodeId, byGuid, guidOf)
		}
		next, changed := nextCascadePins(old, view)
		if err := saveCascadePins(c.Id, old, next); err != nil {
			logger.Warning("cascade", c.Id, ": save pins:", err)
			continue
		}
		if !changed {
			continue
		}
		if err := s.rewriteEntry(ctx, c.EntryNodeId, c, true); err != nil {
			logger.Warning("cascade", c.Id, ": apply pins on", nodeLabel(c.EntryNodeId), ":", err)
		}
	}
}
