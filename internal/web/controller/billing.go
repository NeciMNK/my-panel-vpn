package controller

import (
	"strconv"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/gin-gonic/gin"
)

// BillingController lets outside systems (a payment provider's webhook, a shop)
// read a client's payment state and apply a payment exactly like the bot's confirm.
type BillingController struct {
	billingService service.BillingService
}

func NewBillingController(g *gin.RouterGroup) *BillingController {
	a := &BillingController{}
	g.GET("/client", a.client)
	g.POST("/pay", a.pay)
	return a
}

func (a *BillingController) client(c *gin.Context) {
	view, err := a.billingService.GetClientBilling(c.Query("email"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, view, nil)
}

type billingPayBody struct {
	Email  string `json:"email"`
	Months int    `json:"months"`
	Note   string `json:"note"`
}

func (a *BillingController) pay(c *gin.Context) {
	var body billingPayBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if body.Note == "" {
		body.Note = I18nWeb(c, "tgbot.ledger.paymentNote", "Months=="+strconv.Itoa(body.Months))
	}
	result, err := a.billingService.ApplyPayment(body.Email, body.Months, body.Note, time.Now())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, result, nil)
}
