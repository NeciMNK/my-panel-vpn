package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// SubEdgeDNSRequest is the admin's DNS switching setup; an empty token keeps
// the saved one.
type SubEdgeDNSRequest struct {
	Domain    string   `json:"domain" example:"sub.example.com"`
	Token     string   `json:"token" example:""`
	MasterIPs []string `json:"masterIps" example:"[\"198.51.100.1\"]"`
}

// SubEdgeDNSView is the DNS switching setup without its token, and what the
// last switching step did.
type SubEdgeDNSView struct {
	Domain    string            `json:"domain" example:"sub.example.com"`
	TokenSet  bool              `json:"tokenSet" example:"true"`
	MasterIPs []string          `json:"masterIps" example:"[\"198.51.100.1\"]"`
	Status    subedge.DNSStatus `json:"status"`
}

var (
	subEdgeDNS = &subedge.Switcher{}
	// cloudflareAPI is empty in production, where the client uses the real API.
	cloudflareAPI = ""
)

func (s *SubEdgeService) DNSView() (*SubEdgeDNSView, error) {
	var set SettingService
	domain, err := set.getString("subEdgeDnsDomain")
	if err != nil {
		return nil, err
	}
	token, err := set.getString("subEdgeDnsToken")
	if err != nil {
		return nil, err
	}
	view := &SubEdgeDNSView{Domain: domain, TokenSet: token != "", MasterIPs: savedMasterIPs(), Status: subedge.DNSStatus{Tier: -1}}
	if raw, _ := set.getString("subEdgeDnsStatus"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &view.Status)
	}
	return view, nil
}

func (s *SubEdgeService) SaveDNS(req SubEdgeDNSRequest) error {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Domain), "."))
	if domain != "" && !strings.Contains(domain, ".") {
		return fmt.Errorf("%q is not a domain name", req.Domain)
	}
	ips := make([]string, 0, len(req.MasterIPs))
	for _, raw := range req.MasterIPs {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%q is not an IPv4 address", raw)
		}
		ips = append(ips, ip.String())
	}
	raw, err := json.Marshal(ips)
	if err != nil {
		return err
	}
	var set SettingService
	if err := set.setString("subEdgeDnsDomain", domain); err != nil {
		return err
	}
	if err := set.setString("subEdgeDnsMasterIps", string(raw)); err != nil {
		return err
	}
	if token := strings.TrimSpace(req.Token); token != "" {
		return set.setString("subEdgeDnsToken", token)
	}
	return nil
}

func savedMasterIPs() []string {
	raw, _ := (&SettingService{}).getString("subEdgeDnsMasterIps")
	ips := []string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &ips)
	}
	return ips
}

type planEdge struct {
	row  model.SubEdge
	ips  []string
	tier int
}

// dnsPlan is the master's plan: its own addresses, then the edge nodes in
// Russia, then the others, each tier ordered by edge id.
func (s *SubEdgeService) dnsPlan(ctx context.Context) (subedge.DNSPlan, []planEdge, error) {
	var set SettingService
	domain, err := set.getString("subEdgeDnsDomain")
	if err != nil {
		return subedge.DNSPlan{}, nil, err
	}
	token, err := set.getString("subEdgeDnsToken")
	if err != nil {
		return subedge.DNSPlan{}, nil, err
	}
	plan := subedge.DNSPlan{Domain: domain, Token: token, Tiers: [][]string{savedMasterIPs(), {}, {}}}
	rows, err := s.List()
	if err != nil {
		return plan, nil, err
	}
	var edges []planEdge
	for _, row := range rows {
		if !row.Enable {
			continue
		}
		node, err := s.nodeService.GetById(row.NodeId)
		if err != nil || !node.Enable {
			continue
		}
		tier := subedge.TierForeign
		if strings.EqualFold(node.Country, "RU") {
			tier = subedge.TierRussia
		}
		edges = append(edges, planEdge{row: row, ips: nodeIPv4(ctx, node.Address), tier: tier})
	}
	slices.SortStableFunc(edges, func(a, b planEdge) int { return a.tier - b.tier })
	for _, e := range edges {
		plan.Tiers[e.tier] = append(plan.Tiers[e.tier], e.ips...)
	}
	return plan, edges, nil
}

func nodeIPv4(ctx context.Context, address string) []string {
	if ip := net.ParseIP(address); ip != nil {
		if ip.To4() != nil {
			return []string{ip.String()}
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", address)
	if err != nil {
		logger.Warning("subscription edge address", address, "not resolved:", err)
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// SwitchDNS runs one DNS switching step: on the leading master always, on an
// edge node only while no master can.
func (s *SubEdgeService) SwitchDNS(ctx context.Context) {
	if plan, ok := SubEdgeNodeDNSPlan(); ok {
		cf := &subedge.Cloudflare{Token: plan.Token, BaseURL: cloudflareAPI}
		prober := subedge.Prober{Domain: plan.Domain}
		if status, acted := subEdgeNode.StepDNS(ctx, cf, plan, prober.Live, time.Now()); acted && status.Error != "" {
			logger.Warning("subscription DNS switch from this node:", status.Error)
		}
		return
	}
	if !IsClusterLeader() {
		return
	}
	plan, edges, err := s.dnsPlan(ctx)
	if err != nil {
		logger.Warning("subscription DNS plan not built:", err)
		return
	}
	s.sendNodePlans(ctx, plan, edges)
	if !plan.Enabled() {
		return
	}
	prober := subedge.Prober{Domain: plan.Domain}
	status := subEdgeDNS.Step(ctx, &subedge.Cloudflare{Token: plan.Token, BaseURL: cloudflareAPI}, plan, prober.Live, time.Now())
	raw, err := json.Marshal(status)
	if err == nil {
		err = (&SettingService{}).setString("subEdgeDnsStatus", string(raw))
	}
	if err != nil {
		logger.Warning("subscription DNS status not saved:", err)
	}
}

// sendNodePlans gives every edge node the plan with the edges ordered before
// it, so the first live one takes over when the masters are gone.
func (s *SubEdgeService) sendNodePlans(ctx context.Context, plan subedge.DNSPlan, edges []planEdge) {
	var before []string
	for _, e := range edges {
		nodePlan := subedge.DNSPlan{}
		if plan.Enabled() {
			nodePlan = plan
			nodePlan.EdgesBefore = slices.Clone(before)
		}
		before = append(before, e.ips...)
		node, err := s.nodeService.GetById(e.row.NodeId)
		if err != nil {
			continue
		}
		remote, err := runtime.GetManager().RemoteFor(node)
		if err == nil {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = remote.ConfigureSubEdgeDNS(cctx, nodePlan)
			cancel()
		}
		if err != nil {
			logger.Debug("subscription DNS plan not sent to node", node.Name, ":", err)
		}
	}
}

// ConfigureSubEdgeDNSNode is the node side of ConfigureSubEdgeDNS.
func (s *SettingService) ConfigureSubEdgeDNSNode(plan subedge.DNSPlan) error {
	if !plan.Enabled() {
		return s.setString("subEdgeDnsPlan", "")
	}
	if len(plan.Tiers) != 3 {
		return errors.New("a DNS plan has three tiers")
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return s.setString("subEdgeDnsPlan", string(raw))
}

// SubEdgeNodeDNSPlan is the plan a master gave this panel as an edge node.
func SubEdgeNodeDNSPlan() (subedge.DNSPlan, bool) {
	raw, err := (&SettingService{}).getString("subEdgeDnsPlan")
	if err != nil || raw == "" {
		return subedge.DNSPlan{}, false
	}
	var plan subedge.DNSPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil || !plan.Enabled() {
		return subedge.DNSPlan{}, false
	}
	return plan, true
}
