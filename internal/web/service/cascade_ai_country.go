package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// SetAICountry makes country the only one the emails use AI from and pins them
// there (the best open server unless already pinned); "" goes back to every country.
func (s *CascadeService) SetAICountry(ctx context.Context, emails []string, country string) (UserExit, bool, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		err := database.GetDB().Where("email IN ?", emails).Delete(&model.CascadeAICountry{}).Error
		if err == nil {
			s.StickCascadeUsers(ctx)
		}
		return UserExit{}, false, err
	}
	groups, err := s.UserExitGroups(emails)
	if err != nil {
		return UserExit{}, false, err
	}
	group, found := UserExitGroup{}, false
	for _, g := range groups {
		if g.Country == country {
			group, found = g, true
		}
	}
	if !found {
		return UserExit{}, false, common.NewErrorf("none of your exits is in %s", country)
	}
	_, all := s.AIMode(emails)
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("email IN ?", emails).Delete(&model.CascadeAICountry{}).Error; err != nil {
			return err
		}
		for _, email := range emails {
			if err := tx.Create(&model.CascadeAICountry{Email: email, Country: country, AllTraffic: all}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return UserExit{}, false, err
	}
	exit, pinned := group.Pinned()
	if !pinned {
		if exit, pinned = group.Best(); pinned {
			ids := group.nodeIDs()
			for _, email := range emails {
				if err := replaceFixedExit(email, exit.NodeId, func(id int) bool { return slices.Contains(ids, id) }); err != nil {
					return UserExit{}, false, err
				}
			}
			exit.Pinned = true
		}
	}
	s.StickCascadeUsers(ctx)
	return exit, pinned, nil
}

// AICountry is the country the emails use AI from, "" when any.
func (s *CascadeService) AICountry(emails []string) string {
	country, _ := s.AIMode(emails)
	return country
}

// AIMode is the emails' AI country and whether all their traffic, not only
// AI services, leaves through the pin there.
func (s *CascadeService) AIMode(emails []string) (string, bool) {
	var row model.CascadeAICountry
	if err := database.GetDB().Where("email IN ?", emails).Order("id desc").Limit(1).Find(&row).Error; err != nil {
		return "", false
	}
	return row.Country, row.AllTraffic
}

// SetAIAllTraffic lets the user choose what their AI-country pin carries: all
// their traffic, or AI services only while the rest is chosen automatically.
func (s *CascadeService) SetAIAllTraffic(ctx context.Context, emails []string, all bool) error {
	res := database.GetDB().Model(&model.CascadeAICountry{}).Where("email IN ?", emails).Update("all_traffic", all)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return common.NewError("pick the country you use AI from first")
	}
	s.StickCascadeUsers(ctx)
	return nil
}

// aiExitPlan is each AI-country user's pin there: Domains carries the AI
// services in Sites only, All carries all their traffic.
type aiExitPlan struct {
	Domains map[string]int
	All     map[string]int
	Sites   []string
	// Fixed is the pin rows with every dead pin moved to the user's stand-in.
	Fixed   []model.CascadeFixedExit
	Outages []AIPinOutage
	// domainRows are the pin rows that carry AI domains only, dead or alive.
	domainRows map[int]bool
}

// isDomainPin reports whether a fixed exit only carries the user's AI domains.
func (p aiExitPlan) isDomainPin(r model.CascadeFixedExit) bool {
	return p.domainRows[r.Id]
}

// exitWorld is what a plan needs to know about the servers.
type exitWorld struct {
	countryOf map[int]string
	healthy   map[int]bool
	cascades  []*model.Cascade
}

func newExitWorld(nodes []*model.Node, enabled []*model.Cascade) exitWorld {
	w := exitWorld{countryOf: map[int]string{}, healthy: map[int]bool{0: true}, cascades: enabled}
	for _, n := range nodes {
		w.countryOf[n.Id] = n.Country
		w.healthy[n.Id] = n.Enable && n.Status == "online"
	}
	return w
}

// down reports a known server that is not serving; a pin on a deleted one is
// no outage, it just never matches a cascade link.
func (w exitWorld) down(id int) bool {
	_, known := w.countryOf[id]
	return known && !w.healthy[id]
}

func aiCountryExits(fixed []model.CascadeFixedExit, w exitWorld) (aiExitPlan, error) {
	plan := aiExitPlan{
		Domains: map[string]int{}, All: map[string]int{}, Sites: (&SettingService{}).GetTgAIExitDomains(),
		Fixed: slices.Clone(fixed), domainRows: map[int]bool{},
	}
	var rows []model.CascadeAICountry
	if err := database.GetDB().Find(&rows).Error; err != nil {
		return plan, err
	}
	choices, err := loadAIOutageChoices()
	if err != nil {
		return plan, err
	}
	want := make(map[string]model.CascadeAICountry, len(rows))
	for _, r := range rows {
		want[r.Email] = r
	}
	for i, f := range fixed {
		if !w.down(f.NodeId) {
			continue
		}
		o := w.outage(f, choices[f.Email], plan.Sites)
		plan.Outages = append(plan.Outages, o)
		if o.Swapped {
			plan.Fixed[i].NodeId = o.SwapNodeId
		}
	}
	for i, f := range plan.Fixed {
		r, ok := want[f.Email]
		if !ok || w.countryOf[fixed[i].NodeId] != r.Country {
			continue
		}
		if !r.AllTraffic {
			plan.domainRows[f.Id] = true
		}
		if w.down(f.NodeId) {
			continue
		}
		if r.AllTraffic {
			plan.All[f.Email] = f.NodeId
		} else {
			plan.Domains[f.Email] = f.NodeId
		}
	}
	return plan, nil
}

// loadAIExitPlan reads the plan for a template rewrite outside the sticky pass.
func loadAIExitPlan() (aiExitPlan, error) {
	var fixed []model.CascadeFixedExit
	if err := database.GetDB().Order("id asc").Find(&fixed).Error; err != nil {
		return aiExitPlan{}, err
	}
	var nodes []*model.Node
	if err := database.GetDB().Select("id, country, enable, status").Find(&nodes).Error; err != nil {
		return aiExitPlan{}, err
	}
	var cascades []*model.Cascade
	if err := database.GetDB().Where("enable = ?", true).Find(&cascades).Error; err != nil {
		return aiExitPlan{}, err
	}
	return aiCountryExits(fixed, newExitWorld(nodes, cascades))
}

// defaultAIServiceDomains are the AI services that ban accounts whose IP keeps
// changing; the admin replaces them with tgAIExitDomains.
var defaultAIServiceDomains = []string{
	"domain:openai.com", "domain:chatgpt.com", "domain:oaistatic.com", "domain:oaiusercontent.com",
	"domain:anthropic.com", "domain:claude.ai", "domain:claude.com", "domain:claudeusercontent.com",
	"domain:gemini.google.com", "domain:aistudio.google.com", "domain:generativelanguage.googleapis.com",
	"domain:notebooklm.google.com", "domain:labs.google", "domain:perplexity.ai", "domain:x.ai", "domain:grok.com",
	"domain:copilot.microsoft.com", "domain:mistral.ai", "domain:cursor.com", "domain:cursor.sh",
}

// cascadeAIRules send each domain-only AI user of inbounds to their pinned exit
// for AI services; exits outside c are skipped.
func cascadeAIRules(c *model.Cascade, aiDomains map[string]int, domains []string, tagPrefix string, inbounds []string) []map[string]any {
	byNode := map[int][]string{}
	for email, id := range aiDomains {
		byNode[id] = append(byNode[id], email)
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
			"ruleTag":     fmt.Sprintf("%sai-n%d", tagPrefix, l.NodeId),
			"user":        emails,
			"domain":      slices.Clone(domains),
			"outboundTag": cascadeLinkTag(c.Id, l.NodeId),
		}
		if len(inbounds) > 0 {
			rule["inboundTag"] = inbounds
		}
		rules = append(rules, rule)
	}
	return rules
}

// aiRulesKey fingerprints the AI and outage rules of c, so the sticky pass
// rewrites the entry when only they changed.
func aiRulesKey(c *model.Cascade, ai aiExitPlan) string {
	raw, _ := json.Marshal([]any{
		cascadeAIRules(c, ai.Domains, ai.Sites, "", nil),
		cascadeOutageRules(c, ai.Outages, "", nil),
	})
	return string(raw)
}
