package job

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// TgVpnFallbackJob moves the panel (and so the bot) onto its own VPN client
// when this server cannot reach Telegram directly.
type TgVpnFallbackJob struct {
	fallback    service.TgVpnFallback
	xrayService service.XrayService
}

func NewTgVpnFallbackJob() *TgVpnFallbackJob {
	return &TgVpnFallbackJob{}
}

func (j *TgVpnFallbackJob) Run() {
	changed, err := j.fallback.Run(context.Background())
	if err != nil {
		logger.Warning("Telegram VPN fallback:", err)
	}
	if changed {
		j.xrayService.SetToNeedRestart()
	}
}
