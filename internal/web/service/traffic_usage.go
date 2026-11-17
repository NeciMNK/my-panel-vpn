package service

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	// Hourly rows older than this many panel-timezone days fold into daily rows.
	trafficUsageHourlyDays = 8
	// Daily rows outlive the longest period (a rolling or calendar year).
	trafficUsageDailyDays = 400
	trafficUsageBatchSize = 200
)

// Period ids an admin picks from: rolling windows, then calendar periods that
// start over at each new day, Monday-based week, month, half-year and year.
var TrafficUsagePeriods = []string{"24h", "7d", "30d", "180d", "365d", "day", "week", "month", "halfyear", "year"}

const defaultTrafficUsagePeriods = "24h,7d,30d,month"

// localUsageSubject is the node subject of traffic the local Xray reported.
const localUsageSubject = "0"

type trafficUsageKey struct {
	kind    string
	subject string
	bucket  int64
}

// trafficUsageBatch is the usage one accounting pass saw; it reaches the shared
// recorder only once that pass committed, so a rolled-back retry is not counted twice.
type trafficUsageBatch struct {
	sums map[trafficUsageKey][2]int64
}

func newTrafficUsageBatch() *trafficUsageBatch {
	return &trafficUsageBatch{sums: map[trafficUsageKey][2]int64{}}
}

func (b *trafficUsageBatch) add(kind, subject string, at time.Time, up, down int64) {
	if b == nil || (up <= 0 && down <= 0) {
		return
	}
	k := trafficUsageKey{kind, subject, at.Truncate(time.Hour).Unix()}
	cur := b.sums[k]
	b.sums[k] = [2]int64{cur[0] + max(up, 0), cur[1] + max(down, 0)}
}

// addClient books a client's usage and the same bytes on the server it ran on.
func (b *trafficUsageBatch) addClient(email, server string, at time.Time, up, down int64) {
	b.add(model.TrafficUsageClient, email, at, up, down)
	b.add(model.TrafficUsageNode, server, at, up, down)
}

type trafficUsageRecorder struct {
	mu      sync.Mutex
	pending *trafficUsageBatch
}

var usageRecorder = &trafficUsageRecorder{pending: newTrafficUsageBatch()}

func (r *trafficUsageRecorder) merge(b *trafficUsageBatch) {
	if b == nil || len(b.sums) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range b.sums {
		cur := r.pending.sums[k]
		r.pending.sums[k] = [2]int64{cur[0] + v[0], cur[1] + v[1]}
	}
}

func (r *trafficUsageRecorder) take() *trafficUsageBatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.pending
	r.pending = newTrafficUsageBatch()
	return out
}

// TrafficUsageService keeps per-period traffic history that survives the
// counter resets client_traffics goes through.
type TrafficUsageService struct {
	settingService SettingService
}

// Flush writes the usage recorded since the last flush into hourly rows. Rows
// add up, so every master flushing its own observations never double counts.
func (s *TrafficUsageService) Flush() error {
	batch := usageRecorder.take()
	if len(batch.sums) == 0 {
		return nil
	}
	rows := make([]model.TrafficUsageHour, 0, len(batch.sums))
	for k, v := range batch.sums {
		rows = append(rows, model.TrafficUsageHour{Kind: k.kind, Subject: k.subject, Bucket: k.bucket, Up: v[0], Down: v[1]})
	}
	err := database.RetryTxConflict(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			for chunk := range slices.Chunk(rows, trafficUsageBatchSize) {
				if err := tx.Clauses(usageUpsert("traffic_usage_hours")).Create(&chunk).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		usageRecorder.merge(batch)
		return fmt.Errorf("flush traffic usage: %w", err)
	}
	return nil
}

func usageUpsert(table string) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "kind"}, {Name: "subject"}, {Name: "bucket"}},
		DoUpdates: clause.Assignments(map[string]any{
			"up":   gorm.Expr(table + ".up + excluded.up"),
			"down": gorm.Expr(table + ".down + excluded.down"),
		}),
	}
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// Rollup folds hourly rows of whole days older than the hourly window into
// daily rows, and drops daily rows past the yearly window.
func (s *TrafficUsageService) Rollup(now time.Time, loc *time.Location) error {
	cutoff := dayStart(now, loc).AddDate(0, 0, -trafficUsageHourlyDays).Unix()
	expired := dayStart(now, loc).AddDate(0, 0, -trafficUsageDailyDays).Unix()
	return database.RetryTxConflict(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var hours []model.TrafficUsageHour
			if err := tx.Where("bucket < ?", cutoff).Find(&hours).Error; err != nil {
				return err
			}
			days := make(map[trafficUsageKey][2]int64)
			for _, h := range hours {
				k := trafficUsageKey{h.Kind, h.Subject, dayStart(time.Unix(h.Bucket, 0), loc).Unix()}
				cur := days[k]
				days[k] = [2]int64{cur[0] + h.Up, cur[1] + h.Down}
			}
			rows := make([]model.TrafficUsageDay, 0, len(days))
			for k, v := range days {
				rows = append(rows, model.TrafficUsageDay{Kind: k.kind, Subject: k.subject, Bucket: k.bucket, Up: v[0], Down: v[1]})
			}
			for chunk := range slices.Chunk(rows, trafficUsageBatchSize) {
				if err := tx.Clauses(usageUpsert("traffic_usage_days")).Create(&chunk).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("bucket < ?", cutoff).Delete(&model.TrafficUsageHour{}).Error; err != nil {
				return err
			}
			return tx.Where("bucket < ?", expired).Delete(&model.TrafficUsageDay{}).Error
		})
	})
}

// trafficUsagePeriodStart is where a period begins when it is measured at now.
func trafficUsagePeriodStart(period string, now time.Time, loc *time.Location) (time.Time, bool) {
	today := dayStart(now, loc)
	switch period {
	case "24h":
		return now.Add(-24 * time.Hour), true
	case "7d":
		return now.AddDate(0, 0, -7), true
	case "30d":
		return now.AddDate(0, 0, -30), true
	case "180d":
		return now.AddDate(0, 0, -180), true
	case "365d":
		return now.AddDate(0, 0, -365), true
	case "day":
		return today, true
	case "week":
		sinceMonday := (int(today.Weekday()) + 6) % 7
		return today.AddDate(0, 0, -sinceMonday), true
	case "month":
		return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc), true
	case "halfyear":
		month := time.January
		if today.Month() >= time.July {
			month = time.July
		}
		return time.Date(today.Year(), month, 1, 0, 0, 0, 0, loc), true
	case "year":
		return time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, loc), true
	}
	return time.Time{}, false
}

// ParseTrafficUsagePeriods keeps the known period ids of a comma list, in the
// canonical order, without duplicates.
func ParseTrafficUsagePeriods(csv string) []string {
	picked := make(map[string]bool)
	for p := range strings.SplitSeq(csv, ",") {
		picked[strings.TrimSpace(p)] = true
	}
	out := make([]string, 0, len(picked))
	for _, p := range TrafficUsagePeriods {
		if picked[p] {
			out = append(out, p)
		}
	}
	return out
}

// TrafficUsageAmount is the traffic of one period in bytes.
type TrafficUsageAmount struct {
	Up   int64 `json:"up" example:"1073741824"`
	Down int64 `json:"down" example:"5368709120"`
}

// TrafficUsageRow is one client (subject = email) or server (subject = node id,
// "0" = this panel); Usage lines up with the report's Periods.
type TrafficUsageRow struct {
	Subject string               `json:"subject" example:"anna"`
	Name    string               `json:"name" example:"anna"`
	Usage   []TrafficUsageAmount `json:"usage"`
}

// TrafficUsageReport is the traffic of every server and client over the periods
// the admin picked in the settings.
type TrafficUsageReport struct {
	Periods []string          `json:"periods" example:"[\"24h\",\"30d\",\"month\"]"`
	Servers []TrafficUsageRow `json:"servers"`
	Clients []TrafficUsageRow `json:"clients"`
}

// Report sums each enabled period from the hourly and daily rows. Hourly rows
// cover the last days exactly; older days count whole, so long periods are day-accurate.
func (s *TrafficUsageService) Report(now time.Time) (*TrafficUsageReport, error) {
	csv, err := s.settingService.getString("trafficUsagePeriods")
	if err != nil {
		return nil, err
	}
	loc, err := s.settingService.GetTimeLocation()
	if err != nil {
		return nil, err
	}
	periods := ParseTrafficUsagePeriods(csv)
	return usageReport(database.GetDB(), periods, now, loc)
}

func usageReport(db *gorm.DB, periods []string, now time.Time, loc *time.Location) (*TrafficUsageReport, error) {
	type sum struct {
		Kind    string
		Subject string
		Up      int64
		Down    int64
	}
	rows := map[trafficUsageKey]*TrafficUsageRow{}
	var order []trafficUsageKey
	for i, p := range periods {
		from, _ := trafficUsagePeriodStart(p, now, loc)
		var sums []sum
		hours := db.Model(&model.TrafficUsageHour{}).
			Select("kind, subject, SUM(up) AS up, SUM(down) AS down").
			Where("bucket >= ?", from.Truncate(time.Hour).Unix()).Group("kind, subject")
		days := db.Model(&model.TrafficUsageDay{}).
			Select("kind, subject, SUM(up) AS up, SUM(down) AS down").
			Where("bucket >= ?", dayStart(from, loc).Unix()).Group("kind, subject")
		for _, q := range []*gorm.DB{hours, days} {
			var part []sum
			if err := q.Scan(&part).Error; err != nil {
				return nil, err
			}
			sums = append(sums, part...)
		}
		for _, s := range sums {
			k := trafficUsageKey{kind: s.Kind, subject: s.Subject}
			row := rows[k]
			if row == nil {
				row = &TrafficUsageRow{Subject: s.Subject, Name: s.Subject, Usage: make([]TrafficUsageAmount, len(periods))}
				rows[k] = row
				order = append(order, k)
			}
			row.Usage[i].Up += s.Up
			row.Usage[i].Down += s.Down
		}
	}
	report := &TrafficUsageReport{Periods: periods, Servers: []TrafficUsageRow{}, Clients: []TrafficUsageRow{}}
	slices.SortFunc(order, func(a, b trafficUsageKey) int { return strings.Compare(a.subject, b.subject) })
	for _, k := range order {
		if k.kind == model.TrafficUsageNode {
			report.Servers = append(report.Servers, *rows[k])
		} else {
			report.Clients = append(report.Clients, *rows[k])
		}
	}
	if err := nameUsageServers(db, report.Servers); err != nil {
		return nil, err
	}
	return report, nil
}

func nameUsageServers(db *gorm.DB, servers []TrafficUsageRow) error {
	var nodes []model.Node
	if err := db.Select("id", "name").Find(&nodes).Error; err != nil {
		return err
	}
	names := make(map[string]string, len(nodes))
	for _, n := range nodes {
		names[strconv.Itoa(n.Id)] = n.Name
	}
	for i := range servers {
		if name, ok := names[servers[i].Subject]; ok {
			servers[i].Name = name
		} else if servers[i].Subject == localUsageSubject {
			servers[i].Name = ""
		}
	}
	return nil
}
