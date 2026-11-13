package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// Everything this fork writes into an Xray template carries a tag that starts
// with managedTagPrefix, so a feature can strip and rewrite only its own part.
const managedTagPrefix = model.ManagedClientPrefix

var errManagedInboundClient = common.NewError("this is a service inbound of a cascade or home network link: only the panel's own clients can be on it")

// refuseRegularClientsOnManagedInbound keeps users off the panel's hidden link
// inbounds, where they would bypass the cascade's routing and pins.
func refuseRegularClientsOnManagedInbound(inbound *model.Inbound, clients []model.Client) error {
	if !strings.HasPrefix(inbound.Tag, managedTagPrefix) {
		return nil
	}
	for _, c := range clients {
		if !strings.HasPrefix(c.Email, managedTagPrefix) {
			return errManagedInboundClient
		}
	}
	return nil
}

// xrayTemplate is one panel's Xray template plus the facts needed to edit it.
type xrayTemplate struct {
	Config          map[string]any
	InboundTags     []string
	OutboundTestURL string
}

// xrayTemplateStore loads and saves the Xray template of the master itself
// (nodeID 0) or of a node, through the node's own xray settings API.
type xrayTemplateStore struct {
	nodeID int
}

func templateStoreFor(nodeID int) xrayTemplateStore { return xrayTemplateStore{nodeID: nodeID} }

func (s xrayTemplateStore) Load(ctx context.Context) (*xrayTemplate, error) {
	if s.nodeID == 0 {
		return loadLocalTemplate()
	}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, s.nodeID, http.MethodPost, "/", "", "", nil)
	if err != nil {
		return nil, err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return nil, err
	}
	var payload string
	if err := json.Unmarshal(obj, &payload); err != nil {
		return nil, fmt.Errorf("node %d: xray settings payload: %w", s.nodeID, err)
	}
	return parseTemplatePayload(payload)
}

func (s xrayTemplateStore) Save(ctx context.Context, t *xrayTemplate) error {
	raw, err := json.Marshal(t.Config)
	if err != nil {
		return err
	}
	testURL := t.OutboundTestURL
	if testURL == "" {
		testURL = "https://www.google.com/generate_204"
	}
	if s.nodeID == 0 {
		return saveLocalTemplate(string(raw), testURL)
	}
	form := url.Values{"xraySetting": {string(raw)}, "outboundTestUrl": {testURL}}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, s.nodeID, http.MethodPost, "/update", "",
		"application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return err
	}
	_, err = proxyEnvelopeObj(resp)
	return err
}

func proxyEnvelopeObj(resp *runtime.XrayProxyResponse) (json.RawMessage, error) {
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("node answered HTTP %d", resp.Status)
	}
	var env struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("node reply: %w", err)
	}
	if !env.Success {
		return nil, errors.New("node: " + env.Msg)
	}
	return env.Obj, nil
}

func parseTemplatePayload(payload string) (*xrayTemplate, error) {
	var resp struct {
		XraySetting     map[string]any `json:"xraySetting"`
		InboundTags     []string       `json:"inboundTags"`
		OutboundTestURL string         `json:"outboundTestUrl"`
	}
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return nil, fmt.Errorf("xray settings payload: %w", err)
	}
	if resp.XraySetting == nil {
		return nil, errors.New("xray settings payload has no template")
	}
	return &xrayTemplate{Config: resp.XraySetting, InboundTags: resp.InboundTags, OutboundTestURL: resp.OutboundTestURL}, nil
}

func loadLocalTemplate() (*xrayTemplate, error) {
	settings := SettingService{}
	raw, err := settings.GetXrayConfigTemplate()
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(UnwrapXrayTemplateConfig(raw)), &cfg); err != nil {
		return nil, fmt.Errorf("local xray template: %w", err)
	}
	tagsJSON, err := (&InboundService{}).GetInboundTags()
	if err != nil {
		return nil, err
	}
	var tags []string
	_ = json.Unmarshal([]byte(tagsJSON), &tags)
	testURL, _ := settings.GetXrayOutboundTestUrl()
	return &xrayTemplate{Config: cfg, InboundTags: tags, OutboundTestURL: testURL}, nil
}

func saveLocalTemplate(raw, testURL string) error {
	if err := (&XraySettingService{}).SaveXraySetting(raw); err != nil {
		return err
	}
	if err := (&SettingService{}).SetXrayOutboundTestUrl(testURL); err != nil {
		return err
	}
	xs := XrayService{}
	if xs.IsXrayRunning() {
		return xs.RestartXray(false)
	}
	return nil
}

// ---- pure template edits ----

func templateList(cfg map[string]any, key string) []any {
	if v, ok := cfg[key].([]any); ok {
		return v
	}
	return nil
}

func templateRouting(cfg map[string]any) map[string]any {
	r, ok := cfg["routing"].(map[string]any)
	if !ok {
		r = map[string]any{}
		cfg["routing"] = r
	}
	return r
}

func tagOf(item any, key string) string {
	if m, ok := item.(map[string]any); ok {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

func dropByTagPrefix(items []any, key, prefix string) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		if strings.HasPrefix(tagOf(it, key), prefix) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// stripManaged removes every outbound, rule, balancer and observatory subject
// owned by prefix (e.g. "mp-cascade-3-").
func stripManaged(cfg map[string]any, prefix string) {
	if obs := templateList(cfg, "outbounds"); obs != nil {
		cfg["outbounds"] = dropByTagPrefix(obs, "tag", prefix)
	}
	routing := templateRouting(cfg)
	if rules, ok := routing["rules"].([]any); ok {
		routing["rules"] = dropByTagPrefix(rules, "ruleTag", prefix)
	}
	if bals, ok := routing["balancers"].([]any); ok {
		kept := dropByTagPrefix(bals, "tag", prefix)
		if len(kept) == 0 {
			delete(routing, "balancers")
		} else {
			routing["balancers"] = kept
		}
	}
	for _, key := range []string{"observatory", "burstObservatory"} {
		obs, ok := cfg[key].(map[string]any)
		if !ok {
			continue
		}
		sel, _ := obs["subjectSelector"].([]any)
		kept := make([]any, 0, len(sel))
		for _, s := range sel {
			if str, ok := s.(string); ok && strings.HasPrefix(str, prefix) {
				continue
			}
			kept = append(kept, s)
		}
		if len(kept) == 0 && len(sel) > 0 {
			delete(cfg, key)
			continue
		}
		obs["subjectSelector"] = kept
	}
}

func appendOutbounds(cfg map[string]any, outbounds ...map[string]any) {
	list := templateList(cfg, "outbounds")
	for _, o := range outbounds {
		list = append(list, o)
	}
	cfg["outbounds"] = list
}

func appendBalancer(cfg map[string]any, balancer map[string]any) {
	routing := templateRouting(cfg)
	bals, _ := routing["balancers"].([]any)
	routing["balancers"] = append(bals, balancer)
}

// observeSubjects adds selectors to the template's observatory, creating one
// when missing; stripManaged drops it again once its last subject is ours.
func observeSubjects(cfg map[string]any, key string, selectors ...string) {
	obs, ok := cfg[key].(map[string]any)
	if !ok {
		obs = map[string]any{}
		if key == "observatory" {
			obs["probeURL"] = "https://www.google.com/generate_204"
			obs["probeInterval"] = "30s"
			obs["enableConcurrency"] = true
		} else {
			obs["pingConfig"] = map[string]any{
				"destination": "https://www.google.com/generate_204",
				"interval":    "30s",
				"sampling":    3,
				"timeout":     "5s",
			}
		}
		cfg[key] = obs
	}
	sel, _ := obs["subjectSelector"].([]any)
	for _, s := range selectors {
		sel = append(sel, s)
	}
	obs["subjectSelector"] = sel
}

// Rule placement: "head" rules (home networks, sticky sessions) must win over
// the user's own rules, "tail" rules (cascades) only catch what nothing else did.
func insertHeadRules(cfg map[string]any, rules ...map[string]any) {
	routing := templateRouting(cfg)
	existing, _ := routing["rules"].([]any)
	at := 0
	for i, r := range existing {
		if m, ok := r.(map[string]any); ok && isAPIRule(m) {
			at = i + 1
		}
	}
	out := make([]any, 0, len(existing)+len(rules))
	out = append(out, existing[:at]...)
	for _, r := range rules {
		out = append(out, r)
	}
	out = append(out, existing[at:]...)
	routing["rules"] = out
}

// appendTailRules keeps the WARP switch's rule last, so it only takes what
// cascades and every other rule left over.
func appendTailRules(cfg map[string]any, rules ...map[string]any) {
	routing := templateRouting(cfg)
	existing, _ := routing["rules"].([]any)
	at := len(existing)
	for i, r := range existing {
		if strings.HasPrefix(tagOf(r, "ruleTag"), warpRulePrefix) {
			at = i
			break
		}
	}
	out := make([]any, 0, len(existing)+len(rules))
	out = append(out, existing[:at]...)
	for _, r := range rules {
		out = append(out, r)
	}
	out = append(out, existing[at:]...)
	routing["rules"] = out
}

func isAPIRule(rule map[string]any) bool {
	if rule["outboundTag"] == "api" {
		return true
	}
	tags, _ := rule["inboundTag"].([]any)
	for _, t := range tags {
		if t == "api" {
			return true
		}
	}
	return false
}
