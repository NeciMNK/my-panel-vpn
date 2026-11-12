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
}

// isDomainPin reports whether a fixed exit only carries the user's AI domains.
func (p aiExitPlan) isDomainPin(r model.CascadeFixedExit) bool {
	id, ok := p.Domains[r.Email]
	return ok && id == r.NodeId
}

func aiCountryExits(fixed []model.CascadeFixedExit, countryOf map[int]string) (aiExitPlan, error) {
	plan := aiExitPlan{Domains: map[string]int{}, All: map[string]int{}, Sites: (&SettingService{}).GetTgAIExitDomains()}
	var rows []model.CascadeAICountry
	if err := database.GetDB().Find(&rows).Error; err != nil {
		return plan, err
	}
	want := make(map[string]model.CascadeAICountry, len(rows))
	for _, r := range rows {
		want[r.Email] = r
	}
	for _, f := range fixed {
		r, ok := want[f.Email]
		if !ok || countryOf[f.NodeId] != r.Country {
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
	var nodes []model.Node
	if err := database.GetDB().Select("id, country").Find(&nodes).Error; err != nil {
		return aiExitPlan{}, err
	}
	countryOf := make(map[int]string, len(nodes))
	for _, n := range nodes {
		countryOf[n.Id] = n.Country
	}
	return aiCountryExits(fixed, countryOf)
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

// aiRulesKey fingerprints the AI rules of c, so the sticky pass rewrites the
// entry when only they changed.
func aiRulesKey(c *model.Cascade, aiDomains map[string]int, domains []string) string {
	raw, _ := json.Marshal(cascadeAIRules(c, aiDomains, domains, "", nil))
	return string(raw)
}
