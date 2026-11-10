package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/integration"
)

// WarpSwitchController serves /panel/api/warpSwitch: one WARP switch per server.
type WarpSwitchController struct {
	switches service.WarpSwitchService
	warp     integration.WarpService
}

func NewWarpSwitchController(g *gin.RouterGroup) *WarpSwitchController {
	a := &WarpSwitchController{}
	g.GET("/list", a.list)
	g.POST("/set", a.set)
	return a
}

func (a *WarpSwitchController) list(c *gin.Context) {
	rows, err := a.switches.List()
	jsonObj(c, rows, err)
}

func (a *WarpSwitchController) set(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.WarpSwitchRequest](c)
	if !ok {
		return
	}
	row, err := a.switches.Set(c.Request.Context(), req, &a.warp)
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.warp.toasts.saved"), row, err)
}
