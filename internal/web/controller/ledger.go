package controller

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/gin-gonic/gin"
)

// LedgerController serves the admin's VPN profit/loss book.
type LedgerController struct {
	ledgerService service.LedgerService
}

func NewLedgerController(g *gin.RouterGroup) *LedgerController {
	a := &LedgerController{}
	a.initRouter(g)
	return a
}

func (a *LedgerController) initRouter(g *gin.RouterGroup) {
	g.GET("/report", a.report)
	g.POST("/servers/save", a.saveServer)
	g.POST("/servers/delete", a.deleteServer)
	g.POST("/entries/add", a.addEntry)
	g.POST("/entries/delete", a.deleteEntry)
	g.POST("/nonPaying", a.setNonPaying)
}

func (a *LedgerController) report(c *gin.Context) {
	month := c.Query("month")
	if month == "" {
		month = service.LedgerMonthOf(time.Now())
	}
	report, err := a.ledgerService.Report(month)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, report, nil)
}

func (a *LedgerController) saveServer(c *gin.Context) {
	var server model.LedgerServer
	if err := c.ShouldBindJSON(&server); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.ledgerService.SaveServer(&server); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, server, nil)
}

type ledgerIDBody struct {
	Id int `json:"id"`
}

func (a *LedgerController) deleteServer(c *gin.Context) {
	var body ledgerIDBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.ledger.toasts.deleted"), a.ledgerService.DeleteServer(body.Id))
}

func (a *LedgerController) addEntry(c *gin.Context) {
	var entry model.LedgerEntry
	if err := c.ShouldBindJSON(&entry); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.ledgerService.AddEntry(&entry); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, entry, nil)
}

func (a *LedgerController) deleteEntry(c *gin.Context) {
	var body ledgerIDBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.ledger.toasts.deleted"), a.ledgerService.DeleteEntry(body.Id))
}

// ledgerNonPayingBody targets either one client (email) or a whole group.
type ledgerNonPayingBody struct {
	Email     string `json:"email"`
	Group     string `json:"group"`
	NonPaying bool   `json:"nonPaying"`
}

func (a *LedgerController) setNonPaying(c *gin.Context) {
	var body ledgerNonPayingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	var err error
	if body.Email != "" {
		err = a.ledgerService.SetClientNonPaying(body.Email, body.NonPaying)
	} else {
		err = a.ledgerService.SetGroupNonPaying(body.Group, body.NonPaying)
	}
	jsonMsg(c, I18nWeb(c, "pages.ledger.toasts.saved"), err)
}
