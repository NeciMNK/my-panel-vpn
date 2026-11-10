package controller

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// maxNodeXrayProxyBody bounds a proxied Xray-settings body; a full template with
// many outbounds and rules stays far below it.
const maxNodeXrayProxyBody = 8 << 20

// xrayProxy serves /panel/api/nodes/:id/xray/*path by replaying the request
// against the node's own /panel/api/xray/*path and returning its answer as is.
func (a *NodeController) xrayProxy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.obtain"), err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxNodeXrayProxyBody+1))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.obtain"), err)
		return
	}
	if len(body) > maxNodeXrayProxyBody {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return
	}
	resp, err := a.nodeService.ProxyNodeXray(
		c.Request.Context(), id, c.Request.Method, c.Param("path"),
		c.Request.URL.RawQuery, c.ContentType(), body,
	)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.obtain"), err)
		return
	}
	contentType := resp.ContentType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(resp.Status, contentType, resp.Body)
}
