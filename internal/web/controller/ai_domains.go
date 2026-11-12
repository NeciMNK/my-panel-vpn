package controller

import (
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"

	"github.com/gin-gonic/gin"
)

// AIDomainsController edits the AI pin's site list and answers clients' suggestions for it.
type AIDomainsController struct {
	aiDomainService service.AIDomainService
	tgbot           tgbot.Tgbot
}

func NewAIDomainsController(g *gin.RouterGroup) *AIDomainsController {
	a := &AIDomainsController{}
	g.GET("/list", a.view)
	g.POST("/save", a.save)
	g.POST("/suggestions/answer", a.answer)
	return a
}

func (a *AIDomainsController) view(c *gin.Context) {
	view, err := a.aiDomainService.View()
	jsonObj(c, view, err)
}

type aiDomainsBody struct {
	Domains string `json:"domains"`
}

func (a *AIDomainsController) save(c *gin.Context) {
	var body aiDomainsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.aiDomains.saved"), a.aiDomainService.Save(body.Domains))
}

type aiSuggestionAnswerBody struct {
	Id     int    `json:"id"`
	Status string `json:"status"`
}

func (a *AIDomainsController) answer(c *gin.Context) {
	var body aiSuggestionAnswerBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	sg, claimed, err := a.aiDomainService.Resolve(body.Id, body.Status)
	msg := I18nWeb(c, "pages.aiDomains.alreadyAnswered")
	if err == nil && claimed {
		a.tgbot.NotifyAISuggestionAnswer(sg)
		msg = I18nWeb(c, "pages.aiDomains.answered")
	}
	jsonMsgObj(c, msg, sg, err)
}
