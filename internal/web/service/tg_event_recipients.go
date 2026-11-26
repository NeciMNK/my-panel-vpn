package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// adminOnlyEvents never reach a client's chat: a login alert names the panel's
// admins and their IPs.
var adminOnlyEvents = []string{"login.attempt"}

// TgEventAudience is who, besides the admins, gets one kind of event.
type TgEventAudience struct {
	Groups  []string `json:"groups"`
	Clients []string `json:"clients"`
}

func parseTgEventRecipients(raw string) (map[string]TgEventAudience, error) {
	out := map[string]TgEventAudience{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("telegram event recipients: %w", err)
	}
	for event := range out {
		if slices.Contains(adminOnlyEvents, event) {
			delete(out, event)
		}
	}
	return out, nil
}

// SetTgEventRecipients validates and stores the per-event recipients.
func (s *SettingService) SetTgEventRecipients(raw string) error {
	parsed, err := parseTgEventRecipients(raw)
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(parsed)
	if err != nil {
		return err
	}
	return s.setString("tgEventRecipients", string(normalized))
}

// TgEventChats lists the client Telegram chats an event goes to besides the admins.
func (s *SettingService) TgEventChats(eventType string) ([]int64, error) {
	raw, err := s.getString("tgEventRecipients")
	if err != nil {
		return nil, err
	}
	recipients, err := parseTgEventRecipients(raw)
	if err != nil {
		return nil, err
	}
	audience, ok := recipients[eventType]
	if !ok || len(audience.Groups)+len(audience.Clients) == 0 {
		return nil, nil
	}
	db := database.GetDB()
	match := db.Where("1 = 0")
	if len(audience.Groups) > 0 {
		match = match.Or("group_name IN ?", audience.Groups)
	}
	if len(audience.Clients) > 0 {
		match = match.Or("email IN ?", audience.Clients)
	}
	var chats []int64
	err = db.Model(&model.ClientRecord{}).Where("tg_id > 0").Where(match).Distinct().Pluck("tg_id", &chats).Error
	return chats, err
}
