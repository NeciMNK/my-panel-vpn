package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// MasterClusterController serves /panel/api/masters: the masters sharing this
// panel's database and which of them leads.
type MasterClusterController struct {
	cluster service.MasterClusterService
}

// ForgetMasterRequest names a master that is down for good.
type ForgetMasterRequest struct {
	Name string `json:"name" validate:"required" example:"master-2"`
}

func NewMasterClusterController(g *gin.RouterGroup) *MasterClusterController {
	a := &MasterClusterController{}
	g.GET("/list", a.list)
	g.POST("/forget", a.forget)
	return a
}

func (a *MasterClusterController) list(c *gin.Context) {
	out, err := a.cluster.List()
	jsonObj(c, out, err)
}

func (a *MasterClusterController) forget(c *gin.Context) {
	req, ok := middleware.BindAndValidate[ForgetMasterRequest](c)
	if !ok {
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.masters.toasts.forgotten"), a.cluster.Forget(req.Name))
}
