package service

import (
	"context"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// What a pinned exit carries: all the user's traffic, or AI sites only.
const (
	FixedExitAllTraffic = "all"
	FixedExitAIOnly     = "ai"
)

// CascadeFixedExitRequest pins (or unpins) one user to one exit server.
type CascadeFixedExitRequest struct {
	Email       string `json:"email" example:"anna"`
	NodeId      int    `json:"nodeId" example:"2"`
	TrafficMode string `json:"trafficMode" validate:"omitempty,oneof=all ai" example:"all"`
}

// CascadeFixedExitRow is one pin as the panel lists it, with what it carries.
type CascadeFixedExitRow struct {
	Id          int    `json:"id" example:"1"`
	Email       string `json:"email" example:"anna"`
	NodeId      int    `json:"nodeId" example:"2"`
	TrafficMode string `json:"trafficMode" validate:"oneof=all ai" example:"all"`
	CreatedAt   int64  `json:"createdAt" example:"1762862400"`
}

func (s *CascadeService) ListFixedExits() ([]CascadeFixedExitRow, error) {
	var fixed []model.CascadeFixedExit
	if err := database.GetDB().Order("id asc").Find(&fixed).Error; err != nil {
		return nil, err
	}
	plan, err := loadAIExitPlan()
	if err != nil {
		return nil, err
	}
	rows := make([]CascadeFixedExitRow, 0, len(fixed))
	for _, f := range fixed {
		mode := FixedExitAllTraffic
		if plan.isDomainPin(f) {
			mode = FixedExitAIOnly
		}
		rows = append(rows, CascadeFixedExitRow{Id: f.Id, Email: f.Email, NodeId: f.NodeId, TrafficMode: mode, CreatedAt: f.CreatedAt})
	}
	return rows, nil
}

// SetFixedExit pins a user to an exit, replacing their pin in the same country,
// and re-routes them at once instead of on the next sticky pass.
func (s *CascadeService) SetFixedExit(ctx context.Context, req *CascadeFixedExitRequest) error {
	email, err := fixedExitEmail(req)
	if err != nil {
		return err
	}
	if req.NodeId != 0 {
		if ok, err := (&NodeService{}).NodeExists(req.NodeId); err != nil || !ok {
			return common.NewErrorf("node %d does not exist", req.NodeId)
		}
	}
	country := nodeCountry(req.NodeId)
	if req.TrafficMode == FixedExitAIOnly && country == "" {
		return common.NewErrorf("AI sites only needs a server with a country")
	}
	err = replaceFixedExit(email, req.NodeId, func(id int) bool { return country != "" && nodeCountry(id) == country })
	if err != nil {
		return err
	}
	if err := setFixedExitTraffic(email, country, req.TrafficMode); err != nil {
		return err
	}
	s.StickCascadeUsers(ctx)
	return nil
}

// setFixedExitTraffic applies an admin's mode through the user's one AI
// country, the bot AI button's switch; no mode leaves it as it was.
func setFixedExitTraffic(email, country, mode string) error {
	db := database.GetDB()
	if mode == FixedExitAIOnly {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("email = ?", email).Delete(&model.CascadeAICountry{}).Error; err != nil {
				return err
			}
			return tx.Create(&model.CascadeAICountry{Email: email, Country: country}).Error
		})
	}
	if mode == "" || country == "" {
		return nil
	}
	return db.Model(&model.CascadeAICountry{}).Where("email = ? AND country = ?", email, country).Update("all_traffic", true).Error
}

// DeleteFixedExit hands the user back to automatic choice; they keep their
// current exit under the usual hold rules.
func (s *CascadeService) DeleteFixedExit(ctx context.Context, req *CascadeFixedExitRequest) error {
	email, err := fixedExitEmail(req)
	if err != nil {
		return err
	}
	err = database.GetDB().Where("email = ? AND node_id = ?", email, req.NodeId).Delete(&model.CascadeFixedExit{}).Error
	if err != nil {
		return err
	}
	s.StickCascadeUsers(ctx)
	return nil
}

func fixedExitEmail(req *CascadeFixedExitRequest) (string, error) {
	if req == nil || strings.TrimSpace(req.Email) == "" {
		return "", common.NewError("pick a user")
	}
	return strings.TrimSpace(req.Email), nil
}

// fixedExitsIn maps each user to their pinned exit among exits; with several
// (a cascade mixing countries) an all-traffic AI pin wins, else the latest one.
// A domain-only AI pin is no fixed exit: it routes AI services alone.
func fixedExitsIn(rows []model.CascadeFixedExit, exits []int, ai aiExitPlan) map[string]int {
	out := map[string]int{}
	best := map[string]int{}
	for _, r := range rows {
		if slices.Contains(exits, r.NodeId) && r.Id >= best[r.Email] && !ai.isDomainPin(r) {
			out[r.Email], best[r.Email] = r.NodeId, r.Id
		}
	}
	for email, id := range ai.All {
		if _, pinnedHere := out[email]; pinnedHere && slices.Contains(exits, id) {
			out[email] = id
		}
	}
	return out
}
