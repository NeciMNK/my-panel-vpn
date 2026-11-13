package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"gorm.io/gorm"
)

// When this server cannot reach Telegram itself, the panel gives itself a VPN
// client on the nodes and routes its own traffic (the bot's too) through it.
const (
	PanelTechClientEmail = "panel-tech"
	PanelTechGroup       = model.ClientGroupTech
	// One failed probe is often a blip; two in a row is a filter, not a hiccup.
	tgVpnFallbackFailures = 2
	tgDirectProbeTimeout  = 10 * time.Second
)

// panelTechProtocols are the inbound protocols whose share links parse into an
// Xray outbound, so the tech client is useful on them and nowhere else.
var panelTechProtocols = []model.Protocol{model.VLESS, model.VMESS, model.Trojan, model.Shadowsocks, model.Hysteria}

var errPanelTechNoNodeInbound = common.NewError("no enabled VLESS/VMess/Trojan/Shadowsocks/Hysteria inbound on a node to route Telegram through")

// TgVpnFallback runs one Telegram reachability check per Run and keeps the
// consecutive-failure count between them, so it is held by its cron job.
type TgVpnFallback struct {
	// Probe reports whether Telegram answers over a direct connection.
	Probe    func(ctx context.Context) error
	failures int

	settingService SettingService
	inboundService InboundService
	clientService  ClientService
	panelProxy     PanelProxyService
	ledger         LedgerService
}

// Run returns whether the Xray config must be rebuilt for the change it made.
func (f *TgVpnFallback) Run(ctx context.Context) (bool, error) {
	mode, source, err := f.panelProxy.stored()
	if err != nil {
		return false, err
	}
	if mode == PanelProxyModeClient && source == PanelTechClientEmail {
		f.failures = 0
		// Nodes added since the switch only become exits once the client is on them.
		return f.ensureTechClient()
	}
	candidate, err := f.isCandidate(mode)
	if err != nil || !candidate {
		f.failures = 0
		return false, err
	}
	probe := f.Probe
	if probe == nil {
		probe = probeTelegramDirect
	}
	if err := probe(ctx); err == nil {
		f.failures = 0
		return false, nil
	} else if f.failures++; f.failures < tgVpnFallbackFailures {
		logger.Debug("Telegram is unreachable directly:", err)
		return false, nil
	}
	f.failures = 0
	if _, err := f.ensureTechClient(); err != nil {
		return false, err
	}
	if err := f.panelProxy.Set(PanelProxyModeClient, PanelTechClientEmail); err != nil {
		return false, err
	}
	logger.Warning("Telegram is unreachable from this server: the panel now connects through its VPN client", PanelTechClientEmail)
	return true, nil
}

// isCandidate is false whenever the admin already routes the bot or the panel
// some other way: their choice wins and a failure there is theirs to see.
func (f *TgVpnFallback) isCandidate(mode string) (bool, error) {
	if mode != PanelProxyModeOff {
		return false, nil
	}
	for _, read := range []func() (bool, error){f.settingService.GetTgBotVpnFallback, f.settingService.GetTgbotEnabled} {
		if on, err := read(); err != nil || !on {
			return false, err
		}
	}
	for _, read := range []func() (string, error){f.settingService.GetTgBotProxy, f.settingService.GetTgBotAPIServer, f.settingService.GetPanelOutbound} {
		if v, err := read(); err != nil || v != "" {
			return false, err
		}
	}
	return true, nil
}

// ensureTechClient puts the tech client, in the non-paying tech group, on every node
// exit inbound (never the panel's own mp-* links); true if attached anywhere new.
func (f *TgVpnFallback) ensureTechClient() (bool, error) {
	var inboundIds []int
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("enable = ? AND node_id IS NOT NULL AND protocol IN ? AND tag NOT LIKE ?", true, panelTechProtocols, managedTagPrefix+"%").
		Order("id").Pluck("id", &inboundIds).Error; err != nil {
		return false, err
	}
	if len(inboundIds) == 0 {
		return false, errPanelTechNoNodeInbound
	}

	rec, err := f.clientService.GetRecordByEmail(nil, PanelTechClientEmail)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if _, err := f.clientService.Create(&f.inboundService, &ClientCreatePayload{
			Client: model.Client{
				Email:   PanelTechClientEmail,
				Enable:  true,
				Group:   PanelTechGroup,
				Comment: "Panel's own VPN access to Telegram",
			},
			InboundIds: inboundIds,
		}); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	default:
		attached, err := f.clientService.GetInboundIdsForRecord(rec.Id)
		if err != nil {
			return false, err
		}
		missing := slices.DeleteFunc(inboundIds, func(id int) bool { return slices.Contains(attached, id) })
		if len(missing) == 0 {
			return false, nil
		}
		if _, err := f.clientService.Attach(&f.inboundService, rec.Id, missing); err != nil {
			return false, err
		}
	}
	return true, f.markTechGroup()
}

func (f *TgVpnFallback) markTechGroup() error {
	if _, err := f.clientService.AddToGroup([]string{PanelTechClientEmail}, PanelTechGroup); err != nil {
		return err
	}
	return f.ledger.SetGroupNonPaying(PanelTechGroup, true)
}

// probeTelegramDirect bypasses every panel proxy: any HTTP answer from the Bot
// API host means Telegram is reachable from here.
func probeTelegramDirect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, tgDirectProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, panelProxyProbeURL, nil)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return WithoutRequestURL(err)
	}
	return resp.Body.Close()
}
