package service

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/outbound"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const (
	panelProxyPinInterval = time.Minute
	// TestOutbounds refuses larger batches.
	panelProxyProbeBatch = 50
)

// panelProxyPin remembers the core it last probed for: a restarted core has lost
// its override and gets probed at once instead of a minute on a random exit.
var panelProxyPin struct {
	sync.Mutex
	process *xray.Process
	probed  time.Time
}

// PinPanelProxyExit probes every panel proxy exit through Telegram once a minute
// (and right after Xray restarts), pinning the balancer to one that reaches it.
func (s *PanelProxyService) PinPanelProxyExit(now time.Time) {
	process := currentXrayProcess()
	if process == nil || !process.IsRunning() {
		return
	}
	panelProxyPin.Lock()
	defer panelProxyPin.Unlock()
	if process == panelProxyPin.process && now.Sub(panelProxyPin.probed) < panelProxyPinInterval {
		return
	}
	panelProxyPin.process, panelProxyPin.probed = process, now

	members, all, err := panelProxyExits(process.GetConfig())
	if err != nil {
		logger.Warning("panel proxy: cannot read the running outbounds:", err)
		return
	}
	if len(members) < 2 {
		return
	}
	xrayService := &XrayService{}
	statuses, err := xrayService.GetBalancersStatus([]string{PanelProxyBalancerTag})
	if err != nil || len(statuses) == 0 || !statuses[0].Running {
		return
	}
	var results []*outbound.TestOutboundResult
	for chunk := range slices.Chunk(members, panelProxyProbeBatch) {
		batch, err := json.Marshal(chunk)
		if err != nil {
			return
		}
		got, err := (&outbound.OutboundService{}).TestOutbounds(string(batch), panelProxyProbeURL, string(all), "real")
		if err != nil {
			logger.Warning("panel proxy: probing exits failed:", err)
			return
		}
		results = append(results, got...)
	}
	target, change := pickPanelProxyExit(statuses[0].Override, results)
	if !change {
		return
	}
	if err := xrayService.OverrideBalancer(PanelProxyBalancerTag, target); err != nil {
		logger.Warning("panel proxy: pinning exit [", target, "] failed:", err)
		return
	}
	logger.Info("panel proxy: pinned exit [", target, "], it reaches Telegram")
}

// panelProxyExits returns the running panelproxy-* outbounds and, for their
// dialerProxy chains, every outbound as a JSON array.
func panelProxyExits(cfg *xray.Config) (members []map[string]any, all json.RawMessage, err error) {
	var outbounds []map[string]any
	if cfg == nil || len(cfg.OutboundConfigs) == 0 {
		return nil, nil, nil
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		return nil, nil, err
	}
	for _, ob := range outbounds {
		if tag, _ := ob["tag"].(string); strings.HasPrefix(tag, PanelProxyTagPrefix) {
			members = append(members, ob)
		}
	}
	return members, json.RawMessage(cfg.OutboundConfigs), nil
}

// pickPanelProxyExit keeps a pin that still reaches Telegram, so the bot's open
// connections survive; otherwise it moves to the fastest exit that does.
func pickPanelProxyExit(current string, results []*outbound.TestOutboundResult) (string, bool) {
	var best *outbound.TestOutboundResult
	for _, r := range results {
		if r == nil || !r.Success {
			continue
		}
		if r.Tag == current {
			return current, false
		}
		if best == nil || r.Delay < best.Delay {
			best = r
		}
	}
	if best == nil {
		return current, false
	}
	return best.Tag, true
}
