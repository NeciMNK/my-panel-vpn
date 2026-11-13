package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// defaultFreePlanGB matches the tgFreePlanGB default, for a setting that cannot be read.
const defaultFreePlanGB = 10

// ExpiredFreePlanJob moves clients whose term ended onto the free plan instead of
// leaving them switched off.
type ExpiredFreePlanJob struct {
	clientService  service.ClientService
	inboundService service.InboundService
	settingService service.SettingService
	xrayService    service.XrayService
}

func NewExpiredFreePlanJob() *ExpiredFreePlanJob {
	return new(ExpiredFreePlanJob)
}

func (j *ExpiredFreePlanJob) Run() {
	gb, err := j.settingService.GetTgFreePlanGB()
	if err != nil || gb <= 0 {
		gb = defaultFreePlanGB
	}
	needRestart, moved, err := j.clientService.MoveExpiredToFreePlan(&j.inboundService, gb, time.Now())
	if needRestart {
		j.xrayService.SetToNeedRestart()
	}
	if len(moved) > 0 {
		logger.Infof("expired clients moved to the %d GB free plan: %v", gb, moved)
	}
	if err != nil {
		logger.Warning(err)
	}
}
