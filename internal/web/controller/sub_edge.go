package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SubEdgeController serves /panel/api/subEdges: nodes that serve this panel's
// subscriptions, and on a node the endpoints its masters configure it through.
type SubEdgeController struct {
	subEdgeService service.SubEdgeService
	settingService service.SettingService
}

func NewSubEdgeController(g *gin.RouterGroup) *SubEdgeController {
	a := &SubEdgeController{}
	g.GET("/list", a.list)
	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/node/configure", a.configure)
	g.POST("/node/tunnel", a.tunnel)
	g.POST("/node/cache", a.cache)
	g.POST("/node/dns", a.nodeDNS)
	g.GET("/dns", a.dns)
	g.POST("/dns", a.saveDNS)
	return a
}

func (a *SubEdgeController) list(c *gin.Context) {
	rows, err := a.subEdgeService.List()
	jsonObj(c, rows, err)
}

func (a *SubEdgeController) add(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.SubEdgeRequest](c)
	if !ok {
		return
	}
	row, err := a.subEdgeService.Create(c.Request.Context(), *req)
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), row, err)
}

func (a *SubEdgeController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), err)
		return
	}
	req, ok := middleware.BindAndValidate[service.SubEdgeRequest](c)
	if !ok {
		return
	}
	row, err := a.subEdgeService.Update(c.Request.Context(), id, *req)
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), row, err)
}

func (a *SubEdgeController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err == nil {
		err = a.subEdgeService.Delete(c.Request.Context(), id)
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.deleted"), err)
}

func (a *SubEdgeController) configure(c *gin.Context) {
	cfg, ok := middleware.BindAndValidate[subedge.ListenerConfig](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), a.settingService.ConfigureSubEdgeNode(*cfg))
}

func (a *SubEdgeController) tunnel(c *gin.Context) {
	if err := service.SubEdgeNode().AcceptTunnel(c.Writer, c.Request); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.tunnel"), err)
	}
}

func (a *SubEdgeController) cache(c *gin.Context) {
	update, ok := middleware.BindAndValidate[subedge.CacheUpdate](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), service.SubEdgeNode().UpdateCache(*update))
}

func (a *SubEdgeController) nodeDNS(c *gin.Context) {
	plan, ok := middleware.BindAndValidate[subedge.DNSPlan](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.toasts.saved"), a.settingService.ConfigureSubEdgeDNSNode(*plan))
}

func (a *SubEdgeController) dns(c *gin.Context) {
	view, err := a.subEdgeService.DNSView()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.dns.title"), err)
		return
	}
	jsonObj(c, view, nil)
}

func (a *SubEdgeController) saveDNS(c *gin.Context) {
	req, ok := middleware.BindAndValidate[service.SubEdgeDNSRequest](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.subEdge.dns.toasts.saved"), a.subEdgeService.SaveDNS(*req))
}
