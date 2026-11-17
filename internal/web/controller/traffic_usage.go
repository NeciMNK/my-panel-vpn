package controller

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// TrafficUsageController serves /panel/api/trafficUsage: traffic of every
// server and client over the periods picked in the settings.
type TrafficUsageController struct {
	usage service.TrafficUsageService
}

func NewTrafficUsageController(g *gin.RouterGroup) *TrafficUsageController {
	a := &TrafficUsageController{}
	g.GET("/report", a.report)
	return a
}

func (a *TrafficUsageController) report(c *gin.Context) {
	out, err := a.usage.Report(time.Now())
	jsonObj(c, out, err)
}
