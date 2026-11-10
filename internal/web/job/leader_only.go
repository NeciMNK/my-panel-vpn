package job

import (
	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type leaderOnlyJob struct{ cron.Job }

// LeaderOnly runs j only on the leading master, for work that must not run
// once per master: node sync, traffic resets, cascade pins, reports.
func LeaderOnly(j cron.Job) cron.Job { return leaderOnlyJob{j} }

func (l leaderOnlyJob) Run() {
	if service.IsClusterLeader() {
		l.Job.Run()
	}
}
