package controller

import (
	"net"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// CascadeController serves /panel/api/cascades: entry→exit server chains.
type CascadeController struct {
	cascadeService service.CascadeService
}

func NewCascadeController(g *gin.RouterGroup) *CascadeController {
	a := &CascadeController{}
	g.GET("/list", a.list)
	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	return a
}

// requestHost is this panel's address as the admin reaches it; a cascade whose
// exit is this panel points the entry at it.
func requestHost(c *gin.Context) string {
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

func loginUserID(c *gin.Context) int {
	if u := session.GetLoginUser(c); u != nil {
		return u.Id
	}
	return 1
}

func (a *CascadeController) list(c *gin.Context) {
	rows, err := a.cascadeService.List()
	jsonObj(c, rows, err)
}

func (a *CascadeController) add(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.CascadeRequest](c)
	if !ok {
		return
	}
	row, err := a.cascadeService.Create(c.Request.Context(), req, loginUserID(c), requestHost(c))
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.cascade.toasts.saved"), row, err)
}

func (a *CascadeController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.cascade.toasts.saved"), err)
		return
	}
	req, ok := middleware.BindAndValidate[service.CascadeRequest](c)
	if !ok {
		return
	}
	row, err := a.cascadeService.Update(c.Request.Context(), id, req, loginUserID(c), requestHost(c))
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.cascade.toasts.saved"), row, err)
}

func (a *CascadeController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.cascade.toasts.deleted"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.cascade.toasts.deleted"), a.cascadeService.Delete(c.Request.Context(), id))
}
