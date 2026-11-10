package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/homenet"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// HomeNetworkService keeps a home network reachable from every server: the
// gateway holds the tunnel, the others reach it through a hidden link.
type HomeNetworkService struct{}

// HomeNetworkRequest is the home network form.
type HomeNetworkRequest struct {
	Name          string                 `json:"name" example:"Home"`
	Enable        bool                   `json:"enable" example:"true"`
	Kind          string                 `json:"kind" example:"wireguard"`
	GatewayNodeId int                    `json:"gatewayNodeId" example:"0"`
	Config        string                 `json:"config"`
	Server        string                 `json:"server" example:"203.0.113.7"`
	Username      string                 `json:"username"`
	Password      string                 `json:"password"`
	Psk           string                 `json:"psk"`
	Subnets       []string               `json:"subnets" example:"192.168.1.0/24"`
	Hosts         []string               `json:"hosts" example:"192.168.1.10"`
	Rules         []model.HomeAccessRule `json:"rules"`
}

func homePrefix(id int) string { return fmt.Sprintf("%shome-%d-", managedTagPrefix, id) }

func homeTargets(h *model.HomeNetwork) []string {
	return append(append([]string{}, h.Subnets...), h.Hosts...)
}

func (s *HomeNetworkService) List() ([]*model.HomeNetwork, error) {
	var out []*model.HomeNetwork
	err := database.GetDB().Order("id asc").Find(&out).Error
	return out, err
}

func (s *HomeNetworkService) get(id int) (*model.HomeNetwork, error) {
	h := &model.HomeNetwork{}
	if err := database.GetDB().Where("id = ?", id).First(h).Error; err != nil {
		return nil, err
	}
	return h, nil
}

func (s *HomeNetworkService) validate(req *HomeNetworkRequest) error {
	if req == nil {
		return common.NewError("home network request is required")
	}
	if !slices.Contains(homenet.Kinds, req.Kind) {
		return common.NewError("connection type must be wireguard, openvpn or l2tp")
	}
	var err error
	if req.Subnets, err = homenet.NormalizeTargets(req.Subnets); err != nil {
		return common.NewError(err.Error())
	}
	if req.Hosts, err = homenet.NormalizeTargets(req.Hosts); err != nil {
		return common.NewError(err.Error())
	}
	if len(req.Subnets)+len(req.Hosts) == 0 {
		return common.NewError("add at least one subnet or IP of the home network")
	}
	known := map[string]bool{}
	for _, t := range append(append([]string{}, req.Subnets...), req.Hosts...) {
		known[t] = true
	}
	for i := range req.Rules {
		r := &req.Rules[i]
		r.Emails = trimmedSet(r.Emails)
		r.Groups = trimmedSet(r.Groups)
		if r.Targets, err = homenet.NormalizeTargets(r.Targets); err != nil {
			return common.NewError(err.Error())
		}
		for _, t := range r.Targets {
			if !known[t] {
				return common.NewError(fmt.Sprintf("rule %d: %s is not one of this network's subnets or IPs", i+1, t))
			}
		}
	}
	switch req.Kind {
	case homenet.KindWireGuard:
		if _, err := homenet.ParseWireGuard(req.Config); err != nil {
			return common.NewError(err.Error())
		}
	case homenet.KindOpenVPN:
		if _, err := homenet.RenderOpenVPN(&homenet.Tunnel{OpenVPN: req.Config}, ""); err != nil {
			return common.NewError(err.Error())
		}
	case homenet.KindL2TP:
		t := &homenet.Tunnel{Server: req.Server, Username: req.Username, Password: req.Password, PSK: req.Psk}
		if _, err := homenet.L2TPFiles(t, "/"); err != nil {
			return common.NewError(err.Error())
		}
	}
	if req.GatewayNodeId != 0 {
		if ok, err := (&NodeService{}).NodeExists(req.GatewayNodeId); err != nil || !ok {
			return common.NewError(fmt.Sprintf("node %d does not exist", req.GatewayNodeId))
		}
	}
	return nil
}

func trimmedSet(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" && !slices.Contains(out, it) {
			out = append(out, it)
		}
	}
	return out
}

func (s *HomeNetworkService) fill(h *model.HomeNetwork, req *HomeNetworkRequest) {
	h.Name, h.Enable, h.Kind, h.GatewayNodeId = strings.TrimSpace(req.Name), req.Enable, req.Kind, req.GatewayNodeId
	h.Config, h.Server, h.Username, h.Password, h.Psk = req.Config, strings.TrimSpace(req.Server), req.Username, req.Password, req.Psk
	h.Subnets, h.Hosts, h.Rules = req.Subnets, req.Hosts, req.Rules
}

// Create stores the network and installs it; masterAddress is how other
// servers reach this panel when it is the gateway.
func (s *HomeNetworkService) Create(ctx context.Context, req *HomeNetworkRequest, userID int, masterAddress string) (*model.HomeNetwork, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	h := &model.HomeNetwork{}
	s.fill(h, req)
	h.MasterAddress = masterAddress
	if err := database.GetDB().Create(h).Error; err != nil {
		return nil, err
	}
	if h.Name == "" {
		h.Name = fmt.Sprintf("home-%d", h.Id)
	}
	err := s.apply(ctx, h, nil, userID, true)
	return h, err
}

func (s *HomeNetworkService) Update(ctx context.Context, id int, req *HomeNetworkRequest, userID int, masterAddress string) (*model.HomeNetwork, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	h, err := s.get(id)
	if err != nil {
		return nil, err
	}
	prev := *h
	s.fill(h, req)
	if masterAddress != "" {
		h.MasterAddress = masterAddress
	}
	err = s.apply(ctx, h, &prev, userID, true)
	return h, err
}

func (s *HomeNetworkService) Delete(ctx context.Context, id int) error {
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	h, err := s.get(id)
	if err != nil {
		return err
	}
	prev := *h
	h.Enable = false
	if err := s.apply(ctx, h, &prev, 1, true); err != nil {
		logger.Warning("home network", id, ": delete left traces:", err)
	}
	return database.GetDB().Delete(&model.HomeNetwork{}, id).Error
}

// homeServers lists every server users can connect to: this panel and each
// enabled node.
func homeServers() ([]int, error) {
	var ids []int
	if err := database.GetDB().Model(model.Node{}).Where("enable = ?", true).Order("id asc").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return append([]int{0}, ids...), nil
}

// resolvedHomeRule is an access rule with its client groups expanded to emails.
type resolvedHomeRule struct {
	Emails  []string `json:"emails"`
	Targets []string `json:"targets"`
}

func resolveHomeRules(h *model.HomeNetwork) ([]resolvedHomeRule, error) {
	out := make([]resolvedHomeRule, 0, len(h.Rules))
	for _, r := range h.Rules {
		emails := append([]string{}, r.Emails...)
		if len(r.Groups) > 0 {
			var fromGroups []string
			if err := database.GetDB().Model(model.ClientRecord{}).Where("group_name IN ?", r.Groups).
				Pluck("email", &fromGroups).Error; err != nil {
				return nil, err
			}
			emails = append(emails, fromGroups...)
		}
		emails = trimmedSet(emails)
		sort.Strings(emails)
		targets := r.Targets
		if len(targets) == 0 {
			targets = homeTargets(h)
		}
		out = append(out, resolvedHomeRule{Emails: emails, Targets: targets})
	}
	return out, nil
}

// applyHomeToTemplate installs one server's part: on the gateway the tunnel,
// elsewhere a link to it; everywhere allow rules per user, then deny the rest.
func applyHomeToTemplate(cfg map[string]any, h *model.HomeNetwork, server int, rules []resolvedHomeRule) error {
	p := homePrefix(h.Id)
	targets := homeTargets(h)
	block := p + "block"
	appendOutbounds(cfg, map[string]any{"tag": block, "protocol": "blackhole"})
	var via string
	var head []map[string]any
	if server == h.GatewayNodeId {
		via = p + "tunnel"
		if h.Kind == homenet.KindWireGuard {
			wg, err := homenet.ParseWireGuard(h.Config)
			if err != nil {
				return err
			}
			appendOutbounds(cfg, wg.Outbound(via, targets))
		} else {
			appendOutbounds(cfg, homenet.InterfaceOutbound(via, h.Id))
		}
		if h.Link != nil {
			in := []string{linkInboundTag(p, server)}
			head = append(head,
				map[string]any{"type": "field", "ruleTag": p + "gw", "inboundTag": in, "ip": targets, "outboundTag": via},
				map[string]any{"type": "field", "ruleTag": p + "gw-deny", "inboundTag": in, "outboundTag": block})
		}
	} else {
		if h.Link == nil {
			return errors.New("no link to the gateway server yet")
		}
		via = p + "via"
		appendOutbounds(cfg, linkOutbound(via, *h.Link))
	}
	for i, r := range rules {
		if len(r.Emails) == 0 {
			continue
		}
		head = append(head, map[string]any{
			"type": "field", "ruleTag": fmt.Sprintf("%sallow-%d", p, i),
			"user": r.Emails, "ip": r.Targets, "outboundTag": via,
		})
	}
	head = append(head, map[string]any{"type": "field", "ruleTag": p + "deny", "ip": targets, "outboundTag": block})
	insertHeadRules(cfg, head...)
	return nil
}

func homeFingerprint(h *model.HomeNetwork, servers []int, rules []resolvedHomeRule) string {
	raw, _ := json.Marshal(struct {
		Servers []int
		Rules   []resolvedHomeRule
	}{servers, rules})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func homeTunnel(h *model.HomeNetwork, enable bool) *homenet.Tunnel {
	return &homenet.Tunnel{
		Id: h.Id, Kind: h.Kind, Enable: enable, OpenVPN: h.Config, Server: h.Server,
		Username: h.Username, Password: h.Password, PSK: h.Psk, Routes: homeTargets(h),
	}
}

// setHomeTunnel brings a system tunnel up or down on its gateway server.
func setHomeTunnel(ctx context.Context, nodeID int, t *homenet.Tunnel) (homenet.TunnelStatus, error) {
	if nodeID == 0 {
		return homenet.ApplyWithin(t)
	}
	body, err := json.Marshal(t)
	if err != nil {
		return homenet.TunnelStatus{}, err
	}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, nodeID, http.MethodPost, "/homeTunnel", "", "application/json", body)
	if err != nil {
		return homenet.TunnelStatus{}, err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return homenet.TunnelStatus{}, err
	}
	var st homenet.TunnelStatus
	err = json.Unmarshal(obj, &st)
	return st, err
}

func homeTunnelStatus(ctx context.Context, nodeID, id int) (homenet.TunnelStatus, error) {
	if nodeID == 0 {
		return homenet.Status(id), nil
	}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, nodeID, http.MethodGet, fmt.Sprintf("/homeTunnel/%d", id), "", "", nil)
	if err != nil {
		return homenet.TunnelStatus{}, err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return homenet.TunnelStatus{}, err
	}
	var st homenet.TunnelStatus
	err = json.Unmarshal(obj, &st)
	return st, err
}

// apply converges the tunnel, the link and every server's template; failures
// are kept per server in h.Errors and retried by ReconcileHomeNetworks.
func (s *HomeNetworkService) apply(ctx context.Context, h *model.HomeNetwork, prev *model.HomeNetwork, userID int, touchTunnel bool) error {
	errs := map[string]string{}
	system := h.Kind != homenet.KindWireGuard
	if !touchTunnel {
		if e, ok := h.Errors["tunnel"]; ok {
			errs["tunnel"] = e
		}
	}
	if touchTunnel && prev != nil && prev.Kind != homenet.KindWireGuard && (!system || prev.GatewayNodeId != h.GatewayNodeId || !h.Enable) {
		if _, err := setHomeTunnel(ctx, prev.GatewayNodeId, homeTunnel(prev, false)); err != nil {
			errs["tunnel"] = "stop on " + nodeLabel(prev.GatewayNodeId) + ": " + err.Error()
		}
	}
	if !system || !h.Enable {
		h.TunnelState = ""
	}
	if touchTunnel && system && h.Enable {
		st, err := setHomeTunnel(ctx, h.GatewayNodeId, homeTunnel(h, true))
		if err != nil {
			errs["tunnel"] = err.Error()
		}
		h.TunnelState = st.Detail
	}

	servers, err := homeServers()
	if err != nil {
		return err
	}
	needLink := h.Enable && len(servers) > 1
	if h.Link != nil && (h.Link.NodeId != h.GatewayNodeId || !needLink) {
		(&CascadeService{}).removeLink(*h.Link)
		h.Link = nil
	}
	if needLink && h.Link == nil {
		link, err := provisionHiddenLink(homePrefix(h.Id), h.Name+" (home network link)", h.GatewayNodeId, userID, h.MasterAddress)
		if err != nil {
			errs["link"] = err.Error()
		} else {
			h.Link = &link
		}
	}

	rules, err := resolveHomeRules(h)
	if err != nil {
		return err
	}
	touched := append(append([]int{}, servers...), h.Servers...)
	slices.Sort(touched)
	touched = slices.Compact(touched)
	installed := make([]int, 0, len(servers))
	for _, id := range touched {
		install := h.Enable && slices.Contains(servers, id)
		if err := s.rewriteServer(ctx, id, h, rules, install); err != nil {
			errs[nodeLabel(id)] = err.Error()
			if slices.Contains(h.Servers, id) {
				installed = append(installed, id)
			}
			continue
		}
		if install {
			installed = append(installed, id)
		}
	}
	h.Servers = installed
	h.Errors = errs
	h.Applied = homeFingerprint(h, servers, rules)
	if err := database.GetDB().Save(h).Error; err != nil {
		return err
	}
	if len(errs) > 0 {
		keys := make([]string, 0, len(errs))
		for k := range errs {
			keys = append(keys, k+": "+errs[k])
		}
		sort.Strings(keys)
		return common.NewError(strings.Join(keys, "; "))
	}
	return nil
}

func (s *HomeNetworkService) rewriteServer(ctx context.Context, nodeID int, h *model.HomeNetwork, rules []resolvedHomeRule, install bool) error {
	store := templateStoreFor(nodeID)
	tmpl, err := store.Load(ctx)
	if err != nil {
		return err
	}
	stripManaged(tmpl.Config, homePrefix(h.Id))
	if install {
		if err := applyHomeToTemplate(tmpl.Config, h, nodeID, rules); err != nil {
			return err
		}
	}
	return store.Save(ctx, tmpl)
}

// ReconcileHomeNetworks re-applies networks whose users (via groups), server
// list or earlier failures changed since the last apply.
func (s *HomeNetworkService) ReconcileHomeNetworks(ctx context.Context) {
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	rows, err := s.List()
	if err != nil {
		logger.Warning("home network: list:", err)
		return
	}
	servers, err := homeServers()
	if err != nil {
		logger.Warning("home network: list servers:", err)
		return
	}
	for _, h := range rows {
		if !h.Enable && len(h.Servers) == 0 {
			continue
		}
		rules, err := resolveHomeRules(h)
		if err != nil {
			logger.Warning("home network", h.Id, ": resolve rules:", err)
			continue
		}
		if h.Enable && h.Kind != homenet.KindWireGuard && h.TunnelState != "active" {
			if st, err := homeTunnelStatus(ctx, h.GatewayNodeId, h.Id); err == nil && st.Detail != h.TunnelState {
				h.TunnelState = st.Detail
				database.GetDB().Model(h).Update("tunnel_state", st.Detail)
			}
		}
		if len(h.Errors) == 0 && h.Applied == homeFingerprint(h, servers, rules) {
			continue
		}
		prev := *h
		_, tunnelFailed := h.Errors["tunnel"]
		if err := s.apply(ctx, h, &prev, 1, tunnelFailed); err != nil {
			logger.Warning("home network", h.Id, ":", err)
		}
	}
}
