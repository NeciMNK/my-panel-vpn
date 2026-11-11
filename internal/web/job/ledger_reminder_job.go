package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"
)

// LedgerReminderJob tells admins in Telegram about server bills due by tomorrow.
type LedgerReminderJob struct {
	tgbotService tgbot.Tgbot
}

func NewLedgerReminderJob() *LedgerReminderJob {
	return new(LedgerReminderJob)
}

func (j *LedgerReminderJob) Run() {
	j.tgbotService.RemindServerBills(time.Now())
}
