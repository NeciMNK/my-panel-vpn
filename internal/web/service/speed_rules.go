package service

import (
	"math"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"gorm.io/gorm"
)

// GroupSpeedRequest sets one group's speed; all 0 removes it. FloorMbps > 0
// makes the cap float between it and MaxMbps by how much speed is free.
type GroupSpeedRequest struct {
	Group     string `json:"group" example:"users"`
	MinMbps   int    `json:"minMbps" example:"0"`
	MaxMbps   int    `json:"maxMbps" example:"100"`
	FloorMbps int    `json:"floorMbps" example:"50"`
}

func (s *SpeedService) SetGroup(req *GroupSpeedRequest) error {
	if req == nil || strings.TrimSpace(req.Group) == "" {
		return common.NewError("pick a group")
	}
	if req.MinMbps < 0 || req.MaxMbps < 0 || req.FloorMbps < 0 {
		return common.NewError("speeds cannot be negative")
	}
	if req.MaxMbps > 0 && req.MinMbps > req.MaxMbps {
		return common.NewError("the minimum speed cannot be above the maximum")
	}
	if req.FloorMbps > 0 && req.MaxMbps < req.FloorMbps {
		return common.NewError("a floating cap needs a top speed at or above its floor")
	}
	if req.FloorMbps > 0 && req.MinMbps > req.FloorMbps {
		return common.NewError("the minimum speed cannot be above the floating cap's floor")
	}
	group := strings.TrimSpace(req.Group)
	db := database.GetDB()
	if req.MinMbps == 0 && req.MaxMbps == 0 {
		return db.Where("group_name = ?", group).Delete(&model.GroupSpeed{}).Error
	}
	row := &model.GroupSpeed{}
	db.Where("group_name = ?", group).First(row)
	row.Group, row.MinMbps, row.MaxMbps, row.FloorMbps = group, req.MinMbps, req.MaxMbps, req.FloorMbps
	return db.Save(row).Error
}

// speedRule is the speed one client is entitled to: their own setting, else the
// speed tier of their paid term, else their group's. Floor > 0 makes Max float.
type speedRule struct{ Min, Max, Floor int }

func (r speedRule) floats() bool { return r.Floor > 0 && r.Max > 0 }

// speedLimit is what one client gets right now: a guarantee and a cap (0 = none).
type speedLimit struct{ Min, Max int }

// speedRules maps every client with any speed rule to it.
func speedRules() (map[string]speedRule, error) {
	db := database.GetDB()
	var groups []model.GroupSpeed
	if err := db.Find(&groups).Error; err != nil {
		return nil, err
	}
	out := map[string]speedRule{}
	if len(groups) > 0 {
		byGroup := map[string]speedRule{}
		names := make([]string, 0, len(groups))
		for _, g := range groups {
			byGroup[g.Group] = speedRule{Min: g.MinMbps, Max: g.MaxMbps, Floor: g.FloorMbps}
			names = append(names, g.Group)
		}
		var members []model.ClientRecord
		if err := db.Select("email", "group_name").Where("group_name IN ?", names).Find(&members).Error; err != nil {
			return nil, err
		}
		for _, m := range members {
			if !strings.HasPrefix(m.Email, managedTagPrefix) {
				out[m.Email] = byGroup[m.Group]
			}
		}
	}
	var tiered []struct {
		Email   string
		MinMbps int
	}
	if err := db.Table("client_billings").
		Select("clients.email AS email, speed_tiers.min_mbps AS min_mbps").
		Joins("JOIN clients ON clients.id = client_billings.client_id").
		Joins("JOIN speed_tiers ON speed_tiers.id = client_billings.speed_tier_id").
		Where("client_billings.free_plan = ? AND (clients.expiry_time <= 0 OR clients.expiry_time > ?)", false, time.Now().UnixMilli()).
		Scan(&tiered).Error; err != nil {
		return nil, err
	}
	for _, r := range tiered {
		out[r.Email] = speedRule{Min: r.MinMbps}
	}
	var own []model.ClientSpeed
	if err := db.Find(&own).Error; err != nil {
		return nil, err
	}
	for _, r := range own {
		out[r.Email] = speedRule{Min: r.MinMbps, Max: r.MaxMbps}
	}
	return out, nil
}

// speedStep rounds floating caps down to it, so a user count changing by one
// does not rebuild every server's tc tree.
const speedStep = 10

// reservedMbps is, per server, the sum of the guarantees of the users it carries.
func reservedMbps(rules map[string]speedRule, users map[int][]string) map[int]int {
	out := map[int]int{}
	for id, list := range users {
		for _, e := range list {
			out[id] += rules[e].Min
		}
	}
	return out
}

// speedLimits gives every ruled user their guarantee and cap. A floating cap is
// the smallest share of any server the user loads that guarantees leave free.
func speedLimits(rules map[string]speedRule, users map[int][]string, caps map[int]int) map[string]speedLimit {
	reserved := reservedMbps(rules, users)
	share := map[int]float64{}
	serversOf := map[string][]int{}
	for id, list := range users {
		floating := 0
		for _, e := range list {
			serversOf[e] = append(serversOf[e], id)
			if rules[e].floats() {
				floating++
			}
		}
		if c, known := caps[id]; known && floating > 0 {
			share[id] = float64(c-reserved[id]) / float64(floating)
		}
	}
	out := map[string]speedLimit{}
	for e, servers := range serversOf {
		r, ok := rules[e]
		if !ok || r.Min == 0 && r.Max == 0 {
			continue
		}
		limit := speedLimit{Min: r.Min, Max: r.Max}
		if r.floats() {
			free := float64(r.Max)
			for _, id := range servers {
				if s, known := share[id]; known {
					free = math.Min(free, s)
				}
			}
			stepped := int(math.Max(free, 0)) / speedStep * speedStep
			limit.Max = min(r.Max, max(stepped, r.Floor, r.Min))
		}
		out[e] = limit
	}
	return out
}

// cascadeExitUsers lists, per exit, the online users an enabled cascade carries
// there: by their pin, or to the only exit of a one-exit cascade.
func cascadeExitUsers(cascades []model.Cascade, online map[int][]string, entered map[int]map[string]bool, pins map[int]map[string]int) map[int][]string {
	out := map[int][]string{}
	for _, c := range cascades {
		if len(c.Links) == 0 {
			continue
		}
		for _, e := range online[c.EntryNodeId] {
			if !entered[c.Id][e] {
				continue
			}
			exit, pinned := pins[c.Id][e]
			if !pinned && len(c.Links) == 1 {
				exit, pinned = c.Links[0].NodeId, true
			}
			if pinned {
				out[exit] = append(out[exit], e)
			}
		}
	}
	return out
}

// SpeedTierRequest adds (id 0) or edits a speed tier clients can buy.
type SpeedTierRequest struct {
	Id           int    `json:"id" example:"0"`
	Name         string `json:"name" example:"Plus"`
	MinMbps      int    `json:"minMbps" example:"200"`
	MonthlyPrice int    `json:"monthlyPrice" example:"100"`
}

func (s *SpeedService) SaveTier(req *SpeedTierRequest) error {
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return common.NewError("name the speed tier")
	}
	if req.MinMbps <= 0 {
		return common.NewError("a speed tier needs a guaranteed speed above 0")
	}
	if req.MonthlyPrice < 0 {
		return common.NewError("the price cannot be negative")
	}
	tier := &model.SpeedTier{Id: req.Id, Name: strings.TrimSpace(req.Name), MinMbps: req.MinMbps, MonthlyPrice: req.MonthlyPrice}
	db := database.GetDB()
	if req.Id != 0 {
		res := db.Model(&model.SpeedTier{}).Where("id = ?", req.Id).
			Updates(map[string]any{"name": tier.Name, "min_mbps": tier.MinMbps, "monthly_price": tier.MonthlyPrice})
		if res.Error == nil && res.RowsAffected == 0 {
			return common.NewError("speed tier not found")
		}
		return res.Error
	}
	return db.Create(tier).Error
}

// DeleteTier stops selling a tier; clients who bought it fall back to their group's
// speed, and reports pending with it can no longer be confirmed for it.
func (s *SpeedService) DeleteTier(id int) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ClientBilling{}).Where("speed_tier_id = ?", id).Update("speed_tier_id", 0).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&model.SpeedTier{}).Error
	})
}
