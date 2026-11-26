package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// TrafficUsageFlushJob writes the usage this panel observed into the hourly
// history. It runs on every master: each one flushes only what it saw itself.
type TrafficUsageFlushJob struct {
	usage service.TrafficUsageService
}

func NewTrafficUsageFlushJob() *TrafficUsageFlushJob { return &TrafficUsageFlushJob{} }

func (j *TrafficUsageFlushJob) Run() {
	if err := j.usage.Flush(); err != nil {
		logger.Warning("traffic usage:", err)
	}
}

// TrafficUsageRollupJob folds old hourly history into days in the panel's timezone.
type TrafficUsageRollupJob struct {
	usage    service.TrafficUsageService
	location *time.Location
}

func NewTrafficUsageRollupJob(location *time.Location) *TrafficUsageRollupJob {
	return &TrafficUsageRollupJob{location: location}
}

func (j *TrafficUsageRollupJob) Run() {
	if err := j.usage.Rollup(time.Now(), j.location); err != nil {
		logger.Warning("traffic usage rollup:", err)
	}
}
