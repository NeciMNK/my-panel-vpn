package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/wireguard"
)

// WarpAccount is this panel's own WARP registration (integration.WarpService),
// passed in because that package already imports this one.
type WarpAccount interface {
	GetWarpData() (string, error)
	RegWarp(secretKey, publicKey string) (string, error)
	GetWarpConfig() (string, error)
}

// WarpSwitchService turns WARP on per server with one switch: it registers
// the server's WARP account when needed and routes all or some traffic into it.
type WarpSwitchService struct{}

// WarpSwitchRequest is one server's switch position.
type WarpSwitchRequest struct {
	NodeId  int      `json:"nodeId" example:"0"`
	Mode    string   `json:"mode" example:"domains"`
	Domains []string `json:"domains"`
}

const (
	WarpOff     = "off"
	WarpAll     = "all"
	WarpDomains = "domains"
	// warpOutboundTag is the stock 3x-ui WARP outbound: the hourly IP rotation
	// job rewrites exactly this tag, so the switch reuses it.
	warpOutboundTag = "warp"
	warpRulePrefix  = managedTagPrefix + "warp-"
)

func (s *WarpSwitchService) List() ([]*model.WarpSwitch, error) {
	var out []*model.WarpSwitch
	err := database.GetDB().Order("node_id asc").Find(&out).Error
	return out, err
}

func normalizeWarpRequest(req *WarpSwitchRequest) error {
	if req == nil {
		return common.NewError("warp request is required")
	}
	if !slices.Contains([]string{WarpOff, WarpAll, WarpDomains}, req.Mode) {
		return common.NewError("warp mode must be off, all or domains")
	}
	req.Domains = trimmedSet(req.Domains)
	if req.Mode == WarpDomains && len(req.Domains) == 0 {
		return common.NewError("list the sites that should go through WARP")
	}
	return nil
}

// Set moves one server's switch and rewrites that server's template.
func (s *WarpSwitchService) Set(ctx context.Context, req *WarpSwitchRequest, local WarpAccount) (*model.WarpSwitch, error) {
	if err := normalizeWarpRequest(req); err != nil {
		return nil, err
	}
	if req.NodeId != 0 {
		if ok, err := (&NodeService{}).NodeExists(req.NodeId); err != nil || !ok {
			return nil, common.NewError(fmt.Sprintf("node %d does not exist", req.NodeId))
		}
	}
	cascadeMu.Lock()
	defer cascadeMu.Unlock()
	row := &model.WarpSwitch{}
	database.GetDB().Where("node_id = ?", req.NodeId).First(row)
	row.NodeId, row.Mode, row.Domains = req.NodeId, req.Mode, req.Domains
	err := s.apply(ctx, row, local)
	row.LastError = ""
	if err != nil {
		row.LastError = err.Error()
	}
	if saveErr := database.GetDB().Save(row).Error; saveErr != nil {
		return nil, saveErr
	}
	return row, err
}

func (s *WarpSwitchService) apply(ctx context.Context, row *model.WarpSwitch, local WarpAccount) error {
	store := templateStoreFor(row.NodeId)
	tmpl, err := store.Load(ctx)
	if err != nil {
		return err
	}
	stripManaged(tmpl.Config, warpRulePrefix)
	if row.Mode != WarpOff {
		if !hasOutbound(tmpl.Config, warpOutboundTag) {
			outbound, err := registerWarp(ctx, row.NodeId, local)
			if err != nil {
				return fmt.Errorf("register WARP: %w", err)
			}
			appendOutbounds(tmpl.Config, outbound)
		}
		applyWarpRule(tmpl.Config, row)
	}
	return store.Save(ctx, tmpl)
}

func hasOutbound(cfg map[string]any, tag string) bool {
	for _, o := range templateList(cfg, "outbounds") {
		if tagOf(o, "tag") == tag {
			return true
		}
	}
	return false
}

// applyWarpRule adds the switch's rule as the very last one: home networks,
// cascades and the admin's own rules all decide before WARP takes the rest.
func applyWarpRule(cfg map[string]any, row *model.WarpSwitch) {
	rule := map[string]any{"type": "field", "outboundTag": warpOutboundTag}
	if row.Mode == WarpAll {
		rule["ruleTag"] = warpRulePrefix + "all"
		rule["network"] = "tcp,udp"
	} else {
		rule["ruleTag"] = warpRulePrefix + "domains"
		rule["domain"] = row.Domains
	}
	appendTailRules(cfg, rule)
}

// warpCall runs one of the server's /panel/api/xray/warp/<action> handlers.
func warpCall(ctx context.Context, nodeID int, local WarpAccount, action string, form url.Values) (string, error) {
	if nodeID == 0 {
		if local == nil {
			return "", errors.New("no WARP account for this panel")
		}
		switch action {
		case "data":
			return local.GetWarpData()
		case "reg":
			return local.RegWarp(form.Get("privateKey"), form.Get("publicKey"))
		default:
			return local.GetWarpConfig()
		}
	}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, nodeID, http.MethodPost, "/warp/"+action, "",
		"application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return "", err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return "", err
	}
	var out string
	if len(obj) > 0 && string(obj) != "null" {
		if err := json.Unmarshal(obj, &out); err != nil {
			return "", err
		}
	}
	return out, nil
}

// registerWarp reuses the server's WARP account or creates one, then builds
// the outbound the same way the panel's WARP dialog does.
func registerWarp(ctx context.Context, nodeID int, local WarpAccount) (map[string]any, error) {
	data, err := warpCall(ctx, nodeID, local, "data", url.Values{})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(data) == "" {
		priv, pub, err := wireguard.GenerateWireguardKeypair()
		if err != nil {
			return nil, err
		}
		if _, err := warpCall(ctx, nodeID, local, "reg", url.Values{"privateKey": {priv}, "publicKey": {pub}}); err != nil {
			return nil, err
		}
		if data, err = warpCall(ctx, nodeID, local, "data", url.Values{}); err != nil {
			return nil, err
		}
	}
	config, err := warpCall(ctx, nodeID, local, "config", url.Values{})
	if err != nil {
		return nil, err
	}
	return buildWarpOutbound(data, config)
}

func buildWarpOutbound(dataJSON, configJSON string) (map[string]any, error) {
	var data struct {
		PrivateKey string `json:"private_key"`
		ClientID   string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return nil, fmt.Errorf("warp data: %w", err)
	}
	var account struct {
		Config struct {
			ClientID  string `json:"client_id"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					Host string `json:"host"`
				} `json:"endpoint"`
			} `json:"peers"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(configJSON), &account); err != nil {
		return nil, fmt.Errorf("warp config: %w", err)
	}
	cfg := account.Config
	if len(cfg.Peers) == 0 || data.PrivateKey == "" {
		return nil, errors.New("WARP returned no peer or key")
	}
	var addresses []string
	if cfg.Interface.Addresses.V4 != "" {
		addresses = append(addresses, cfg.Interface.Addresses.V4+"/32")
	}
	if cfg.Interface.Addresses.V6 != "" {
		addresses = append(addresses, cfg.Interface.Addresses.V6+"/128")
	}
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = data.ClientID
	}
	reserved := []int{}
	if raw, err := base64.StdEncoding.DecodeString(clientID); err == nil {
		for _, b := range raw {
			reserved = append(reserved, int(b))
		}
	}
	return map[string]any{
		"tag":            warpOutboundTag,
		"protocol":       "wireguard",
		"streamSettings": map[string]any{"sockopt": map[string]any{"domainStrategy": "ForceIPv4v6"}},
		"targetStrategy": "ForceIPv4v6",
		"settings": map[string]any{
			"mtu":         1420,
			"secretKey":   data.PrivateKey,
			"address":     addresses,
			"reserved":    reserved,
			"peers":       []any{map[string]any{"publicKey": cfg.Peers[0].PublicKey, "endpoint": cfg.Peers[0].Endpoint.Host}},
			"noKernelTun": true,
		},
	}, nil
}
