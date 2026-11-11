package service

import (
	"context"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// CascadeFixedExitRequest pins (or unpins) one user to one exit server.
type CascadeFixedExitRequest struct {
	Email  string `json:"email" example:"anna"`
	NodeId int    `json:"nodeId" example:"2"`
}

func (s *CascadeService) ListFixedExits() ([]model.CascadeFixedExit, error) {
	var rows []model.CascadeFixedExit
	err := database.GetDB().Order("id asc").Find(&rows).Error
	return rows, err
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
	err = replaceFixedExit(email, req.NodeId, func(id int) bool { return country != "" && nodeCountry(id) == country })
	if err != nil {
		return err
	}
	s.StickCascadeUsers(ctx)
	return nil
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
// (a cascade mixing countries) the AI-country pin wins, else the latest one.
func fixedExitsIn(rows []model.CascadeFixedExit, exits []int, aiExits map[string]int) map[string]int {
	out := map[string]int{}
	best := map[string]int{}
	for _, r := range rows {
		if slices.Contains(exits, r.NodeId) && r.Id >= best[r.Email] {
			out[r.Email], best[r.Email] = r.NodeId, r.Id
		}
	}
	for email, id := range aiExits {
		if _, pinnedHere := out[email]; pinnedHere && slices.Contains(exits, id) {
			out[email] = id
		}
	}
	return out
}
