package service

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// AI domain suggestion states; every state but pending is an admin's answer.
const (
	AISuggestPending  = "pending"
	AISuggestAdded    = "added"
	AISuggestPartial  = "partial"
	AISuggestDeclined = "declined"
	AISuggestPresent  = "present"
)

// maxSuggestedDomains bounds one suggestion so a paste cannot flood the admins.
const maxSuggestedDomains = 20

var (
	ErrNoSuggestedDomain = errors.New("no domain suggested")
	ErrAISuggestVerdict  = errors.New("unknown answer to an AI domain suggestion")
)

// AIDomainService keeps the admin's AI domain list and the clients' suggestions for it.
type AIDomainService struct{}

// AIDomainsView is the AI domains page: the list in force and the latest suggestions.
type AIDomainsView struct {
	Domains     string                     `json:"domains" example:"domain:openai.com,geosite:openai"`
	Suggestions []model.AIDomainSuggestion `json:"suggestions"`
}

func (s *AIDomainService) View() (AIDomainsView, error) {
	view := AIDomainsView{Domains: strings.Join((&SettingService{}).GetTgAIExitDomains(), ",")}
	err := database.GetDB().Order("id DESC").Limit(200).Find(&view.Suggestions).Error
	return view, err
}

// Save replaces the AI domain list; an empty one restores the built-in list.
func (s *AIDomainService) Save(raw string) error {
	if err := validateAIDomains(raw); err != nil {
		return err
	}
	return (&SettingService{}).setString("tgAIExitDomains", strings.Join(splitAIDomains(raw), ","))
}

// ParseSuggestedDomains reads a client's free text into bare hosts: links lose
// their scheme, path and port, "www." goes, and duplicates are dropped.
func ParseSuggestedDomains(text string) ([]string, error) {
	var out []string
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	}) {
		host := strings.ToLower(word)
		if _, rest, found := strings.Cut(host, "://"); found {
			host = rest
		}
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		if h, port, found := strings.Cut(host, ":"); found && port != "" && strings.Trim(port, "0123456789") == "" {
			host = h
		}
		host = strings.TrimPrefix(host, "www.")
		if !suggestedHost.MatchString(host) {
			return nil, common.NewErrorf("not a domain: %s", word)
		}
		if !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoSuggestedDomain
	}
	if len(out) > maxSuggestedDomains {
		out = out[:maxSuggestedDomains]
	}
	return out, nil
}

func (s *AIDomainService) Suggest(tgUserID int64, email string, domains []string) (model.AIDomainSuggestion, error) {
	sg := model.AIDomainSuggestion{
		TgUserId: tgUserID, Email: email, Domains: strings.Join(domains, ","),
		Status: AISuggestPending, CreatedAt: time.Now().Unix(),
	}
	err := database.GetDB().Create(&sg).Error
	return sg, err
}

// Resolve records the admin's answer; claimed is false when another answer came
// first. "Added" also merges the suggested sites into the list.
func (s *AIDomainService) Resolve(id int, verdict string) (sg model.AIDomainSuggestion, claimed bool, err error) {
	if !slices.Contains([]string{AISuggestAdded, AISuggestPartial, AISuggestDeclined, AISuggestPresent}, verdict) {
		return sg, false, ErrAISuggestVerdict
	}
	db := database.GetDB()
	if err = db.First(&sg, id).Error; err != nil {
		return sg, false, err
	}
	if sg.Status != AISuggestPending {
		return sg, false, nil
	}
	if verdict == AISuggestAdded {
		if err = s.add(strings.Split(sg.Domains, ",")); err != nil {
			return sg, false, err
		}
	}
	now := time.Now().Unix()
	res := db.Model(&model.AIDomainSuggestion{}).Where("id = ? AND status = ?", id, AISuggestPending).
		Updates(map[string]any{"status": verdict, "resolved_at": now})
	if res.Error != nil {
		return sg, false, res.Error
	}
	if res.RowsAffected == 0 {
		err = db.First(&sg, id).Error
		return sg, false, err
	}
	sg.Status, sg.ResolvedAt = verdict, now
	return sg, true, nil
}

// add appends the hosts the list does not already name; adding twice is a no-op.
func (s *AIDomainService) add(hosts []string) error {
	list := (&SettingService{}).GetTgAIExitDomains()
	for _, host := range hosts {
		if host == "" || slices.Contains(list, host) || slices.Contains(list, "domain:"+host) || slices.Contains(list, "full:"+host) {
			continue
		}
		list = append(list, "domain:"+host)
	}
	return s.Save(strings.Join(list, ","))
}
