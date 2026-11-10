package controller

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tlscert"
)

// TlsCertController issues panel-managed TLS certificates and, on a node,
// accepts the copies its master pushes.
type TlsCertController struct {
	service tlscert.Service
}

func NewTlsCertController(g *gin.RouterGroup) *TlsCertController {
	a := &TlsCertController{}
	g = g.Group("/certs")
	g.GET("/list", a.list)
	g.GET("/settings", a.getSettings)
	g.POST("/settings", a.setSettings)
	g.POST("/issue", a.issue)
	g.POST("/renew/:id", a.renew)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/install", a.install)
	return a
}

func parseTlsCertID(c *gin.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid id %q", c.Param("id"))
	}
	return id, nil
}

func formBool(c *gin.Context, key string) (bool, error) {
	raw, ok := c.GetPostForm(key)
	if !ok || raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q", key, raw)
	}
	return v, nil
}

func (a *TlsCertController) list(c *gin.Context) {
	certs, err := a.service.List()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, certs, nil)
}

func (a *TlsCertController) getSettings(c *gin.Context) {
	settings, err := a.service.Settings()
	jsonObj(c, settings, err)
}

func (a *TlsCertController) setSettings(c *gin.Context) {
	if err := a.service.SetCloudflareToken(c.PostForm("cloudflareToken")); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.saveToken"), err)
		return
	}
	settings, err := a.service.Settings()
	jsonObj(c, settings, err)
}

func (a *TlsCertController) issue(c *gin.Context) {
	autoRenew, err := formBool(c, "autoRenew")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.issue"), err)
		return
	}
	applyToPanel, err := formBool(c, "applyToPanel")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.issue"), err)
		return
	}
	cert, err := a.service.Create(c.PostFormArray("domains"), autoRenew, applyToPanel)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.issue"), err)
		return
	}
	jsonObj(c, cert, nil)
}

func (a *TlsCertController) renew(c *gin.Context) {
	id, err := parseTlsCertID(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.renew"), err)
		return
	}
	cert, err := a.service.Renew(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.renew"), err)
		return
	}
	jsonObj(c, cert, nil)
}

func (a *TlsCertController) update(c *gin.Context) {
	id, err := parseTlsCertID(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.update"), err)
		return
	}
	autoRenew, err := formBool(c, "autoRenew")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.update"), err)
		return
	}
	cert, err := a.service.Update(id, autoRenew)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.update"), err)
		return
	}
	jsonObj(c, cert, nil)
}

func (a *TlsCertController) del(c *gin.Context) {
	id, err := parseTlsCertID(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.delete"), err)
		return
	}
	if err := a.service.Delete(id); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.delete"), err)
		return
	}
	jsonObj(c, "", nil)
}

func (a *TlsCertController) install(c *gin.Context) {
	var req tlscert.TlsCertInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.install"), err)
		return
	}
	files, err := a.service.Install(req)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tlsCerts.toasts.install"), err)
		return
	}
	jsonObj(c, files, nil)
}
