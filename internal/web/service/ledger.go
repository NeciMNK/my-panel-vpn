package service

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	LedgerIncome  = "income"
	LedgerExpense = "expense"

	NonPayingClient   = "client"
	NonPayingGroup    = "group"
	NonPayingFreePlan = "freePlan"
)

var (
	ledgerMonthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

	ErrLedgerMonth  = errors.New("month must be YYYY-MM")
	ErrLedgerAmount = errors.New("amount must be positive")
	// ErrFriendDiscount keeps a friend's price above zero: 100% would hand out plans for free.
	ErrFriendDiscount = errors.New("friends discount must be 0-99%")
	ErrLedgerKind     = errors.New("kind must be income or expense")
	ErrLedgerName     = errors.New("name is required")
	ErrLedgerRange    = errors.New("end month is before start month")
	ErrLedgerDay      = errors.New("billing day must be 1-31")
	ErrLedgerMonths   = errors.New("extend by 1-36 months")
)

// LedgerService is the admin's profit/loss book: recurring server costs,
// booked income and expenses, and who is expected to pay at all.
type LedgerService struct {
	settingService SettingService
	clientService  ClientService
}

// LedgerNonPaying is a client kept out of the expected income, with the reason.
type LedgerNonPaying struct {
	Email  string `json:"email" example:"admin"`
	Reason string `json:"reason" example:"group"`
	Group  string `json:"group" example:"family"`
}

// LedgerServerDue is a server's next bill.
type LedgerServerDue struct {
	ServerId int    `json:"serverId" example:"1"`
	Name     string `json:"name" example:"Hetzner DE"`
	Amount   int    `json:"amount" example:"500"`
	Date     string `json:"date" example:"2026-11-15"`
}

// LedgerReport is one month of the book. Money is in whole units of Currency.
type LedgerReport struct {
	Month          string               `json:"month" example:"2026-11"`
	Currency       string               `json:"currency" example:"RUB"`
	Price          int                  `json:"price" example:"300"`
	Payment        LedgerPayment        `json:"payment"`
	Income         int                  `json:"income" example:"2400"`
	ServerCosts    int                  `json:"serverCosts" example:"1500"`
	OtherExpenses  int                  `json:"otherExpenses" example:"0"`
	Expenses       int                  `json:"expenses" example:"1500"`
	Balance        int                  `json:"balance" example:"900"`
	PayingClients  int                  `json:"payingClients" example:"10"`
	ExpectedIncome int                  `json:"expectedIncome" example:"3000"`
	NonPaying      []LedgerNonPaying    `json:"nonPaying"`
	FreeGroups     []string             `json:"freeGroups"`
	Servers        []model.LedgerServer `json:"servers"`
	Upcoming       []LedgerServerDue    `json:"upcoming"`
	Entries        []model.LedgerEntry  `json:"entries"`
}

// LedgerPayment is what clients pay: the bot bills by it and the Finance page edits it.
type LedgerPayment struct {
	Price     int    `json:"price" example:"300"`
	Currency  string `json:"currency" example:"RUB"`
	Discounts string `json:"discounts" example:"3:10, 6:15, 12:20"`
	Details   string `json:"details" example:"Card 2200 0000 0000 0000"`
	// FriendDiscount is the percent off every plan for clients marked as friends.
	FriendDiscount int `json:"friendDiscount" example:"20"`
	// AIExitPrice is the one-off price of the bot's AI button; friends get it free, 0 hides it.
	AIExitPrice int `json:"aiExitPrice" example:"50"`
}

func LedgerMonthOf(t time.Time) string { return t.Format("2006-01") }

// ShiftLedgerMonth moves a valid YYYY-MM by delta months.
func ShiftLedgerMonth(month string, delta int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return LedgerMonthOf(t.AddDate(0, delta, 0))
}

func validLedgerMonth(month string) bool { return ledgerMonthPattern.MatchString(month) }

// serverChargedIn is whether a server's cost belongs to month; YYYY-MM compares as text.
func serverChargedIn(server model.LedgerServer, month string) bool {
	return server.StartMonth <= month && (server.EndMonth == "" || month <= server.EndMonth)
}

func (s *LedgerService) Report(month string) (*LedgerReport, error) {
	if !validLedgerMonth(month) {
		return nil, ErrLedgerMonth
	}
	db := database.GetDB()
	report := &LedgerReport{Month: month, NonPaying: []LedgerNonPaying{}, FreeGroups: []string{}}
	report.Payment = s.payment()
	report.Price = report.Payment.Price
	report.Currency = strings.TrimSpace(report.Payment.Currency)

	if err := db.Order("start_month, id").Find(&report.Servers).Error; err != nil {
		return nil, err
	}
	report.Upcoming = []LedgerServerDue{}
	today := time.Now()
	for _, server := range report.Servers {
		if serverChargedIn(server, month) {
			report.ServerCosts += server.MonthlyCost
		}
		if due, ok := nextServerBill(server, today); ok {
			report.Upcoming = append(report.Upcoming, LedgerServerDue{
				ServerId: server.Id, Name: server.Name, Amount: server.MonthlyCost, Date: due.Format(time.DateOnly),
			})
		}
	}
	slices.SortStableFunc(report.Upcoming, func(a, b LedgerServerDue) int { return strings.Compare(a.Date, b.Date) })
	if err := db.Where("month = ?", month).Order("created_at, id").Find(&report.Entries).Error; err != nil {
		return nil, err
	}
	for _, entry := range report.Entries {
		if entry.Kind == LedgerIncome {
			report.Income += entry.Amount
		} else {
			report.OtherExpenses += entry.Amount
		}
	}
	report.Expenses = report.ServerCosts + report.OtherExpenses
	report.Balance = report.Income - report.Expenses

	if err := db.Model(&model.LedgerFreeGroup{}).Order("name").Pluck("name", &report.FreeGroups).Error; err != nil {
		return nil, err
	}
	if err := s.countPaying(report); err != nil {
		return nil, err
	}
	report.ExpectedIncome = report.PayingClients * report.Price
	return report, nil
}

// countPaying splits enabled clients into paying ones and the non-paying list;
// a client's own mark wins over its group, which wins over a free plan.
func (s *LedgerService) countPaying(report *LedgerReport) error {
	var rows []struct {
		Email     string
		GroupName string
		NonPaying bool
		FreePlan  bool
	}
	err := database.GetDB().Table("clients AS c").
		Select("c.email, c.group_name, COALESCE(b.non_paying, false) AS non_paying, COALESCE(b.free_plan, false) AS free_plan").
		Joins("LEFT JOIN client_billings b ON b.client_id = c.id").
		Where("c.enable = ?", true).
		Order("c.email").
		Scan(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		item := LedgerNonPaying{Email: row.Email, Group: row.GroupName}
		switch {
		case row.NonPaying:
			item.Reason = NonPayingClient
		case row.GroupName != "" && slices.Contains(report.FreeGroups, row.GroupName):
			item.Reason = NonPayingGroup
		case row.FreePlan:
			item.Reason = NonPayingFreePlan
		default:
			report.PayingClients++
			continue
		}
		report.NonPaying = append(report.NonPaying, item)
	}
	return nil
}

func (s *LedgerService) SaveServer(server *model.LedgerServer) error {
	server.Name = strings.TrimSpace(server.Name)
	if server.BillingDay == 0 {
		server.BillingDay = 1
	}
	switch {
	case server.Name == "":
		return ErrLedgerName
	case server.MonthlyCost <= 0:
		return ErrLedgerAmount
	case !validLedgerMonth(server.StartMonth) || (server.EndMonth != "" && !validLedgerMonth(server.EndMonth)):
		return ErrLedgerMonth
	case server.EndMonth != "" && server.EndMonth < server.StartMonth:
		return ErrLedgerRange
	case server.BillingDay < 1 || server.BillingDay > 31:
		return ErrLedgerDay
	}
	return database.GetDB().Omit("reminded_for", "paid_through").Save(server).Error
}

func (s *LedgerService) DeleteServer(id int) error {
	return deleteLedgerRow(&model.LedgerServer{}, id)
}

func (s *LedgerService) AddEntry(entry *model.LedgerEntry) error {
	entry.Id = 0
	entry.Email = strings.TrimSpace(entry.Email)
	entry.Note = strings.TrimSpace(entry.Note)
	switch {
	case !validLedgerMonth(entry.Month):
		return ErrLedgerMonth
	case entry.Kind != LedgerIncome && entry.Kind != LedgerExpense:
		return ErrLedgerKind
	case entry.Amount <= 0:
		return ErrLedgerAmount
	}
	return database.GetDB().Create(entry).Error
}

func (s *LedgerService) DeleteEntry(id int) error {
	return deleteLedgerRow(&model.LedgerEntry{}, id)
}

func deleteLedgerRow(row any, id int) error {
	result := database.GetDB().Delete(row, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RecordPayment books a confirmed client payment as this month's income.
func (s *LedgerService) RecordPayment(email string, amount int, note string, at time.Time) error {
	return s.AddEntry(&model.LedgerEntry{Month: LedgerMonthOf(at), Kind: LedgerIncome, Amount: amount, Email: email, Note: note})
}

func (s *LedgerService) SetClientNonPaying(email string, nonPaying bool) error {
	billing, err := s.clientService.GetBillingByEmail(email)
	if err != nil {
		return err
	}
	billing.NonPaying = nonPaying
	return s.clientService.SaveBilling(billing)
}

func (s *LedgerService) SetGroupNonPaying(name string, nonPaying bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrLedgerName
	}
	db := database.GetDB()
	if !nonPaying {
		return db.Where("name = ?", name).Delete(&model.LedgerFreeGroup{}).Error
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.LedgerFreeGroup{Name: name}).Error
}

// moveLedgerFreeGroup keeps a group's non-paying mark across a rename and drops it on delete.
func moveLedgerFreeGroup(db *gorm.DB, oldName, newName string) error {
	result := db.Where("name = ?", oldName).Delete(&model.LedgerFreeGroup{})
	if result.Error != nil || result.RowsAffected == 0 || newName == "" {
		return result.Error
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.LedgerFreeGroup{Name: newName}).Error; err != nil {
		return fmt.Errorf("keep non-paying mark on group %q: %w", newName, err)
	}
	return nil
}

func (s *LedgerService) payment() LedgerPayment {
	var p LedgerPayment
	p.Price, _ = s.settingService.GetTgPaymentPrice()
	p.Currency, _ = s.settingService.GetTgPaymentCurrency()
	p.Discounts, _ = s.settingService.GetTgPaymentDiscounts()
	p.Details, _ = s.settingService.GetTgPaymentDetails()
	p.FriendDiscount, _ = s.settingService.GetTgFriendDiscount()
	p.AIExitPrice, _ = s.settingService.GetTgAIExitPrice()
	return p
}

// SavePayment validates everything before writing, so a bad discount list changes nothing.
func (s *LedgerService) SavePayment(p LedgerPayment) error {
	if p.Price < 0 || p.AIExitPrice < 0 {
		return ErrLedgerAmount
	}
	if p.FriendDiscount < 0 || p.FriendDiscount > 99 {
		return ErrFriendDiscount
	}
	if _, err := entity.ParsePaymentPlans(p.Discounts); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"tgPaymentPrice":     strconv.Itoa(p.Price),
		"tgPaymentCurrency":  strings.TrimSpace(p.Currency),
		"tgPaymentDiscounts": strings.TrimSpace(p.Discounts),
		"tgPaymentDetails":   strings.TrimSpace(p.Details),
		"tgFriendDiscount":   strconv.Itoa(p.FriendDiscount),
		"tgAIExitPrice":      strconv.Itoa(p.AIExitPrice),
	} {
		if err := s.settingService.setString(key, value); err != nil {
			return err
		}
	}
	return nil
}

// serverBillIn is the server's due date in month, clamped to the month's last day.
func serverBillIn(server model.LedgerServer, month time.Time) time.Time {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(max(server.BillingDay, 1), last)-1)
}

// nextServerBill is the first due date from today on, in a month the server is still paid for.
func nextServerBill(server model.LedgerServer, now time.Time) (time.Time, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := today.AddDate(0, 0, 1-today.Day())
	if start, err := time.ParseInLocation("2006-01", server.StartMonth, now.Location()); err == nil && start.After(month) {
		month = start
	}
	if paid, err := time.ParseInLocation("2006-01", server.PaidThrough, now.Location()); err == nil && !paid.Before(month) {
		month = paid.AddDate(0, 1, 0)
	}
	for range 2 {
		if !serverChargedIn(server, LedgerMonthOf(month)) {
			return time.Time{}, false
		}
		if due := serverBillIn(server, month); !due.Before(today) {
			return due, true
		}
		month = month.AddDate(0, 1, 0)
	}
	return time.Time{}, false
}

// ExtendServer pays the server's next months bills ahead; a stopped server runs again until then.
func (s *LedgerService) ExtendServer(id, months int, now time.Time) (model.LedgerServer, error) {
	var server model.LedgerServer
	if months < 1 || months > 36 {
		return server, ErrLedgerMonths
	}
	db := database.GetDB()
	if err := db.First(&server, id).Error; err != nil {
		return server, err
	}
	running := server
	running.EndMonth = ""
	due, _ := nextServerBill(running, now)
	server.PaidThrough = LedgerMonthOf(due.AddDate(0, 0, 1-due.Day()).AddDate(0, months-1, 0))
	if server.EndMonth != "" && server.EndMonth < server.PaidThrough {
		server.EndMonth = server.PaidThrough
	}
	err := db.Model(&server).Updates(map[string]any{"paid_through": server.PaidThrough, "end_month": server.EndMonth}).Error
	return server, err
}

// ServersToRemind are bills due within days from now that admins were not yet told about.
func (s *LedgerService) ServersToRemind(now time.Time, days int) ([]LedgerServerDue, error) {
	var servers []model.LedgerServer
	if err := database.GetDB().Order("id").Find(&servers).Error; err != nil {
		return nil, err
	}
	horizon := time.Date(now.Year(), now.Month(), now.Day()+days, 0, 0, 0, 0, now.Location())
	var out []LedgerServerDue
	for _, server := range servers {
		due, ok := nextServerBill(server, now)
		if !ok || due.After(horizon) || server.RemindedFor == LedgerMonthOf(due) {
			continue
		}
		out = append(out, LedgerServerDue{ServerId: server.Id, Name: server.Name, Amount: server.MonthlyCost, Date: due.Format(time.DateOnly)})
	}
	return out, nil
}

// MarkReminded records that the bill due on date was announced, so it is announced once.
func (s *LedgerService) MarkReminded(due LedgerServerDue) error {
	return database.GetDB().Model(&model.LedgerServer{}).Where("id = ?", due.ServerId).
		Update("reminded_for", due.Date[:len("2006-01")]).Error
}
