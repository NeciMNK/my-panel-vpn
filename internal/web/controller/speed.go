package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/shaper"
	"github.com/mhsanaei/3x-ui/v3/internal/speedtest"
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
	g.POST("/test", a.startTest)
	g.GET("/tests", a.testHistory)
	g.POST("/testSettings", a.setTestSettings)
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

func (a *SpeedController) startTest(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.SpeedTestRequest](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.speed.toasts.testStarted"), a.speed.StartTest(req.NodeId))
}

func (a *SpeedController) testHistory(c *gin.Context) {
	nodeID, _ := strconv.Atoi(c.Query("nodeId"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	out, err := a.speed.TestHistory(nodeID, limit)
	jsonObj(c, out, err)
}

func (a *SpeedController) setTestSettings(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.SpeedTestSettings](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.speed.toasts.saved"), a.speed.SetTestSettings(req))
}

// startSpeedTest and speedTestState run on every server: the master starts a
// measurement of the server's own link, then polls it until it ends.
func (a *XraySettingController) startSpeedTest(c *gin.Context) {
	jsonObj(c, speedtest.Local.Start(), nil)
}

func (a *XraySettingController) speedTestState(c *gin.Context) {
	jsonObj(c, speedtest.Local.State(), nil)
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
