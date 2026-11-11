package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
)

// Migration sources understood by MigrationService.
const (
	MigrationSourceRemnawave = "remnawave"
	MigrationSourceXUI       = "3x-ui"
)

// Target values a source server can be mapped to; "node:<id>" picks a node.
const (
	MigrationTargetLocal = "local"
	MigrationTargetSkip  = "skip"
)

// migrationPlan is the source-neutral result of reading another panel: what
// to create here, keyed so that clients and hosts can follow their inbounds.
type migrationPlan struct {
	source   string
	servers  []MigrationSourceServer
	inbounds []migrationInbound
	clients  []migrationClient
	hosts    []migrationHost
	sub      MigrationSubInfo
	warnings []string
}

type migrationInbound struct {
	key       string
	serverKey string
	inbound   model.Inbound
}

type migrationClient struct {
	client      model.Client
	limitHwid   int
	traffic     *ClientPortableTraffic
	inboundKeys []string
	// hostIdx indexes plan.hosts the client is bound to; empty shows every host.
	hostIdx []int
}

type migrationHost struct {
	inboundKey string
	group      entity.HostGroup
}

func (p *migrationPlan) warnf(format string, args ...any) {
	p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
}

// MigrationSourceServer is one server of the source panel whose inbounds must
// land somewhere here: on this panel, on one of its nodes, or nowhere.
type MigrationSourceServer struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Inbounds  int    `json:"inbounds"`
	Suggested string `json:"suggested"`
}

// MigrationSubInfo describes where the source panel served subscriptions, so
// the same links keep working once that address points at this panel.
type MigrationSubInfo struct {
	ExampleURL string `json:"exampleUrl"`
	Scheme     string `json:"scheme"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	JsonPath   string `json:"jsonPath,omitempty"`
	ClashPath  string `json:"clashPath,omitempty"`
	URI        string `json:"uri,omitempty"`
	// RemarkTemplate names configs the way the source did, e.g. "{{HOST}}".
	RemarkTemplate string `json:"remarkTemplate,omitempty"`
}

type MigrationInboundPreview struct {
	Key       string `json:"key"`
	ServerKey string `json:"serverKey"`
	Remark    string `json:"remark"`
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
	Clients   int    `json:"clients"`
	Hosts     int    `json:"hosts"`
	Conflict  string `json:"conflict,omitempty"`
}

type MigrationTargetOption struct {
	Value   string `json:"value"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type MigrationClientStats struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Orphaned int `json:"orphaned"`
}

type MigrationPreview struct {
	Source   string                    `json:"source"`
	Servers  []MigrationSourceServer   `json:"servers"`
	Inbounds []MigrationInboundPreview `json:"inbounds"`
	Clients  MigrationClientStats      `json:"clients"`
	Hosts    int                       `json:"hosts"`
	Sub      MigrationSubInfo          `json:"sub"`
	Warnings []string                  `json:"warnings"`
	Targets  []MigrationTargetOption   `json:"targets"`
}

type MigrationItemError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type MigrationResult struct {
	InboundsCreated int                  `json:"inboundsCreated"`
	InboundErrors   []MigrationItemError `json:"inboundErrors"`
	Clients         BulkCreateResult     `json:"clients"`
	HostsCreated    int                  `json:"hostsCreated"`
	HostErrors      []MigrationItemError `json:"hostErrors"`
	SubApplied      bool                 `json:"subApplied"`
	NeedRestart     bool                 `json:"needRestart"`
	PanelRestart    bool                 `json:"panelRestart"`
}

// MigrationService imports inbounds, clients and hosts from another panel
// while keeping every credential and subscription id the end users hold.
type MigrationService struct {
	inboundService InboundService
	clientService  ClientService
	hostService    HostService
	nodeService    NodeService
	settingService SettingService
}

// ParseMigrationTargets decodes the {serverKey: target} JSON the UI sends.
func ParseMigrationTargets(raw string) (map[string]string, error) {
	targets := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return targets, nil
	}
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return nil, fmt.Errorf("invalid targets: %w", err)
	}
	return targets, nil
}

// resolveTargets fills every server with its explicit target or the suggestion,
// and rejects targets naming a node that does not exist.
func (s *MigrationService) resolveTargets(plan *migrationPlan, targets map[string]string) (map[string]*int, error) {
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		return nil, err
	}
	known := make(map[int]bool, len(nodes))
	for _, n := range nodes {
		known[n.Id] = true
	}
	out := make(map[string]*int, len(plan.servers))
	for i := range plan.servers {
		srv := &plan.servers[i]
		target, ok := targets[srv.Key]
		if !ok {
			target = srv.Suggested
		}
		switch {
		case target == MigrationTargetLocal:
			out[srv.Key] = nil
		case target == "" || target == MigrationTargetSkip:
			continue
		case strings.HasPrefix(target, "node:"):
			id, convErr := strconv.Atoi(strings.TrimPrefix(target, "node:"))
			if convErr != nil || !known[id] {
				return nil, fmt.Errorf("server %q: unknown target %q", srv.Name, target)
			}
			out[srv.Key] = &id
		default:
			return nil, fmt.Errorf("server %q: unknown target %q", srv.Name, target)
		}
	}
	return out, nil
}

// suggestTargets maps each source server to the node sharing its address, or
// to this panel when the address is this panel's own domain.
func (s *MigrationService) suggestTargets(plan *migrationPlan) error {
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		return err
	}
	self := map[string]bool{}
	for _, get := range []func() (string, error){s.settingService.GetWebDomain, s.settingService.GetSubDomain} {
		if v, gErr := get(); gErr == nil && strings.TrimSpace(v) != "" {
			self[strings.ToLower(strings.TrimSpace(v))] = true
		}
	}
	for i := range plan.servers {
		srv := &plan.servers[i]
		if srv.Suggested != "" {
			continue
		}
		addr := strings.ToLower(strings.TrimSpace(srv.Address))
		if addr == "" {
			continue
		}
		for _, n := range nodes {
			if strings.EqualFold(strings.TrimSpace(n.Address), addr) {
				srv.Suggested = "node:" + strconv.Itoa(n.Id)
				break
			}
		}
		if srv.Suggested == "" && self[addr] {
			srv.Suggested = MigrationTargetLocal
		}
	}
	return nil
}

func (s *MigrationService) targetOptions() ([]MigrationTargetOption, error) {
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		return nil, err
	}
	opts := []MigrationTargetOption{{Value: MigrationTargetLocal, Name: "local"}}
	for _, n := range nodes {
		opts = append(opts, MigrationTargetOption{Value: "node:" + strconv.Itoa(n.Id), Name: n.Name, Address: n.Address})
	}
	return opts, nil
}

func (s *MigrationService) preview(plan *migrationPlan, targets map[string]string) (*MigrationPreview, error) {
	if err := s.suggestTargets(plan); err != nil {
		return nil, err
	}
	resolved, err := s.resolveTargets(plan, targets)
	if err != nil {
		return nil, err
	}
	opts, err := s.targetOptions()
	if err != nil {
		return nil, err
	}
	clientsPerInbound := map[string]int{}
	stats := MigrationClientStats{Total: len(plan.clients)}
	for _, c := range plan.clients {
		if c.client.Enable {
			stats.Enabled++
		}
		if len(c.inboundKeys) == 0 {
			stats.Orphaned++
		}
		for _, k := range c.inboundKeys {
			clientsPerInbound[k]++
		}
	}
	hostsPerInbound := map[string]int{}
	for _, h := range plan.hosts {
		hostsPerInbound[h.inboundKey]++
	}
	for i := range plan.servers {
		plan.servers[i].Inbounds = 0
	}
	serverIdx := map[string]int{}
	for i := range plan.servers {
		serverIdx[plan.servers[i].Key] = i
	}
	out := &MigrationPreview{
		Source:   plan.source,
		Clients:  stats,
		Hosts:    len(plan.hosts),
		Sub:      plan.sub,
		Warnings: append([]string{}, plan.warnings...),
		Targets:  opts,
	}
	for _, in := range plan.inbounds {
		if i, ok := serverIdx[in.serverKey]; ok {
			plan.servers[i].Inbounds++
		}
		row := MigrationInboundPreview{
			Key:       in.key,
			ServerKey: in.serverKey,
			Remark:    in.inbound.Remark,
			Protocol:  string(in.inbound.Protocol),
			Port:      in.inbound.Port,
			Clients:   clientsPerInbound[in.key],
			Hosts:     hostsPerInbound[in.key],
		}
		if nodeID, ok := resolved[in.serverKey]; ok {
			probe := in.inbound
			probe.NodeID = nodeID
			conflict, cErr := s.inboundService.checkPortConflict(&probe, 0)
			if cErr != nil {
				return nil, cErr
			}
			if conflict != nil {
				row.Conflict = conflict.String()
			}
		}
		out.Inbounds = append(out.Inbounds, row)
	}
	out.Servers = plan.servers
	return out, nil
}

// apply creates the plan through the regular services, so every inbound and
// client reaches its node via the runtime exactly like a hand-made one.
func (s *MigrationService) apply(plan *migrationPlan, targets map[string]string, applySub bool, userID int) (*MigrationResult, error) {
	if err := s.suggestTargets(plan); err != nil {
		return nil, err
	}
	resolved, err := s.resolveTargets(plan, targets)
	if err != nil {
		return nil, err
	}
	res := &MigrationResult{InboundErrors: []MigrationItemError{}, HostErrors: []MigrationItemError{}}

	created := map[string]int{}
	for _, in := range plan.inbounds {
		nodeID, ok := resolved[in.serverKey]
		if !ok {
			continue
		}
		ib := in.inbound
		ib.NodeID = nodeID
		ib.UserId = userID
		saved, needRestart, addErr := s.inboundService.AddInbound(&ib)
		if addErr != nil {
			res.InboundErrors = append(res.InboundErrors, MigrationItemError{Name: in.inbound.Remark, Error: addErr.Error()})
			continue
		}
		res.NeedRestart = res.NeedRestart || needRestart
		created[in.key] = saved.Id
		res.InboundsCreated++
	}

	// Hosts go first so clients can be bound to the ones their source showed them.
	hostIDs := make([][]int, len(plan.hosts))
	for i, h := range plan.hosts {
		id, ok := created[h.inboundKey]
		if !ok {
			continue
		}
		group := h.group
		group.InboundIds = []int{id}
		rows, hErr := s.hostService.AddHostGroup(&group)
		if hErr != nil {
			res.HostErrors = append(res.HostErrors, MigrationItemError{Name: group.Remark, Error: hErr.Error()})
			continue
		}
		for _, row := range rows {
			hostIDs[i] = append(hostIDs[i], row.Id)
		}
		res.HostsCreated++
	}

	payloads := make([]ClientCreatePayload, 0, len(plan.clients))
	for _, c := range plan.clients {
		ids := make([]int, 0, len(c.inboundKeys))
		for _, k := range c.inboundKeys {
			if id, ok := created[k]; ok {
				ids = append(ids, id)
			}
		}
		client := c.client
		for _, hi := range c.hostIdx {
			client.ClientHostRuleIds = append(client.ClientHostRuleIds, hostIDs[hi]...)
		}
		payloads = append(payloads, ClientCreatePayload{Client: client, InboundIds: ids, LimitHwid: c.limitHwid, Traffic: c.traffic})
	}
	clientRes, needRestart, err := s.clientService.ImportClients(&s.inboundService, payloads)
	res.Clients = clientRes
	res.NeedRestart = res.NeedRestart || needRestart
	if res.HostsCreated > 0 {
		InvalidateSubData()
	}
	if err != nil {
		return res, err
	}

	if applySub {
		restart, subErr := s.applySubSettings(plan.sub)
		if subErr != nil {
			return res, subErr
		}
		res.SubApplied = true
		res.PanelRestart = restart
	}
	return res, nil
}

// applySubSettings moves this panel's subscription paths and public URI onto
// the source's, and reports whether the sub server must restart to serve them.
func (s *MigrationService) applySubSettings(sub MigrationSubInfo) (bool, error) {
	if sub.Path == "" {
		return false, errors.New("the source subscription address is unknown")
	}
	changes := map[string]string{"subPath": sub.Path}
	if sub.JsonPath != "" {
		changes["subJsonPath"] = sub.JsonPath
	}
	if sub.ClashPath != "" {
		changes["subClashPath"] = sub.ClashPath
	}
	if sub.URI != "" {
		changes["subURI"] = sub.URI
	}
	if sub.RemarkTemplate != "" {
		changes["remarkTemplate"] = sub.RemarkTemplate
	}
	restart := false
	for key, value := range changes {
		current, err := s.settingService.getString(key)
		if err != nil {
			return restart, err
		}
		if current == value {
			continue
		}
		if err := s.settingService.saveSetting(key, value); err != nil {
			return restart, err
		}
		restart = true
	}
	return restart, nil
}

// subInfoFromURL splits a full subscription link into its base and the path
// prefix in front of the subscription id.
func subInfoFromURL(raw, subID string) (MigrationSubInfo, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || subID == "" {
		return MigrationSubInfo{}, false
	}
	path := strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(path, "/"+subID) {
		return MigrationSubInfo{}, false
	}
	prefix := strings.TrimSuffix(path, subID)
	info := MigrationSubInfo{
		ExampleURL: raw,
		Scheme:     u.Scheme,
		Domain:     u.Hostname(),
		Path:       prefix,
		URI:        u.Scheme + "://" + u.Host + prefix,
	}
	if p := u.Port(); p != "" {
		info.Port, _ = strconv.Atoi(p)
	} else if u.Scheme == "http" {
		info.Port = 80
	} else {
		info.Port = 443
	}
	return info, true
}
