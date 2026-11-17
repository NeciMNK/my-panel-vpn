package service

import (
	"fmt"
	"slices"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// lapsedClientsCond matches terms that ended and will not renew themselves (arg: nowMs);
// a renewing client is autoRenewClients' until its reset_max runs out.
const lapsedClientsCond = `expiry_time > 0 AND expiry_time <= ?
	AND NOT ((reset > 0 OR reset_day > 0 OR reset_weekday > 0) AND (reset_max <= 0 OR reset_count < reset_max))`

// MoveExpiredToFreePlan puts every client whose term ended on the free plan: freeGB a
// month with no end date, usage zeroed, switched back on unless an operator switched it off.
func (s *ClientService) MoveExpiredToFreePlan(inboundSvc *InboundService, freeGB int, now time.Time) (bool, []string, error) {
	var lapsed []xray.ClientTraffic
	if err := database.GetDB().Model(xray.ClientTraffic{}).
		Where(lapsedClientsCond, now.UnixMilli()).Find(&lapsed).Error; err != nil {
		return false, nil, err
	}
	var switchedOff []xray.ClientTraffic
	for _, row := range lapsed {
		if !row.Enable {
			switchedOff = append(switchedOff, row)
		}
	}
	lifecycleOff, err := lifecycleDisabledEmails(database.GetDB(), switchedOff)
	if err != nil {
		return false, nil, err
	}
	needRestart := false
	var moved []string
	var errs []error
	for _, row := range lapsed {
		if _, off := lifecycleOff[row.Email]; !row.Enable && !off {
			continue
		}
		nr, err := s.moveToFreePlan(inboundSvc, row.Email, freeGB)
		needRestart = needRestart || nr
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", row.Email, err))
			continue
		}
		moved = append(moved, row.Email)
	}
	slices.Sort(moved)
	if len(errs) > 0 {
		return needRestart, moved, fmt.Errorf("move expired clients to the free plan: %v", errs)
	}
	return needRestart, moved, nil
}

func (s *ClientService) moveToFreePlan(inboundSvc *InboundService, email string, freeGB int) (bool, error) {
	// Zeroed first: the old term's usage must not spend the new allowance.
	needRestart, err := s.ResetTrafficByEmail(inboundSvc, email)
	if err != nil {
		return needRestart, err
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return needRestart, err
	}
	free := rec.ToClient()
	free.Enable = true
	free.ExpiryTime = 0
	free.TotalGB = int64(freeGB) << 30
	free.TrafficReset, free.TrafficResetDay = "monthly", 1
	free.Reset, free.ResetDay, free.ResetWeekday, free.ResetMax = 0, 0, 0, 0
	nr, err := s.Update(inboundSvc, rec.Id, *free, rec.LimitHwid)
	needRestart = needRestart || nr
	if err != nil {
		return needRestart, err
	}
	billing, err := s.GetBillingByEmail(email)
	if err != nil {
		return needRestart, err
	}
	billing.FreePlan = true
	return needRestart, s.SaveBilling(billing)
}
