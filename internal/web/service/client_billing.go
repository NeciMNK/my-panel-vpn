package service

import (
	"errors"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetBillingByEmail returns the client's billing row, or a zero row bound to its
// record when none was saved yet; an unknown email is an error.
func (s *ClientService) GetBillingByEmail(email string) (*model.ClientBilling, error) {
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return nil, err
	}
	billing := &model.ClientBilling{}
	err = database.GetDB().Where("client_id = ?", rec.Id).First(billing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.ClientBilling{ClientId: rec.Id}, nil
	}
	if err != nil {
		return nil, err
	}
	return billing, nil
}

// SaveBilling upserts on client_id, so a row read as zero and saved twice stays one row.
func (s *ClientService) SaveBilling(billing *model.ClientBilling) error {
	return database.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"trusted", "free_plan", "grace_days", "last_grace_at", "non_paying"}),
	}).Create(billing).Error
}
