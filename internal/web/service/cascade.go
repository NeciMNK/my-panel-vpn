package service

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// CascadeService wires entry→exit chains: a hidden VLESS+REALITY inbound per exit
// and, on the entry, an outbound per exit plus a catch-all rule or balancer.
type CascadeService struct{}

// CascadeRequest is the "two clicks" form: an entry, its exits, optionally
// which entry inbounds to chain (empty = all) and the multi-exit strategy.
type CascadeRequest struct {
	Name             string   `json:"name" example:"RU → DE"`
	Enable           bool     `json:"enable" example:"true"`
	EntryNodeId      int      `json:"entryNodeId" example:"1"`
	ExitNodeIds      []int    `json:"exitNodeIds" example:"2"`
	ExitCountry      string   `json:"exitCountry" example:"DE"`
	EntryInboundTags []string `json:"entryInboundTags"`
	Strategy         string   `json:"strategy" example:"leastPing"`
	Mode             string   `json:"mode" example:"split"`
	DirectDomains    []string `json:"directDomains"`
	DirectIPs        []string `json:"directIps"`
	RelayInboundTags []string `json:"relayInboundTags"`
}

var cascadeMu sync.Mutex

var cascadeStrategies = []string{"leastPing", "leastLoad", "random", "roundRobin"}

// Hosts a REALITY link borrows a TLS identity from; all serve TLS 1.3 + H2.
var cascadeRealityTargets = []string{"www.microsoft.com", "www.apple.com", "dl.google.com", "www.amazon.com"}

func cascadePrefix(id int) string          { return fmt.Sprintf("%scascade-%d-", managedTagPrefix, id) }
func cascadeRouteTag(id int) string        { return cascadePrefix(id) + "route" }
func cascadeLinkTag(id, nodeID int) string { return fmt.Sprintf("%sn%d", cascadePrefix(id), nodeID) }

func (s *CascadeService) List() ([]*model.Cascade, error) {
	var out []*model.Cascade
	err := database.GetDB().Order("id asc").Find(&out).Error
	return out, err
}

func (s *CascadeService) get(id int) (*model.Cascade, error) {
	c := &model.Cascade{}
	if err := database.GetDB().Where("id = ?", id).First(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

func (s *CascadeService) validate(req *CascadeRequest) error {
	if req == nil {
		return common.NewError("cascade request is required")
	}
	req.ExitCountry = strings.ToUpper(strings.TrimSpace(req.ExitCountry))
	if req.ExitCountry != "" {
		if !isCountryCode(req.ExitCountry) {
			return common.NewError("country must be a two-letter code such as DE")
		}
		exits, err := countryExits(req.ExitCountry, req.EntryNodeId)
		if err != nil {
			return err
		}
		req.ExitNodeIds = exits
	} else if len(req.ExitNodeIds) == 0 {
		return common.NewError("pick at least one exit server")
	}
	seen := map[int]bool{}
	for _, id := range req.ExitNodeIds {
		if id == req.EntryNodeId {
			return common.NewError("the entry server cannot also be an exit")
		}
		if seen[id] {
			return common.NewError("an exit server is listed twice")
		}
		seen[id] = true
	}
	if req.Strategy == "" {
		req.Strategy = "leastPing"
	}
	if !slices.Contains(cascadeStrategies, req.Strategy) {
		return common.NewError("unknown balancing strategy " + req.Strategy)
	}
	if err := normalizeCascadeRouting(req); err != nil {
		return err
	}
	for _, id := range append([]int{req.EntryNodeId}, req.ExitNodeIds...) {
		if id == 0 {
			continue
		}
		if ok, err := (&NodeService{}).NodeExists(id); err != nil || !ok {
			return common.NewError(fmt.Sprintf("node %d does not exist", id))
		}
	}
	return nil
}

// Create stores the cascade and provisions it. masterAddress is how exits
// reach this panel when it is an exit itself (the host the admin browses).
func (s *CascadeService) Create(ctx context.Context, req *CascadeRequest, userID int, masterAddress string) (*model.Cascade, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	c := &model.Cascade{
		Name: strings.TrimSpace(req.Name), Enable: req.Enable, EntryNodeId: req.EntryNodeId,
		ExitNodeIds: req.ExitNodeIds, ExitCountry: req.ExitCountry, EntryInboundTags: req.EntryInboundTags,
		Strategy: req.Strategy, Mode: req.Mode, DirectDomains: req.DirectDomains,
		DirectIPs: req.DirectIPs, RelayInboundTags: req.RelayInboundTags,
	}
	if err := database.GetDB().Create(c).Error; err != nil {
		return nil, err
	}
	if c.Name == "" {
		c.Name = fmt.Sprintf("cascade-%d", c.Id)
	}
	err := s.apply(ctx, c, -1, userID, masterAddress)
	s.saveState(c, err)
	return c, err
}

func (s *CascadeService) Update(ctx context.Context, id int, req *CascadeRequest, userID int, masterAddress string) (*model.Cascade, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	prevEntry := c.EntryNodeId
	c.Name = strings.TrimSpace(req.Name)
	if c.Name == "" {
		c.Name = fmt.Sprintf("cascade-%d", c.Id)
	}
	c.Enable = req.Enable
	c.EntryNodeId = req.EntryNodeId
	c.ExitNodeIds = req.ExitNodeIds
	c.ExitCountry = req.ExitCountry
	c.EntryInboundTags = req.EntryInboundTags
	c.Strategy = req.Strategy
	c.Mode = req.Mode
	c.DirectDomains = req.DirectDomains
	c.DirectIPs = req.DirectIPs
	c.RelayInboundTags = req.RelayInboundTags
	err = s.apply(ctx, c, prevEntry, userID, masterAddress)
	s.saveState(c, err)
	return c, err
}

func (s *CascadeService) Delete(ctx context.Context, id int) error {
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	c, err := s.get(id)
	if err != nil {
		return err
	}
	if err := s.rewriteEntry(ctx, c.EntryNodeId, c, false); err != nil {
		return fmt.Errorf("remove cascade from the entry server: %w", err)
	}
	for _, l := range c.Links {
		s.removeLink(l)
	}
	if err := database.GetDB().Where("cascade_id = ?", id).Delete(&model.CascadePin{}).Error; err != nil {
		return err
	}
	return database.GetDB().Delete(&model.Cascade{}, id).Error
}

func (s *CascadeService) saveState(c *model.Cascade, applyErr error) {
	c.LastError = ""
	if applyErr != nil {
		c.LastError = applyErr.Error()
	}
	if err := database.GetDB().Save(c).Error; err != nil {
		logger.Warning("save cascade", c.Id, ":", err)
	}
}

// apply converges exits first (so the entry never points at a missing hop),
// then rewrites the entry template; prevEntry < 0 means a brand-new cascade.
func (s *CascadeService) apply(ctx context.Context, c *model.Cascade, prevEntry, userID int, masterAddress string) error {
	wanted := map[int]bool{}
	if c.Enable {
		for _, id := range c.ExitNodeIds {
			wanted[id] = true
		}
	}
	kept := make([]model.CascadeLink, 0, len(c.Links))
	for _, l := range c.Links {
		if wanted[l.NodeId] {
			kept = append(kept, l)
			delete(wanted, l.NodeId)
			continue
		}
		s.removeLink(l)
	}
	c.Links = kept
	for _, id := range c.ExitNodeIds {
		if !wanted[id] {
			continue
		}
		link, err := s.provisionLink(c, id, userID, masterAddress)
		if err != nil {
			return fmt.Errorf("exit %s: %w", nodeLabel(id), err)
		}
		c.Links = append(c.Links, link)
	}
	if prevEntry >= 0 && prevEntry != c.EntryNodeId {
		if err := s.rewriteEntry(ctx, prevEntry, c, false); err != nil {
			return fmt.Errorf("previous entry %s: %w", nodeLabel(prevEntry), err)
		}
	}
	if err := s.rewriteEntry(ctx, c.EntryNodeId, c, c.Enable); err != nil {
		return fmt.Errorf("entry %s: %w", nodeLabel(c.EntryNodeId), err)
	}
	return nil
}

func nodeLabel(id int) string {
	if id == 0 {
		return "this panel"
	}
	if n, err := (&NodeService{}).GetById(id); err == nil {
		return n.Name
	}
	return fmt.Sprintf("node %d", id)
}

func (s *CascadeService) rewriteEntry(ctx context.Context, nodeID int, c *model.Cascade, install bool) error {
	store := templateStoreFor(nodeID)
	tmpl, err := store.Load(ctx)
	if err != nil {
		return err
	}
	stripManaged(tmpl.Config, cascadePrefix(c.Id))
	if install && len(c.Links) > 0 {
		pins, err := loadCascadePins(c.Id)
		if err != nil {
			return err
		}
		ai, err := loadAIExitPlan()
		if err != nil {
			return err
		}
		applyCascadeToTemplate(tmpl.Config, c, pins, ai.Domains, splitZone(nodeCountry(nodeID)))
	}
	return store.Save(ctx, tmpl)
}

// applyCascadeToTemplate adds one outbound per exit and tail rules: relay-only
// inbounds first, then the split's direct rules, then the catch-all to the exit.
func applyCascadeToTemplate(cfg map[string]any, c *model.Cascade, pins []model.CascadePin, aiDomains map[string]int, zone cascadeZone) {
	for _, l := range c.Links {
		appendOutbounds(cfg, cascadeOutbound(c.Id, l))
	}
	mode := cascadeMode(c)
	target := map[string]any{}
	if len(c.Links) == 1 {
		target["outboundTag"] = cascadeLinkTag(c.Id, c.Links[0].NodeId)
	} else {
		selector := cascadePrefix(c.Id) + "n"
		appendBalancer(cfg, map[string]any{
			"tag":         cascadeRouteTag(c.Id),
			"selector":    []string{selector},
			"strategy":    map[string]any{"type": c.Strategy},
			"fallbackTag": cascadeLinkTag(c.Id, c.Links[0].NodeId),
		})
		switch c.Strategy {
		case "leastPing":
			observeSubjects(cfg, "observatory", selector)
		case "leastLoad":
			observeSubjects(cfg, "burstObservatory", selector)
		}
		target["balancerTag"] = cascadeRouteTag(c.Id)
	}
	multi := len(c.Links) > 1
	if mode == CascadeModeBoth {
		if multi {
			appendTailRules(cfg, cascadeAIRules(c, aiDomains, cascadePrefix(c.Id)+"relay-", c.RelayInboundTags)...)
			appendTailRules(cfg, cascadePinRules(c, pins, cascadePrefix(c.Id)+"relay-", c.RelayInboundTags)...)
		}
		appendTailRules(cfg, cascadeRule(cascadePrefix(c.Id)+"relay", c.RelayInboundTags, target))
	}
	if mode != CascadeModeRelay {
		appendOutbounds(cfg, map[string]any{"tag": cascadeDirectTag(c.Id), "protocol": "freedom"})
		direct := map[string]any{"outboundTag": cascadeDirectTag(c.Id)}
		domain := cascadeRule(cascadePrefix(c.Id)+"direct-domain", c.EntryInboundTags, direct)
		domain["domain"] = append(slices.Clone(zone.Domains), c.DirectDomains...)
		ip := cascadeRule(cascadePrefix(c.Id)+"direct-ip", c.EntryInboundTags, direct)
		ip["ip"] = append(slices.Clone(zone.IPs), c.DirectIPs...)
		appendTailRules(cfg, domain, ip)
	}
	if multi {
		appendTailRules(cfg, cascadeAIRules(c, aiDomains, cascadePrefix(c.Id), c.EntryInboundTags)...)
		appendTailRules(cfg, cascadePinRules(c, pins, cascadePrefix(c.Id), c.EntryInboundTags)...)
	}
	appendTailRules(cfg, cascadeRule(cascadeRouteTag(c.Id), c.EntryInboundTags, target))
}

// cascadeRule is a tcp+udp field rule for inbounds (all when empty) to target.
func cascadeRule(tag string, inbounds []string, target map[string]any) map[string]any {
	rule := map[string]any{"type": "field", "ruleTag": tag, "network": "tcp,udp"}
	if len(inbounds) > 0 {
		rule["inboundTag"] = inbounds
	}
	for k, v := range target {
		rule[k] = v
	}
	return rule
}

func cascadeOutbound(cascadeID int, l model.CascadeLink) map[string]any {
	return linkOutbound(cascadeLinkTag(cascadeID, l.NodeId), l)
}

// linkOutbound dials a hidden VLESS+REALITY link inbound on another server.
func linkOutbound(tag string, l model.CascadeLink) map[string]any {
	return map[string]any{
		"tag":      tag,
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []any{map[string]any{
				"address": l.Address,
				"port":    l.Port,
				"users": []any{map[string]any{
					"id": l.ClientId, "encryption": "none", "flow": "xtls-rprx-vision",
				}},
			}},
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"serverName":  l.ServerName,
				"fingerprint": "chrome",
				"publicKey":   l.PublicKey,
				"shortId":     l.ShortId,
				"spiderX":     "/",
			},
		},
	}
}

func newRealityKeys() (privateKey, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(key.Bytes()), enc.EncodeToString(key.PublicKey().Bytes()), nil
}

func randomInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

func (s *CascadeService) linkAddress(nodeID int, masterAddress string) (string, error) {
	if nodeID == 0 {
		if masterAddress == "" {
			return "", common.NewError("cannot tell this panel's public address; open the panel by its public address")
		}
		return masterAddress, nil
	}
	n, err := (&NodeService{}).GetById(nodeID)
	if err != nil {
		return "", err
	}
	return n.Address, nil
}

func (s *CascadeService) provisionLink(c *model.Cascade, nodeID, userID int, masterAddress string) (model.CascadeLink, error) {
	return provisionHiddenLink(cascadePrefix(c.Id), c.Name+" (cascade link)", nodeID, userID, masterAddress)
}

func linkInboundTag(prefix string, nodeID int) string { return fmt.Sprintf("%sin-n%d", prefix, nodeID) }

// provisionHiddenLink creates a VLESS+REALITY inbound on nodeID that only the
// panel's own outbounds use; its tag and client email start with prefix.
func provisionHiddenLink(prefix, remark string, nodeID, userID int, masterAddress string) (model.CascadeLink, error) {
	addr, err := (&CascadeService{}).linkAddress(nodeID, masterAddress)
	if err != nil {
		return model.CascadeLink{}, err
	}
	priv, pub, err := newRealityKeys()
	if err != nil {
		return model.CascadeLink{}, err
	}
	sid := make([]byte, 4)
	if _, err := rand.Read(sid); err != nil {
		return model.CascadeLink{}, err
	}
	link := model.CascadeLink{
		NodeId: nodeID, Address: addr, ClientId: uuid.NewString(),
		Email:      fmt.Sprintf("%sn%d", prefix, nodeID),
		PublicKey:  pub,
		ShortId:    hex.EncodeToString(sid),
		ServerName: cascadeRealityTargets[randomInt(len(cascadeRealityTargets))],
	}
	settings, _ := json.Marshal(map[string]any{
		"clients": []any{map[string]any{
			"id": link.ClientId, "email": link.Email, "flow": "xtls-rprx-vision", "enable": true,
		}},
		"decryption": "none",
		"fallbacks":  []any{},
	})
	stream, _ := json.Marshal(map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"show": false, "xver": 0,
			"target":      link.ServerName + ":443",
			"serverNames": []string{link.ServerName},
			"privateKey":  priv,
			"shortIds":    []string{link.ShortId},
			"settings": map[string]any{
				"publicKey": pub, "fingerprint": "chrome", "serverName": "", "spiderX": "/",
			},
		},
		"tcpSettings": map[string]any{"acceptProxyProtocol": false, "header": map[string]any{"type": "none"}},
	})
	var nodeRef *int
	if nodeID != 0 {
		id := nodeID
		nodeRef = &id
	}
	inboundSvc := &InboundService{}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		port := 20000 + randomInt(40000)
		ib := &model.Inbound{
			UserId: userID, NodeID: nodeRef, Enable: true, Port: port, Protocol: model.VLESS,
			Remark:         remark,
			Tag:            linkInboundTag(prefix, nodeID),
			Settings:       string(settings),
			StreamSettings: string(stream),
			Sniffing:       `{"enabled":false,"destOverride":[]}`,
		}
		created, needRestart, err := inboundSvc.AddInbound(ib)
		if err != nil {
			lastErr = err
			if strings.Contains(strings.ToLower(err.Error()), "port") {
				continue
			}
			return model.CascadeLink{}, err
		}
		if needRestart {
			(&XrayService{}).SetToNeedRestart()
		}
		link.InboundId = created.Id
		link.Port = created.Port
		return link, nil
	}
	return model.CascadeLink{}, fmt.Errorf("no free port for the link: %w", lastErr)
}

func (s *CascadeService) removeLink(l model.CascadeLink) {
	inboundSvc := &InboundService{}
	if l.InboundId > 0 {
		needRestart, err := inboundSvc.DelInbound(l.InboundId)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warning("cascade: delete link inbound", l.InboundId, ":", err)
		}
		if needRestart {
			(&XrayService{}).SetToNeedRestart()
		}
	}
	if l.Email != "" {
		if _, err := (&ClientService{}).DeleteByEmail(inboundSvc, l.Email, false); err != nil {
			logger.Debug("cascade: delete link client", l.Email, ":", err)
		}
	}
}

// countryExits lists the enabled nodes of a country, entry excluded, in id order.
func countryExits(country string, entryNodeID int) ([]int, error) {
	var ids []int
	err := database.GetDB().Model(model.Node{}).
		Where("country = ? AND enable = ? AND id <> ?", country, true, entryNodeID).
		Order("id asc").Pluck("id", &ids).Error
	return ids, err
}

// ReconcileCountryCascades re-resolves country cascades after nodes were
// added, removed, re-enabled or moved to another country, and re-applies those
// whose exit set changed.
func (s *CascadeService) ReconcileCountryCascades(ctx context.Context) {
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	var rows []*model.Cascade
	if err := database.GetDB().Where("exit_country <> ''").Find(&rows).Error; err != nil {
		logger.Warning("cascade: list country cascades:", err)
		return
	}
	for _, c := range rows {
		exits, err := countryExits(c.ExitCountry, c.EntryNodeId)
		if err != nil {
			logger.Warning("cascade", c.Id, ": resolve country exits:", err)
			continue
		}
		if slices.Equal(exits, c.ExitNodeIds) {
			continue
		}
		c.ExitNodeIds = exits
		err = s.apply(ctx, c, c.EntryNodeId, 1, "")
		s.saveState(c, err)
	}
}
