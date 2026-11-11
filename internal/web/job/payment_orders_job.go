package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"
)

// PaymentOrdersJob polls the acquiring provider for open invoices and applies paid ones.
type PaymentOrdersJob struct {
	tgbotService tgbot.Tgbot
}

func NewPaymentOrdersJob() *PaymentOrdersJob {
	return new(PaymentOrdersJob)
}

func (j *PaymentOrdersJob) Run() {
	j.tgbotService.CheckPaymentOrders(time.Now())
}
