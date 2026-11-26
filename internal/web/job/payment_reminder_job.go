package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"
)

// PaymentReminderJob sends clients the payment reminders they chose in the bot.
type PaymentReminderJob struct {
	tgbotService tgbot.Tgbot
}

func NewPaymentReminderJob() *PaymentReminderJob {
	return new(PaymentReminderJob)
}

func (j *PaymentReminderJob) Run() {
	j.tgbotService.SendPaymentReminders(time.Now())
}
