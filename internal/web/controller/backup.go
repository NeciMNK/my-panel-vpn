package controller

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// BackupController sets up backups to another server over SFTP and moves a
// lost node onto a fresh server from its latest backup.
type BackupController struct {
	service service.BackupService
}

func NewBackupController(g *gin.RouterGroup) *BackupController {
	a := &BackupController{}
	g = g.Group("/backup")
	g.GET("/settings", a.getSettings)
	g.POST("/settings", a.setSettings)
	g.POST("/test", a.test)
	g.POST("/run/:what", a.run)
	g.GET("/status", a.status)
	g.GET("/node/:id", a.nodeBackups)
	g.POST("/restoreNode/:id", a.restoreNode)
	g.GET("/restoreStatus", a.restoreStatus)
	return a
}

func (a *BackupController) getSettings(c *gin.Context) {
	settings, err := a.service.Settings()
	jsonObj(c, settings, err)
}

func (a *BackupController) setSettings(c *gin.Context) {
	var req service.BackupSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.backup.toasts.save"), err)
		return
	}
	if err := a.service.SaveSettings(req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.backup.toasts.save"), err)
		return
	}
	settings, err := a.service.Settings()
	jsonObj(c, settings, err)
}

func (a *BackupController) test(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if _, err := a.service.Test(ctx); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.backup.toasts.test"), err)
		return
	}
	settings, err := a.service.Settings()
	jsonObj(c, settings, err)
}

func (a *BackupController) run(c *gin.Context) {
	var run func(context.Context) error
	switch c.Param("what") {
	case "db":
		run = a.service.BackupDB
	case "nodes":
		run = a.service.BackupNodes
	default:
		jsonMsg(c, I18nWeb(c, "pages.settings.backup.toasts.run"), errors.New("unknown backup: use db or nodes"))
		return
	}
	go func() { _ = run(context.Background()) }()
	jsonObj(c, a.service.Status(), nil)
}

func (a *BackupController) status(c *gin.Context) {
	jsonObj(c, a.service.Status(), nil)
}

func parseBackupNodeID(c *gin.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		return 0, errors.New("invalid node id")
	}
	return id, nil
}

func (a *BackupController) nodeBackups(c *gin.Context) {
	id, err := parseBackupNodeID(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.restore.toasts.list"), err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	files, err := a.service.NodeBackups(ctx, id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.restore.toasts.list"), err)
		return
	}
	jsonObj(c, files, nil)
}

func (a *BackupController) restoreNode(c *gin.Context) {
	id, err := parseBackupNodeID(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.restore.toasts.start"), err)
		return
	}
	var req service.NodeRestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.restore.toasts.start"), err)
		return
	}
	err = a.service.RestoreNode(id, req, func() {
		reconcileCountryCascades()
		reconcileHomeNetworks()
	})
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.restore.toasts.start"), err)
		return
	}
	jsonObj(c, a.service.RestoreStatus(), nil)
}

func (a *BackupController) restoreStatus(c *gin.Context) {
	jsonObj(c, a.service.RestoreStatus(), nil)
}
