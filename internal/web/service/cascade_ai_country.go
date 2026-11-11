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
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("email IN ?", emails).Delete(&model.CascadeAICountry{}).Error; err != nil {
			return err
		}
		for _, email := range emails {
			if err := tx.Create(&model.CascadeAICountry{Email: email, Country: country}).Error; err != nil {
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
	var row model.CascadeAICountry
	if err := database.GetDB().Where("email IN ?", emails).Order("id desc").Limit(1).Find(&row).Error; err != nil {
		return ""
	}
	return row.Country
}

// aiCountryExits maps each user with an AI country to their pin in it.
func aiCountryExits(fixed []model.CascadeFixedExit, countryOf map[int]string) (map[string]int, error) {
	var rows []model.CascadeAICountry
	if err := database.GetDB().Find(&rows).Error; err != nil {
		return nil, err
	}
	want := make(map[string]string, len(rows))
	for _, r := range rows {
		want[r.Email] = r.Country
	}
	out := map[string]int{}
	for _, f := range fixed {
		if c, ok := want[f.Email]; ok && countryOf[f.NodeId] == c {
			out[f.Email] = f.NodeId
		}
	}
	return out, nil
}
