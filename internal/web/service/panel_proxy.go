package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/util/link"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// The quick panel proxy routes the panel's own requests through outbounds it
// builds itself, so the admin needs no hand-made Xray outbound for it.
const (
	PanelProxyModeOff          = ""
	PanelProxyModeClient       = "client"
	PanelProxyModeLink         = "link"
	PanelProxyModeSubscription = "subscription"

	PanelProxyTagPrefix   = "panelproxy-"
	PanelProxyBalancerTag = "panelproxy-auto"
	// Telegram is the service most often filtered where a panel needs a proxy, so an
	// exit that cannot reach it (this server's own inbound, a dead node) counts as down.
	panelProxyProbeURL = "https://api.telegram.org/"
)

var (
	errPanelProxyUnknownMode = common.NewError("unknown panel proxy mode")
	errPanelProxyNoOutbounds = common.NewError("the panel proxy source yields no usable outbound")
)

// PanelProxyStatus is the quick panel proxy as stored, plus the outbounds it builds now.
type PanelProxyStatus struct {
	Mode      string   `json:"mode" example:"client"`
	Source    string   `json:"source" example:"alice"`
	Active    bool     `json:"active" example:"true"`
	Outbounds []string `json:"outbounds" example:"[\"panelproxy-de-frankfurt\"]"`
	Error     string   `json:"error" example:""`
}

// PanelProxyCheck is one live request made the way the panel makes its own.
type PanelProxyCheck struct {
	ViaProxy   bool   `json:"viaProxy" example:"true"`
	ExitIP     string `json:"exitIp" example:"203.0.113.7"`
	Telegram   bool   `json:"telegram" example:"true"`
	TelegramMs int64  `json:"telegramMs" example:"184"`
	Error      string `json:"error" example:""`
}

type PanelProxyService struct {
	settingService SettingService
	inboundService InboundService
	subService     OutboundSubscriptionService
}

func (s *PanelProxyService) stored() (mode, source string, err error) {
	if mode, err = s.settingService.getString("panelProxyMode"); err != nil {
		return "", "", err
	}
	if source, err = s.settingService.getString("panelProxySource"); err != nil {
		return "", "", err
	}
	return mode, source, nil
}

func (s *PanelProxyService) Status() (*PanelProxyStatus, error) {
	mode, source, err := s.stored()
	if err != nil {
		return nil, err
	}
	status := &PanelProxyStatus{Mode: mode, Source: source, Outbounds: []string{}}
	if mode == PanelProxyModeOff {
		return status, nil
	}
	outbounds, err := s.outboundsFor(mode, source)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}
	for _, ob := range outbounds {
		if tag, _ := ob["tag"].(string); tag != "" {
			status.Outbounds = append(status.Outbounds, tag)
		}
	}
	status.Active = len(status.Outbounds) > 0
	return status, nil
}

// Set validates source against mode, stores both, and drops the subscription a
// previous subscription-mode setup created. The caller reapplies the Xray config.
func (s *PanelProxyService) Set(mode, source string) error {
	source = strings.TrimSpace(source)
	switch mode {
	case PanelProxyModeOff:
		source = ""
	case PanelProxyModeClient, PanelProxyModeLink:
		outbounds, err := s.outboundsFor(mode, source)
		if err != nil {
			return err
		}
		if len(outbounds) == 0 {
			return errPanelProxyNoOutbounds
		}
	case PanelProxyModeSubscription:
		if err := s.syncSubscription(source); err != nil {
			return err
		}
	default:
		return errPanelProxyUnknownMode
	}
	if mode != PanelProxyModeSubscription {
		if err := s.deleteSubscription(); err != nil {
			return err
		}
	}
	if err := s.settingService.setString("panelProxyMode", mode); err != nil {
		return err
	}
	return s.settingService.setString("panelProxySource", source)
}

// outboundsFor returns the outbounds mode/source contribute, tagged with PanelProxyTagPrefix.
func (s *PanelProxyService) outboundsFor(mode, source string) ([]link.Outbound, error) {
	switch mode {
	case PanelProxyModeClient:
		links, err := s.clientNodeLinks(source)
		if err != nil {
			return nil, err
		}
		return tagPanelProxyOutbounds(parsePanelProxyLinks(links)), nil
	case PanelProxyModeLink:
		ob, err := panelProxyLinkOutbound(source)
		if err != nil {
			return nil, err
		}
		return tagPanelProxyOutbounds([]link.Outbound{ob}), nil
	case PanelProxyModeSubscription:
		sub, err := s.findSubscription()
		if err != nil || sub == nil {
			return nil, err
		}
		arr, err := s.subService.GetLastOutbounds(sub.Id)
		if err != nil {
			return nil, err
		}
		out := make([]link.Outbound, 0, len(arr))
		for _, ob := range arr {
			if m, ok := ob.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, nil
	case PanelProxyModeOff:
		return nil, nil
	default:
		return nil, errPanelProxyUnknownMode
	}
}

// clientNodeLinks returns the share links of a panel client on inbounds hosted by
// nodes: this server's own inbounds would loop the panel's traffic back to itself.
func (s *PanelProxyService) clientNodeLinks(email string) ([]string, error) {
	if email == "" {
		return nil, common.NewError("client email is required")
	}
	provider := subLinkProvider()
	if provider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	rec, err := s.inboundService.clientService.GetRecordByEmail(nil, email)
	if err != nil {
		return nil, err
	}
	inboundIds, err := s.inboundService.clientService.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return nil, err
	}
	var links []string
	for _, id := range inboundIds {
		inbound, err := s.inboundService.GetInbound(id)
		if err != nil {
			return nil, err
		}
		if inbound.NodeID == nil || !inbound.Enable {
			continue
		}
		links = append(links, provider.LinksForClient("", inbound, email)...)
	}
	if len(links) == 0 {
		return nil, common.NewError("client has no enabled inbound on another server")
	}
	return links, nil
}

// ClientCandidates lists the enabled clients that own an enabled inbound on a node.
func (s *PanelProxyService) ClientCandidates() ([]string, error) {
	emails := []string{}
	err := database.GetDB().Model(&model.ClientRecord{}).
		Distinct("clients.email").
		Joins("JOIN client_inbounds ON client_inbounds.client_id = clients.id").
		Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
		Where("clients.enable = ? AND inbounds.enable = ? AND inbounds.node_id IS NOT NULL", true, true).
		Order("clients.email").
		Pluck("clients.email", &emails).Error
	return emails, err
}

func parsePanelProxyLinks(links []string) []link.Outbound {
	out := make([]link.Outbound, 0, len(links))
	for _, l := range links {
		res, err := link.ParseLink(l)
		if err != nil {
			logger.Debug("panel proxy: skipping unparsable link:", err)
			continue
		}
		out = append(out, res.Outbound)
	}
	return out
}

// tagPanelProxyOutbounds names each outbound "panelproxy-<remark>", unique within the batch.
func tagPanelProxyOutbounds(outbounds []link.Outbound) []link.Outbound {
	used := map[string]bool{}
	for i, ob := range outbounds {
		remark, _ := ob["tag"].(string)
		tag := link.SuggestTag(PanelProxyTagPrefix, remark, i)
		final := tag
		for k := 1; used[final]; k++ {
			final = fmt.Sprintf("%s-%d", tag, k)
		}
		used[final] = true
		ob["tag"] = final
	}
	return outbounds
}

// panelProxyLinkOutbound accepts a share link or a plain socks5/http(s) proxy URL.
func panelProxyLinkOutbound(raw string) (link.Outbound, error) {
	raw = strings.TrimSpace(raw)
	scheme, _, found := strings.Cut(raw, "://")
	if !found {
		return nil, common.NewError("enter a share link or a proxy URL such as socks5://host:port")
	}
	switch strings.ToLower(scheme) {
	case "socks", "socks5", "socks5h", "http", "https":
		return proxyURLOutbound(raw)
	}
	res, err := link.ParseLink(raw)
	if err != nil {
		return nil, err
	}
	return res.Outbound, nil
}

func proxyURLOutbound(raw string) (link.Outbound, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, common.NewError("invalid proxy URL")
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if host == "" || err != nil || port < 1 || port > 65535 {
		return nil, common.NewError("proxy URL needs a host and a port")
	}
	protocol := "socks"
	scheme := strings.ToLower(u.Scheme)
	if scheme == "http" || scheme == "https" {
		protocol = "http"
	}
	server := map[string]any{"address": host, "port": port}
	if u.User != nil {
		pass, _ := u.User.Password()
		server["users"] = []any{map[string]any{"user": u.User.Username(), "pass": pass}}
	}
	ob := link.Outbound{
		"protocol": protocol,
		"tag":      host,
		"settings": map[string]any{"servers": []any{server}},
	}
	if scheme == "https" {
		ob["streamSettings"] = map[string]any{
			"security":    "tls",
			"tlsSettings": map[string]any{"serverName": host},
		}
	}
	return ob, nil
}

func (s *PanelProxyService) findSubscription() (*model.OutboundSubscription, error) {
	var subs []*model.OutboundSubscription
	if err := database.GetDB().Where("tag_prefix = ?", PanelProxyTagPrefix).Order("id asc").Limit(1).Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, nil
	}
	return subs[0], nil
}

// syncSubscription points the panel proxy's own outbound subscription at rawURL
// and fetches it once, so a bad URL is reported now instead of on the next refresh.
func (s *PanelProxyService) syncSubscription(rawURL string) error {
	existing, err := s.findSubscription()
	if err != nil {
		return err
	}
	const remark, interval = "Panel proxy", 3600
	var id int
	if existing != nil {
		id = existing.Id
		err = s.subService.Update(id, remark, rawURL, PanelProxyTagPrefix, existing.UserAgent, true, interval, false, false, false)
	} else {
		var created *model.OutboundSubscription
		created, err = s.subService.Create(remark, rawURL, PanelProxyTagPrefix, "", true, interval, false, false, false)
		if created != nil {
			id = created.Id
		}
	}
	if err != nil {
		return err
	}
	outbounds, err := s.subService.Refresh(id)
	if err == nil && len(outbounds) == 0 {
		err = errPanelProxyNoOutbounds
	}
	if err != nil && existing == nil {
		_ = s.subService.Delete(id)
	}
	return err
}

func (s *PanelProxyService) deleteSubscription() error {
	return database.GetDB().Where("tag_prefix = ?", PanelProxyTagPrefix).Delete(&model.OutboundSubscription{}).Error
}

// generatedOutbounds is what the config build appends: subscription mode returns
// nothing because the subscription merge already placed its outbounds.
func (s *PanelProxyService) generatedOutbounds() (active bool, outbounds []any) {
	mode, source, err := s.stored()
	if err != nil || mode == PanelProxyModeOff {
		return false, nil
	}
	if mode == PanelProxyModeSubscription {
		return true, nil
	}
	built, err := s.outboundsFor(mode, source)
	if err != nil {
		logger.Warning("panel proxy: cannot build outbounds, the panel connects directly:", err)
		return true, nil
	}
	for _, ob := range built {
		outbounds = append(outbounds, map[string]any(ob))
	}
	outbounds, _ = filterOutboundsRejectedByCore("panel proxy", outbounds)
	return true, outbounds
}

// injectPanelProxy appends extra plus a balancer over every panelproxy-* outbound, and
// reports whether it did; cfg is left untouched when it cannot.
func injectPanelProxy(cfg *xray.Config, extra []any) bool {
	var outbounds []any
	if len(cfg.OutboundConfigs) > 0 {
		if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
			logger.Warning("panel proxy: outbounds section is unparsable, skipping:", err)
			return false
		}
	}
	taken := map[string]bool{}
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]any); ok {
			if tag, _ := m["tag"].(string); tag != "" {
				taken[tag] = true
			}
		}
	}
	for _, ob := range extra {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		final := tag
		for k := 1; taken[final]; k++ {
			final = fmt.Sprintf("%s-%d", tag, k)
		}
		taken[final] = true
		m["tag"] = final
		outbounds = append(outbounds, m)
	}
	var members []string
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]any); ok {
			if tag, _ := m["tag"].(string); strings.HasPrefix(tag, PanelProxyTagPrefix) {
				members = append(members, tag)
			}
		}
	}
	if len(members) == 0 {
		logger.Warning("panel proxy: no outbound to route through, the panel connects directly")
		return false
	}

	routing := map[string]any{}
	if len(cfg.RouterConfig) > 0 {
		if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
			logger.Warning("panel proxy: routing section is unparsable, skipping:", err)
			return false
		}
	}
	if routingTagIsBalancer(routing, PanelProxyBalancerTag) {
		logger.Warning("panel proxy: balancer tag [", PanelProxyBalancerTag, "] already exists, skipping")
		return false
	}

	balancers, _ := routing["balancers"].([]any)
	routing["balancers"] = append(balancers, map[string]any{
		"tag":      PanelProxyBalancerTag,
		"selector": []any{PanelProxyTagPrefix},
		// PinPanelProxyExit overrides the pick with an exit that reaches Telegram.
		"strategy": map[string]any{"type": "random"},
	})
	newRouting, err := json.Marshal(routing)
	if err != nil {
		return false
	}
	newOutbounds, err := json.MarshalIndent(outbounds, "", "  ")
	if err != nil {
		return false
	}
	cfg.OutboundConfigs = json_util.RawMessage(newOutbounds)
	cfg.RouterConfig = json_util.RawMessage(newRouting)
	return true
}

// Check makes the requests the panel makes for itself, through the same client.
func (s *PanelProxyService) Check(ctx context.Context) *PanelProxyCheck {
	result := &PanelProxyCheck{ViaProxy: s.settingService.PanelEgressProxyURL() != ""}
	client := s.settingService.NewProxiedHTTPClient(10 * time.Second)
	defer client.CloseIdleConnections()

	start := time.Now()
	if err := panelProxyGet(ctx, client, panelProxyProbeURL, nil); err != nil {
		result.Error = err.Error()
	} else {
		result.Telegram = true
		result.TelegramMs = time.Since(start).Milliseconds()
	}
	var ip []byte
	if err := panelProxyGet(ctx, client, "https://api.ipify.org", &ip); err == nil {
		if parsed := net.ParseIP(strings.TrimSpace(string(ip))); parsed != nil {
			result.ExitIP = parsed.String()
		}
	}
	return result
}

func panelProxyGet(ctx context.Context, client *http.Client, target string, body *[]byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return WithoutRequestURL(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicIPBytes))
	if body != nil {
		*body = data
	}
	return err
}
