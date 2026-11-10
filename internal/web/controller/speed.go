package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/shaper"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SpeedController serves /panel/api/speed: users' minimum and maximum speed
// and the link speed of each server.
type SpeedController struct {
	speed service.SpeedService
}

func NewSpeedController(g *gin.RouterGroup) *SpeedController {
	a := &SpeedController{}
	g.GET("/overview", a.overview)
	g.POST("/client", a.setClient)
	g.POST("/capacity", a.setCapacity)
	return a
}

func (a *SpeedController) overview(c *gin.Context) {
	out, err := a.speed.Overview()
	jsonObj(c, out, err)
}

func (a *SpeedController) setClient(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.ClientSpeedRequest](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.speed.toasts.saved"), a.speed.SetClient(req))
}

func (a *SpeedController) setCapacity(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.ServerCapacityRequest](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.speed.toasts.saved"), a.speed.SetCapacity(req))
}

// speedPlan runs on every server: the master hands it the per-IP caps to
// enforce with tc.
func (a *XraySettingController) speedPlan(c *gin.Context) {
	plan := shaper.Plan{}
	if err := c.ShouldBindJSON(&plan); err != nil {
		jsonMsg(c, "speed plan", err)
		return
	}
	st, err := shaper.Apply(plan, shaper.OwnerMaster)
	jsonObj(c, st, err)
}
