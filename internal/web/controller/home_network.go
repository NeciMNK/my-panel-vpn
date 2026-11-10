package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/homenet"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// HomeNetworkController serves /panel/api/homeNetworks: home LANs behind a
// tunnel and who may reach which of their subnets and IPs.
type HomeNetworkController struct {
	homeService service.HomeNetworkService
}

func NewHomeNetworkController(g *gin.RouterGroup) *HomeNetworkController {
	a := &HomeNetworkController{}
	g.GET("/list", a.list)
	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	return a
}

func (a *HomeNetworkController) list(c *gin.Context) {
	rows, err := a.homeService.List()
	jsonObj(c, rows, err)
}

func (a *HomeNetworkController) add(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.HomeNetworkRequest](c)
	if !ok {
		return
	}
	row, err := a.homeService.Create(c.Request.Context(), req, loginUserID(c), requestHost(c))
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.home.toasts.saved"), row, err)
}

func (a *HomeNetworkController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.home.toasts.saved"), err)
		return
	}
	req, ok := middleware.BindAndValidate[service.HomeNetworkRequest](c)
	if !ok {
		return
	}
	row, err := a.homeService.Update(c.Request.Context(), id, req, loginUserID(c), requestHost(c))
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.home.toasts.saved"), row, err)
}

func (a *HomeNetworkController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.home.toasts.deleted"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.home.toasts.deleted"), a.homeService.Delete(c.Request.Context(), id))
}

// homeTunnel runs on a gateway node: the master hands it the OpenVPN/L2TP
// tunnel it must keep up (or tear down).
func (a *XraySettingController) homeTunnel(c *gin.Context) {
	t := &homenet.Tunnel{}
	if err := c.ShouldBindJSON(t); err != nil {
		jsonMsg(c, "home tunnel", err)
		return
	}
	st, err := homenet.ApplyWithin(t)
	jsonObj(c, st, err)
}

func (a *XraySettingController) homeTunnelStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "home tunnel", err)
		return
	}
	jsonObj(c, homenet.Status(id), nil)
}

// reconcileHomeNetworks puts home network rules on servers added or re-enabled
// since the last apply, in the background like the country cascades.
func reconcileHomeNetworks() {
	runNodeReconcile((&service.HomeNetworkService{}).ReconcileHomeNetworks)
}
