package controller

import (
	"errors"

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

// ReorderMastersRequest lists every master, the new primary first.
type ReorderMastersRequest struct {
	Names []string `json:"names" validate:"required,min=1,dive,required" example:"master-2,master-1"`
}

// AddMasterRequest names a master about to join and the address it serves.
type AddMasterRequest struct {
	Name string `json:"name" validate:"required,max=64" example:"master-3"`
	Url  string `json:"url" validate:"omitempty,url,max=255" example:"https://panel-3.example.com:2053"`
	// DbHost is how the new master reaches the database when this one uses 127.0.0.1.
	DbHost string `json:"dbHost" validate:"omitempty,hostname_rfc1123|ip,max=255" example:"10.0.0.1"`
}

func NewMasterClusterController(g *gin.RouterGroup) *MasterClusterController {
	a := &MasterClusterController{}
	g.GET("/list", a.list)
	g.POST("/forget", a.forget)
	g.POST("/reorder", a.reorder)
	g.POST("/add", a.add)
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
	a.reply(c, "pages.nodes.masters.toasts.forgotten", nil, a.cluster.Forget(req.Name))
}

func (a *MasterClusterController) reorder(c *gin.Context) {
	req, ok := middleware.BindAndValidate[ReorderMastersRequest](c)
	if !ok {
		return
	}
	a.reply(c, "pages.nodes.masters.toasts.reordered", nil, a.cluster.Reorder(req.Names))
}

func (a *MasterClusterController) add(c *gin.Context) {
	req, ok := middleware.BindAndValidate[AddMasterRequest](c)
	if !ok {
		return
	}
	setup, err := a.cluster.Add(req.Name, req.Url, req.DbHost)
	a.reply(c, "pages.nodes.masters.toasts.added", setup, err)
}

// reply turns a refused change into a sentence the admin can act on.
func (a *MasterClusterController) reply(c *gin.Context, okKey string, obj any, err error) {
	if err == nil {
		jsonMsgObj(c, I18nWeb(c, okKey), obj, nil)
		return
	}
	var refusal *service.MasterChangeError
	if errors.As(err, &refusal) {
		err = errors.New(I18nWeb(c, "pages.nodes.masters.refusals."+refusal.Reason, "Name=="+refusal.Master))
	}
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.masters.toasts.refused"), nil, err)
}
