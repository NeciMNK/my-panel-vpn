package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// What a user wants while a server they are pinned to is down.
const (
	AIOutageBlock = "block"
	AIOutageOther = "other"
	AIOutageOff   = "off"
)

// AIPinOutage is one user's pin on a dead server and what replaces it:
// a stand-in server, all traffic cut, or the AI services in Domains cut.
type AIPinOutage struct {
	Email      string
	NodeId     int
	SwapNodeId int
	Swapped    bool
	Off        bool
	Domains    []string
}

func cascadeBlockTag(id int) string { return cascadePrefix(id) + "block" }

func loadAIOutageChoices() (map[string]model.AIOutageChoice, error) {
	var rows []model.AIOutageChoice
	if err := database.GetDB().Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.AIOutageChoice, len(rows))
	for _, r := range rows {
		out[r.Email] = r
	}
	return out, nil
}

func (w exitWorld) outage(f model.CascadeFixedExit, choice model.AIOutageChoice, sites []string) AIPinOutage {
	o := AIPinOutage{Email: f.Email, NodeId: f.NodeId}
	switch choice.Action {
	case AIOutageOff:
		o.Off = true
		return o
	case AIOutageOther:
		if id, ok := w.standIn(f.NodeId, choice.Country); ok {
			o.SwapNodeId, o.Swapped = id, true
			return o
		}
	case AIOutageBlock:
		o.Domains = aiServiceDomains(sites, choice.Services)
		return o
	}
	o.Domains = slices.Clone(sites)
	return o
}

// standIn is the live exit that replaces dead in the cascades it serves: the
// lowest id of its own country, else of fallback, so every pass picks the same.
func (w exitWorld) standIn(dead int, fallback string) (int, bool) {
	var live []int
	for _, c := range w.cascades {
		if !slices.ContainsFunc(c.Links, func(l model.CascadeLink) bool { return l.NodeId == dead }) {
			continue
		}
		for _, l := range c.Links {
			if l.NodeId != dead && w.healthy[l.NodeId] && !slices.Contains(live, l.NodeId) {
				live = append(live, l.NodeId)
			}
		}
	}
	slices.Sort(live)
	for _, country := range []string{w.countryOf[dead], strings.ToUpper(fallback)} {
		if country == "" {
			continue
		}
		for _, id := range live {
			if w.countryOf[id] == country {
				return id, true
			}
		}
	}
	return 0, false
}

// cascadeOutageRules cut, for each user whose pin on one of c's exits is down
// and has no stand-in, either all their traffic or the chosen AI services.
func cascadeOutageRules(c *model.Cascade, outages []AIPinOutage, tagPrefix string, inbounds []string) []map[string]any {
	var off []string
	blocked := map[string][]string{}
	domainsOf := map[string][]string{}
	for _, o := range outages {
		if o.Swapped || !slices.ContainsFunc(c.Links, func(l model.CascadeLink) bool { return l.NodeId == o.NodeId }) {
			continue
		}
		if o.Off {
			off = appendUnique(off, o.Email)
			continue
		}
		key := strings.Join(o.Domains, ",")
		blocked[key] = appendUnique(blocked[key], o.Email)
		domainsOf[key] = o.Domains
	}
	var rules []map[string]any
	rule := func(tag string, users []string) map[string]any {
		slices.Sort(users)
		r := map[string]any{"type": "field", "ruleTag": tagPrefix + tag, "user": users, "outboundTag": cascadeBlockTag(c.Id)}
		if len(inbounds) > 0 {
			r["inboundTag"] = inbounds
		}
		return r
	}
	if len(off) > 0 {
		r := rule("outage-off", off)
		r["network"] = "tcp,udp"
		rules = append(rules, r)
	}
	keys := make([]string, 0, len(blocked))
	for k := range blocked {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for i, k := range keys {
		r := rule(fmt.Sprintf("outage-ai-%d", i), blocked[k])
		r["domain"] = slices.Clone(domainsOf[k])
		rules = append(rules, r)
	}
	return rules
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// AIOutageChoiceOf is the emails' choice; without one their AI services are blocked.
func (s *CascadeService) AIOutageChoiceOf(emails []string) model.AIOutageChoice {
	var row model.AIOutageChoice
	err := database.GetDB().Where("email IN ?", emails).Order("id desc").Limit(1).Find(&row).Error
	if err != nil || row.Action == "" {
		return model.AIOutageChoice{Action: AIOutageBlock}
	}
	return row
}

// SetAIOutageChoice remembers what the person wants during every outage of
// their pinned servers and applies it at once if one is down now.
func (s *CascadeService) SetAIOutageChoice(ctx context.Context, emails []string, choice model.AIOutageChoice) error {
	switch choice.Action {
	case AIOutageBlock, AIOutageOther, AIOutageOff:
	default:
		return common.NewErrorf("unknown outage choice %q", choice.Action)
	}
	names := AIServiceNames()
	for _, name := range choice.Services {
		if !slices.Contains(names, name) {
			return common.NewErrorf("unknown AI service %q", name)
		}
	}
	if choice.Action != AIOutageBlock {
		choice.Services = nil
	}
	choice.Country = strings.ToUpper(strings.TrimSpace(choice.Country))
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("email IN ?", emails).Delete(&model.AIOutageChoice{}).Error; err != nil {
			return err
		}
		for _, email := range emails {
			row := model.AIOutageChoice{Email: email, Action: choice.Action, Services: choice.Services, Country: choice.Country}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.StickCascadeUsers(ctx)
	return nil
}

// AIPinHolders are the users with a pinned exit on the server.
func (s *CascadeService) AIPinHolders(nodeID int) ([]string, error) {
	var emails []string
	err := database.GetDB().Model(&model.CascadeFixedExit{}).Where("node_id = ?", nodeID).
		Distinct().Order("email").Pluck("email", &emails).Error
	if len(emails) == 0 {
		return nil, err
	}
	return emails, err
}

// AIPinOutages are the emails' pins on dead servers, as routed right now.
func (s *CascadeService) AIPinOutages(emails []string) ([]AIPinOutage, error) {
	plan, err := loadAIExitPlan()
	if err != nil {
		return nil, err
	}
	var out []AIPinOutage
	for _, o := range plan.Outages {
		if slices.Contains(emails, o.Email) {
			out = append(out, o)
		}
	}
	return out, nil
}

// AIService is one AI service of the admin's list, as a user picks it.
type AIService struct {
	Name    string
	Domains []string
}

// AIServiceNames are the services of the admin's AI list, in its order.
func AIServiceNames() []string {
	return AIServicesIn((&SettingService{}).GetTgAIExitDomains())
}

// aiServiceDomains are the list's entries of the named services; none named,
// or none left in the list, blocks every AI service rather than none.
func aiServiceDomains(sites []string, services []string) []string {
	var out []string
	for _, g := range groupAIServices(sites) {
		if slices.Contains(services, g.Name) {
			out = append(out, g.Domains...)
		}
	}
	if len(out) == 0 {
		return slices.Clone(sites)
	}
	return out
}

var knownAIHosts = map[string]string{
	"openai.com": "ChatGPT", "chatgpt.com": "ChatGPT", "oaistatic.com": "ChatGPT", "oaiusercontent.com": "ChatGPT",
	"anthropic.com": "Claude", "claude.ai": "Claude", "claude.com": "Claude", "claudeusercontent.com": "Claude",
	"gemini.google.com": "Gemini", "aistudio.google.com": "Gemini", "generativelanguage.googleapis.com": "Gemini",
	"notebooklm.google.com": "Gemini", "labs.google": "Gemini", "perplexity.ai": "Perplexity",
	"x.ai": "Grok", "grok.com": "Grok", "copilot.microsoft.com": "Copilot", "mistral.ai": "Mistral",
	"cursor.com": "Cursor", "cursor.sh": "Cursor",
}

var knownAIGeosites = map[string]string{
	"openai": "ChatGPT", "anthropic": "Claude", "google-gemini": "Gemini", "perplexity": "Perplexity", "xai": "Grok",
}

// groupAIServices names each entry of the admin's flat AI list by its service,
// so users pick "Claude" instead of four domains; unknown hosts go by their site.
func groupAIServices(sites []string) []AIService {
	var out []AIService
	for _, entry := range sites {
		name := aiServiceName(entry)
		i := slices.IndexFunc(out, func(g AIService) bool { return g.Name == name })
		if i < 0 {
			out = append(out, AIService{Name: name})
			i = len(out) - 1
		}
		out[i].Domains = append(out[i].Domains, entry)
	}
	return out
}

func aiServiceName(entry string) string {
	kind, value, found := strings.Cut(entry, ":")
	if !found {
		kind, value = "domain", entry
	}
	value = strings.ToLower(value)
	switch kind {
	case "geosite":
		value, _, _ = strings.Cut(value, "@")
		if name, ok := knownAIGeosites[value]; ok {
			return name
		}
		return value
	case "domain", "full", "dotless":
		for host := value; host != ""; {
			if name, ok := knownAIHosts[host]; ok {
				return name
			}
			_, host, _ = strings.Cut(host, ".")
		}
		labels := strings.Split(value, ".")
		if len(labels) > 2 {
			return strings.Join(labels[len(labels)-2:], ".")
		}
	}
	return value
}

// AIServicesIn names the services the domains of an outage belong to.
func AIServicesIn(domains []string) []string {
	var names []string
	for _, g := range groupAIServices(domains) {
		names = append(names, g.Name)
	}
	return names
}
