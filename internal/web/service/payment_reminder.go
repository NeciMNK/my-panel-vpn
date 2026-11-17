package service

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// PaymentReminderDayChoices are the days before the due date a client can pick
// in the bot, besides the panel's own expireDiff.
var PaymentReminderDayChoices = []int{7, 3, 1}

// PaymentReminderService keeps each Telegram account's reminder choice and
// which reminder it was last sent, so every chosen day reminds once.
type PaymentReminderService struct {
	settingService SettingService
}

// PaymentReminderPrefs is what the client sees and toggles in the bot.
type PaymentReminderPrefs struct {
	Disabled bool
	Days     []int // descending
	Choices  []int // descending
}

func (s *PaymentReminderService) load(tgID int64) (model.PaymentReminder, error) {
	row := model.PaymentReminder{TgID: tgID}
	err := database.GetDB().Where("tg_id = ?", tgID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, nil
	}
	return row, err
}

func (s *PaymentReminderService) save(row model.PaymentReminder) error {
	return database.GetDB().Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}

func (s *PaymentReminderService) defaultDays() []int {
	if d, err := s.settingService.GetExpireDiff(); err == nil && d > 0 {
		return []int{d}
	}
	return []int{3}
}

func parseDays(v string) []int {
	var out []int
	for part := range strings.SplitSeq(v, ",") {
		if d, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && d > 0 && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out
}

// Prefs is the account's choice; an account that never chose gets the default days.
func (s *PaymentReminderService) Prefs(tgID int64) (PaymentReminderPrefs, error) {
	row, err := s.load(tgID)
	if err != nil {
		return PaymentReminderPrefs{}, err
	}
	days := parseDays(row.Days)
	if row.Days == "" {
		days = s.defaultDays()
	}
	choices := parseDays(joinDays(append(slices.Clone(PaymentReminderDayChoices), s.defaultDays()...)))
	return PaymentReminderPrefs{Disabled: row.Disabled, Days: days, Choices: choices}, nil
}

func joinDays(days []int) string {
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

func (s *PaymentReminderService) SetDisabled(tgID int64, disabled bool) error {
	row, err := s.load(tgID)
	if err != nil {
		return err
	}
	row.Disabled = disabled
	return s.save(row)
}

// SetDays stores the chosen days; none chosen is stored as "off" rather than as
// "" so it does not fall back to the default.
func (s *PaymentReminderService) SetDays(tgID int64, days []int) error {
	row, err := s.load(tgID)
	if err != nil {
		return err
	}
	row.Days = joinDays(parseDays(joinDays(days)))
	if row.Days == "" {
		row.Days, row.Disabled = "0", true
	}
	return s.save(row)
}

// ToggleDay adds or removes one day from the account's choice.
func (s *PaymentReminderService) ToggleDay(tgID int64, day int) error {
	prefs, err := s.Prefs(tgID)
	if err != nil {
		return err
	}
	days := prefs.Days
	if i := slices.Index(days, day); i >= 0 {
		days = slices.Delete(slices.Clone(days), i, i+1)
	} else {
		days = append(slices.Clone(days), day)
	}
	if len(days) > 0 && prefs.Disabled {
		if err := s.SetDisabled(tgID, false); err != nil {
			return err
		}
	}
	return s.SetDays(tgID, days)
}

// Due reports the reminder to send now: the nearest chosen day the due date is
// already within, unless that day (or a nearer one) was sent for this due date.
func (s *PaymentReminderService) Due(tgID int64, due, now time.Time) (int, bool) {
	left := due.Sub(now)
	if left <= 0 {
		return 0, false
	}
	row, err := s.load(tgID)
	if err != nil || row.Disabled {
		return 0, false
	}
	prefs, err := s.Prefs(tgID)
	if err != nil {
		return 0, false
	}
	day := 0
	for _, d := range prefs.Days {
		if left <= time.Duration(d)*24*time.Hour {
			day = d
		}
	}
	if day == 0 {
		return 0, false
	}
	if row.SentDue == due.UnixMilli() && row.SentDay > 0 && row.SentDay <= day {
		return 0, false
	}
	return day, true
}

// MarkSent records that the reminder for this due date and day went out.
func (s *PaymentReminderService) MarkSent(tgID int64, due time.Time, day int) error {
	row, err := s.load(tgID)
	if err != nil {
		return err
	}
	row.SentDue, row.SentDay = due.UnixMilli(), day
	return s.save(row)
}
