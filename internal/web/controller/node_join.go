package controller

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/nodejoin"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// NodeJoinController serves the unauthenticated half of "add a node with one
// command": the installer, this panel's build and the registration call.
// Everything past the installer requires a one-time join token.
type NodeJoinController struct {
	nodeService service.NodeService
}

func NewNodeJoinController(g *gin.RouterGroup) *NodeJoinController {
	a := &NodeJoinController{}
	j := g.Group("/node-join")
	j.GET("/install.sh", a.installScript)
	j.GET("/bundle", a.bundle)
	j.POST("/register", middleware.MaxBodyBytes(16<<10), a.register)
	return a
}

func (a *NodeJoinController) installScript(c *gin.Context) {
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", nodejoin.InstallScript())
}

func (a *NodeJoinController) bundle(c *gin.Context) {
	if err := a.nodeService.CheckJoinToken(c.Query("join")); err != nil {
		c.String(http.StatusForbidden, "%s\n", err.Error())
		return
	}
	if arch := c.Query("arch"); arch != nodejoin.Arch() {
		c.String(http.StatusConflict, "this master runs %s builds, the node is %s\n", nodejoin.Arch(), arch)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		c.String(http.StatusInternalServerError, "locate panel executable: %v\n", err)
		return
	}
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="x-ui-node.tar.gz"`)
	c.Status(http.StatusOK)
	if err := nodejoin.WriteBundle(c.Writer, exe, config.GetBinFolderPath()); err != nil {
		logger.Warning("node-join bundle:", err)
	}
}

func (a *NodeJoinController) register(c *gin.Context) {
	var req service.NodeJoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "msg": "malformed join request"})
		return
	}
	view, err := a.nodeService.JoinNode(&req, getRemoteIp(c))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrNodeJoinTokenInvalid) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"success": false, "msg": err.Error()})
		return
	}
	logger.Info(fmt.Sprintf("node %q joined from %s", view.Name, getRemoteIp(c)))
	reconcileCountryCascades()
	reconcileHomeNetworks()
	c.JSON(http.StatusOK, gin.H{"success": true, "msg": "joined", "obj": gin.H{"id": view.Id, "name": view.Name}})
}

// joinToken mints a one-time token for the "add node with one command" dialog.
func (a *NodeController) joinToken(c *gin.Context) {
	view, err := a.nodeService.CreateJoinToken(nodejoin.Arch())
	jsonObj(c, view, err)
}
