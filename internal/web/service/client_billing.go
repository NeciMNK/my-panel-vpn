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
		DoUpdates: clause.AssignmentColumns([]string{"trusted", "free_plan", "grace_days", "last_grace_at", "non_paying", "friend", "credit"}),
	}).Create(billing).Error
}

// SetPendingPayment records the term a client reports paying; SaveBilling never
// touches it, so only ClaimPendingPayment clears it.
func (s *ClientService) SetPendingPayment(clientID, months int) error {
	return database.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"pending_months"}),
	}).Create(&model.ClientBilling{ClientId: clientID, PendingMonths: months}).Error
}

// ClaimPendingPayment clears a pending report for months and says whether this
// caller cleared it, so concurrent or repeated confirms apply the payment once.
func (s *ClientService) ClaimPendingPayment(clientID, months int) (bool, error) {
	res := database.GetDB().Model(&model.ClientBilling{}).
		Where("client_id = ? AND pending_months = ?", clientID, months).
		Update("pending_months", 0)
	return res.RowsAffected == 1, res.Error
}

// SetFriend marks or unmarks a friend and moves the client into or out of the friends
// group; unmarking returns only a client still in friends to paid or users.
func (s *ClientService) SetFriend(email string, friend bool) error {
	billing, err := s.GetBillingByEmail(email)
	if err != nil {
		return err
	}
	billing.Friend = friend
	if err := s.SaveBilling(billing); err != nil {
		return err
	}
	if friend {
		_, err = s.AddToGroup([]string{email}, model.ClientGroupFriends)
		return err
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil || rec.Group != model.ClientGroupFriends {
		return err
	}
	group, err := regularClientGroup(email, billing)
	if err != nil {
		return err
	}
	_, err = s.AddToGroup([]string{email}, group)
	return err
}

// regularClientGroup is paid for a client with a booked payment and no free plan
// since, users for everyone else.
func regularClientGroup(email string, billing *model.ClientBilling) (string, error) {
	if billing.FreePlan {
		return model.ClientGroupUsers, nil
	}
	var payments int64
	if err := database.GetDB().Model(&model.LedgerEntry{}).
		Where("email = ? AND kind = ?", email, model.LedgerIncome).Count(&payments).Error; err != nil {
		return "", err
	}
	if payments > 0 {
		return model.ClientGroupPaid, nil
	}
	return model.ClientGroupUsers, nil
}

// MarkPaid moves a free user who just paid into paid; friends, tech and groups the
// admin chose keep theirs.
func (s *ClientService) MarkPaid(email string) error {
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return err
	}
	if rec.Group != "" && rec.Group != model.ClientGroupUsers {
		return nil
	}
	_, err = s.AddToGroup([]string{email}, model.ClientGroupPaid)
	return err
}
