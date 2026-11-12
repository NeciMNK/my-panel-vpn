package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// PanelProxyPinJob keeps the panel proxy (and so the bot) on an exit that reaches Telegram.
type PanelProxyPinJob struct {
	panelProxy service.PanelProxyService
}

func NewPanelProxyPinJob() *PanelProxyPinJob { return &PanelProxyPinJob{} }

func (j *PanelProxyPinJob) Run() { j.panelProxy.PinPanelProxyExit(time.Now()) }
