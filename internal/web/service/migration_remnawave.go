package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
)

// RemnawaveSnapshot is what the importer reads from Remnawave's REST API; the
// same JSON can be uploaded as a file instead of fetching it live.
type RemnawaveSnapshot struct {
	Users          []remnawaveUser    `json:"users"`
	ConfigProfiles []remnawaveProfile `json:"configProfiles"`
	InternalSquads []remnawaveSquad   `json:"internalSquads"`
	Hosts          []remnawaveHost    `json:"hosts"`
	Nodes          []remnawaveNode    `json:"nodes"`
}

// flexInt accepts a JSON number or numeric string; Remnawave versions differ.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", s)
	}
	*f = flexInt(v)
	return nil
}

type remnawaveRef struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

type remnawaveUser struct {
	ShortUUID            string   `json:"shortUuid"`
	Username             string   `json:"username"`
	Status               string   `json:"status"`
	TrafficLimitBytes    flexInt  `json:"trafficLimitBytes"`
	TrafficLimitStrategy string   `json:"trafficLimitStrategy"`
	ExpireAt             string   `json:"expireAt"`
	TelegramID           *flexInt `json:"telegramId"`
	Description          *string  `json:"description"`
	Tag                  *string  `json:"tag"`
	HwidDeviceLimit      *flexInt `json:"hwidDeviceLimit"`
	TrojanPassword       string   `json:"trojanPassword"`
	VlessUUID            string   `json:"vlessUuid"`
	SsPassword           string   `json:"ssPassword"`
	CreatedAt            string   `json:"createdAt"`
	SubscriptionURL      string   `json:"subscriptionUrl"`
	// Remnawave before 2.2 kept usage on the user itself.
	UsedTrafficBytes *flexInt `json:"usedTrafficBytes"`
	OnlineAt         *string  `json:"onlineAt"`
	UserTraffic      *struct {
		UsedTrafficBytes flexInt `json:"usedTrafficBytes"`
		OnlineAt         *string `json:"onlineAt"`
	} `json:"userTraffic"`
	ActiveInternalSquads []remnawaveRef `json:"activeInternalSquads"`
}

type remnawaveProfileInbound struct {
	UUID       string         `json:"uuid"`
	Tag        string         `json:"tag"`
	RawInbound map[string]any `json:"rawInbound"`
}

type remnawaveProfile struct {
	UUID     string                    `json:"uuid"`
	Name     string                    `json:"name"`
	Config   json.RawMessage           `json:"config"`
	Inbounds []remnawaveProfileInbound `json:"inbounds"`
}

type remnawaveSquad struct {
	UUID     string         `json:"uuid"`
	Name     string         `json:"name"`
	Inbounds []remnawaveRef `json:"inbounds"`
}

type remnawaveNode struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	IsDisabled    bool   `json:"isDisabled"`
	ViewPosition  int    `json:"viewPosition"`
	ConfigProfile struct {
		ActiveConfigProfileUUID string         `json:"activeConfigProfileUuid"`
		ActiveInbounds          []remnawaveRef `json:"activeInbounds"`
	} `json:"configProfile"`
}

type remnawaveHost struct {
	Remark                 string          `json:"remark"`
	Address                string          `json:"address"`
	Port                   int             `json:"port"`
	Path                   *string         `json:"path"`
	Sni                    *string         `json:"sni"`
	Host                   *string         `json:"host"`
	Alpn                   *string         `json:"alpn"`
	Fingerprint            *string         `json:"fingerprint"`
	IsDisabled             bool            `json:"isDisabled"`
	IsHidden               bool            `json:"isHidden"`
	SecurityLayer          string          `json:"securityLayer"`
	ViewPosition           int             `json:"viewPosition"`
	ServerDescription      *string         `json:"serverDescription"`
	Tags                   []string        `json:"tags"`
	OverrideSniFromAddress bool            `json:"overrideSniFromAddress"`
	KeepSniBlank           bool            `json:"keepSniBlank"`
	VlessRouteID           *int            `json:"vlessRouteId"`
	PinnedPeerCertSha256   *string         `json:"pinnedPeerCertSha256"`
	VerifyPeerCertByName   *string         `json:"verifyPeerCertByName"`
	ShuffleHost            bool            `json:"shuffleHost"`
	MihomoX25519           bool            `json:"mihomoX25519"`
	MihomoIPVersion        *string         `json:"mihomoIpVersion"`
	MuxParams              json.RawMessage `json:"muxParams"`
	SockoptParams          json.RawMessage `json:"sockoptParams"`
	FinalMask              json.RawMessage `json:"finalMask"`
	XhttpExtraParams       json.RawMessage `json:"xhttpExtraParams"`
	ExcludeFromSubTypes    []string        `json:"excludeFromSubscriptionTypes"`
	Nodes                  []string        `json:"nodes"`
	Inbound                struct {
		ConfigProfileInboundUUID string `json:"configProfileInboundUuid"`
	} `json:"inbound"`
	InternalSquads *struct {
		Mode   string   `json:"mode"`
		Squads []string `json:"squads"`
	} `json:"internalSquads"`
}

const (
	remnawavePageSize  = 500
	remnawaveBodyLimit = 256 << 20
)

// FetchRemnawaveSnapshot reads a live Remnawave panel through its REST API
// with an API token created under Remnawave Settings → API Tokens.
func FetchRemnawaveSnapshot(ctx context.Context, baseURL, token string) (*RemnawaveSnapshot, error) {
	base, err := normalizeRemnawaveBase(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("remnawave API token is required")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	get := func(path string, query url.Values, into any) error {
		u := base + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("Accept", "application/json")
		// Remnawave refuses plain-HTTP callers unless they look proxied.
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		resp, doErr := client.Do(req)
		if doErr != nil {
			return fmt.Errorf("GET %s: %w", path, doErr)
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, remnawaveBodyLimit))
		if readErr != nil {
			return fmt.Errorf("GET %s: %w", path, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, truncateForError(body))
		}
		var envelope struct {
			Response json.RawMessage `json:"response"`
		}
		if jErr := json.Unmarshal(body, &envelope); jErr != nil || len(envelope.Response) == 0 {
			return fmt.Errorf("GET %s: not a Remnawave API response: %s", path, truncateForError(body))
		}
		if jErr := json.Unmarshal(envelope.Response, into); jErr != nil {
			return fmt.Errorf("GET %s: %w", path, jErr)
		}
		return nil
	}

	snap := &RemnawaveSnapshot{}
	for start := 0; ; start += remnawavePageSize {
		var page struct {
			Users []remnawaveUser `json:"users"`
			Total int             `json:"total"`
		}
		q := url.Values{"start": {strconv.Itoa(start)}, "size": {strconv.Itoa(remnawavePageSize)}}
		if err := get("/api/users", q, &page); err != nil {
			return nil, err
		}
		snap.Users = append(snap.Users, page.Users...)
		if len(page.Users) == 0 || len(snap.Users) >= page.Total {
			break
		}
	}
	var profiles struct {
		ConfigProfiles []remnawaveProfile `json:"configProfiles"`
	}
	if err := get("/api/config-profiles", nil, &profiles); err != nil {
		return nil, err
	}
	snap.ConfigProfiles = profiles.ConfigProfiles
	var squads struct {
		InternalSquads []remnawaveSquad `json:"internalSquads"`
	}
	if err := get("/api/internal-squads", nil, &squads); err != nil {
		return nil, err
	}
	snap.InternalSquads = squads.InternalSquads
	if err := get("/api/hosts", nil, &snap.Hosts); err != nil {
		return nil, err
	}
	if err := get("/api/nodes", nil, &snap.Nodes); err != nil {
		return nil, err
	}
	return snap, nil
}

func normalizeRemnawaveBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid Remnawave URL %q: expected http(s)://host[:port]", raw)
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api")
	return u.Scheme + "://" + u.Host + path, nil
}

func truncateForError(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ParseRemnawaveSnapshot reads an uploaded snapshot file.
func ParseRemnawaveSnapshot(data []byte) (*RemnawaveSnapshot, error) {
	var snap RemnawaveSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("not a Remnawave snapshot: %w", err)
	}
	if len(snap.Users) == 0 && len(snap.Nodes) == 0 && len(snap.ConfigProfiles) == 0 {
		return nil, errors.New("not a Remnawave snapshot: no users, nodes or config profiles")
	}
	return &snap, nil
}

type remnawaveProfileInboundRef struct {
	tag     string
	profile string
	raw     map[string]any
}

// buildRemnawavePlan turns Remnawave's profile inbounds into one inbound per
// node that runs them, so each server keeps its ports and Reality keys.
func buildRemnawavePlan(snap *RemnawaveSnapshot) (*migrationPlan, error) {
	plan := &migrationPlan{source: MigrationSourceRemnawave}

	profileInbounds := map[string]remnawaveProfileInboundRef{}
	for _, p := range snap.ConfigProfiles {
		var cfg struct {
			Inbounds []map[string]any `json:"inbounds"`
		}
		if len(p.Config) > 0 {
			_ = json.Unmarshal(p.Config, &cfg)
		}
		byTag := map[string]map[string]any{}
		for _, in := range cfg.Inbounds {
			if tag, _ := in["tag"].(string); tag != "" {
				byTag[tag] = in
			}
		}
		for _, in := range p.Inbounds {
			raw := in.RawInbound
			if raw == nil {
				raw = byTag[in.Tag]
			}
			profileInbounds[in.UUID] = remnawaveProfileInboundRef{tag: in.Tag, profile: p.Name, raw: raw}
		}
	}

	nodes := append([]remnawaveNode{}, snap.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ViewPosition < nodes[j].ViewPosition })
	nodesPerInbound := map[string]int{}
	for _, n := range nodes {
		for _, ai := range n.ConfigProfile.ActiveInbounds {
			nodesPerInbound[ai.UUID]++
		}
	}

	nodeAddr := map[string]string{}
	keysByProfileInbound := map[string][]string{}
	protocolByKey := map[string]model.Protocol{}
	converted := map[string]*model.Inbound{}
	failed := map[string]bool{}
	for _, n := range nodes {
		nodeAddr[n.UUID] = n.Address
		plan.servers = append(plan.servers, MigrationSourceServer{Key: n.UUID, Name: n.Name, Address: n.Address})
		if n.IsDisabled {
			plan.warnf("node %q is disabled in Remnawave", n.Name)
		}
		for _, ai := range n.ConfigProfile.ActiveInbounds {
			ref, ok := profileInbounds[ai.UUID]
			if !ok || ref.raw == nil {
				plan.warnf("node %q: inbound %q is not in any config profile, skipped", n.Name, ai.Tag)
				continue
			}
			if failed[ai.UUID] {
				continue
			}
			base, ok := converted[ai.UUID]
			if !ok {
				ib, convErr := inboundFromXrayJSON(ref.raw)
				if convErr != nil {
					plan.warnf("inbound %q (profile %q): %v, skipped", ref.tag, ref.profile, convErr)
					failed[ai.UUID] = true
					continue
				}
				base = ib
				converted[ai.UUID] = ib
			}
			ib := *base
			if nodesPerInbound[ai.UUID] > 1 {
				ib.Remark = ref.tag + " · " + n.Name
				ib.Tag = ref.tag + "-" + tagSafe(n.Name)
			}
			key := n.UUID + "/" + ai.UUID
			plan.inbounds = append(plan.inbounds, migrationInbound{key: key, serverKey: n.UUID, inbound: ib})
			keysByProfileInbound[ai.UUID] = append(keysByProfileInbound[ai.UUID], key)
			protocolByKey[key] = ib.Protocol
		}
	}
	for uuid, ref := range profileInbounds {
		if nodesPerInbound[uuid] == 0 {
			plan.warnf("inbound %q (profile %q) runs on no node, skipped", ref.tag, ref.profile)
		}
	}
	if len(snap.ConfigProfiles) > 0 {
		plan.warnf("Xray routing, outbounds and DNS of config profiles are not imported; review the Xray template of this panel and its nodes")
	}

	withHost := map[string]bool{}
	hosts := append([]remnawaveHost{}, snap.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].ViewPosition < hosts[j].ViewPosition })
	for i, h := range hosts {
		candidates := keysByProfileInbound[h.Inbound.ConfigProfileInboundUUID]
		if len(candidates) == 0 {
			plan.warnf("host %q: its inbound runs on no imported node, skipped", h.Remark)
			continue
		}
		key := pickHostInbound(h, candidates, nodeAddr)
		plan.hosts = append(plan.hosts, migrationHost{inboundKey: key, group: remnawaveHostGroup(h, i, plan)})
		withHost[key] = true
	}
	// Remnawave lists only hosts in a subscription; an inbound with none stays out of it.
	for i := range plan.inbounds {
		plan.inbounds[i].inbound.ExcludeFromSub = !withHost[plan.inbounds[i].key]
	}

	squadInbounds := map[string][]string{}
	for _, sq := range snap.InternalSquads {
		for _, in := range sq.Inbounds {
			squadInbounds[sq.UUID] = append(squadInbounds[sq.UUID], in.UUID)
		}
	}

	conflicts := 0
	for _, u := range snap.Users {
		mc, conflict, convErr := remnawaveClient(u, squadInbounds, keysByProfileInbound, protocolByKey)
		if convErr != nil {
			plan.warnf("user %q: %v, skipped", u.Username, convErr)
			continue
		}
		if conflict {
			conflicts++
		}
		plan.clients = append(plan.clients, mc)
		if plan.sub.Path == "" {
			if info, ok := subInfoFromURL(u.SubscriptionURL, u.ShortUUID); ok {
				// Remnawave names every config by its host remark alone.
				info.RemarkTemplate = "{{HOST}}"
				plan.sub = info
			}
		}
	}
	if conflicts > 0 {
		plan.warnf("%d users have both Trojan and Shadowsocks access with different passwords; their Shadowsocks link changes and needs a subscription refresh", conflicts)
	}
	return plan, nil
}

// pickHostInbound attaches a host to exactly one node copy of its inbound, so
// the subscription lists it once: the node it names, else the one it points at.
func pickHostInbound(h remnawaveHost, candidates []string, nodeAddr map[string]string) string {
	nodeOf := func(key string) string { n, _, _ := strings.Cut(key, "/"); return n }
	for _, key := range candidates {
		for _, n := range h.Nodes {
			if nodeOf(key) == n {
				return key
			}
		}
	}
	for _, key := range candidates {
		if strings.EqualFold(nodeAddr[nodeOf(key)], h.Address) {
			return key
		}
	}
	return candidates[0]
}

var tagUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func tagSafe(s string) string {
	out := strings.Trim(tagUnsafe.ReplaceAllString(s, "-"), "-")
	if out == "" {
		return "node"
	}
	return out
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func rawJSONOrEmpty(r json.RawMessage) string {
	s := strings.TrimSpace(string(r))
	if s == "" || s == "null" || s == "{}" {
		return ""
	}
	return s
}

func remnawaveHostGroup(h remnawaveHost, order int, plan *migrationPlan) entity.HostGroup {
	g := entity.HostGroup{
		Hosts:                  []string{formatHostAddr(h.Address, h.Port)},
		SortOrder:              order,
		Remark:                 h.Remark,
		ServerDescription:      strOrEmpty(h.ServerDescription),
		IsDisabled:             h.IsDisabled,
		IsHidden:               h.IsHidden,
		Tags:                   h.Tags,
		Port:                   h.Port,
		Security:               "same",
		Sni:                    strOrEmpty(h.Sni),
		HostHeader:             strOrEmpty(h.Host),
		Path:                   strOrEmpty(h.Path),
		Fingerprint:            strOrEmpty(h.Fingerprint),
		OverrideSniFromAddress: h.OverrideSniFromAddress,
		KeepSniBlank:           h.KeepSniBlank,
		VerifyPeerCertByName:   strOrEmpty(h.VerifyPeerCertByName),
		MuxParams:              rawJSONOrEmpty(h.MuxParams),
		SockoptParams:          rawJSONOrEmpty(h.SockoptParams),
		FinalMask:              rawJSONOrEmpty(h.FinalMask),
		MihomoIpVersion:        strOrEmpty(h.MihomoIPVersion),
		MihomoX25519:           h.MihomoX25519,
		ShuffleHost:            h.ShuffleHost,
		RandomCount:            1,
	}
	switch strings.ToUpper(h.SecurityLayer) {
	case "TLS":
		g.Security = "tls"
	case "NONE":
		g.Security = "none"
	}
	if alpn := strOrEmpty(h.Alpn); alpn != "" {
		g.Alpn = strings.Split(alpn, ",")
	}
	if pin := strOrEmpty(h.PinnedPeerCertSha256); pin != "" {
		g.PinnedPeerCertSha256 = strings.Split(pin, ",")
	}
	if h.VlessRouteID != nil {
		g.VlessRoute = strconv.Itoa(*h.VlessRouteID)
	}
	seen := map[string]bool{}
	for _, t := range h.ExcludeFromSubTypes {
		format := map[string]string{"XRAY_BASE64": "raw", "XRAY_JSON": "json", "MIHOMO": "clash", "CLASH": "clash", "STASH": "clash"}[strings.ToUpper(t)]
		if format != "" && !seen[format] {
			seen[format] = true
			g.ExcludeFromSubTypes = append(g.ExcludeFromSubTypes, format)
		}
	}
	if rawJSONOrEmpty(h.XhttpExtraParams) != "" {
		plan.warnf("host %q: xhttp extra params are not imported", h.Remark)
	}
	if h.InternalSquads != nil && len(h.InternalSquads.Squads) > 0 {
		plan.warnf("host %q: its internal-squad filter is not imported, every user of the inbound sees it", h.Remark)
	}
	return g
}

func remnawaveClient(u remnawaveUser, squadInbounds, keysByProfileInbound map[string][]string, protocolByKey map[string]model.Protocol) (migrationClient, bool, error) {
	if strings.TrimSpace(u.Username) == "" {
		return migrationClient{}, false, errors.New("empty username")
	}
	c := model.Client{
		Email:   strings.TrimSpace(u.Username),
		ID:      u.VlessUUID,
		Flow:    "xtls-rprx-vision",
		SubID:   u.ShortUUID,
		TotalGB: int64(u.TrafficLimitBytes),
		Enable:  !strings.EqualFold(u.Status, "DISABLED"),
		Comment: strOrEmpty(u.Description),
		Group:   strOrEmpty(u.Tag),
	}
	if u.TelegramID != nil {
		c.TgID = int64(*u.TelegramID)
	}
	if u.ExpireAt != "" {
		t, err := time.Parse(time.RFC3339Nano, u.ExpireAt)
		if err != nil {
			return migrationClient{}, false, fmt.Errorf("bad expireAt %q", u.ExpireAt)
		}
		c.ExpiryTime = t.UnixMilli()
	}
	switch strings.ToUpper(u.TrafficLimitStrategy) {
	case "DAY":
		c.TrafficReset = "daily"
	case "WEEK":
		c.TrafficReset = "weekly"
	case "MONTH":
		c.TrafficReset, c.TrafficResetDay = "monthly", 1
	case "MONTH_ROLLING":
		c.TrafficReset, c.TrafficResetDay = "monthly", 1
		if t, err := time.Parse(time.RFC3339Nano, u.CreatedAt); err == nil {
			c.TrafficResetDay = t.Day()
		}
	}

	var keys []string
	seen := map[string]bool{}
	for _, sq := range u.ActiveInternalSquads {
		for _, in := range squadInbounds[sq.UUID] {
			for _, k := range keysByProfileInbound[in] {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
	}
	hasTrojan, hasSS := false, false
	for _, k := range keys {
		switch protocolByKey[k] {
		case model.Trojan:
			hasTrojan = true
		case model.Shadowsocks:
			hasSS = true
		}
	}
	// One panel client carries one password for both protocols; Trojan wins.
	switch {
	case hasTrojan:
		c.Password = u.TrojanPassword
	case hasSS:
		c.Password = u.SsPassword
	}
	conflict := hasTrojan && hasSS && u.TrojanPassword != u.SsPassword

	mc := migrationClient{client: c, inboundKeys: keys}
	if u.HwidDeviceLimit != nil {
		mc.limitHwid = int(*u.HwidDeviceLimit)
	}
	used, online := int64(0), ""
	if u.UserTraffic != nil {
		used, online = int64(u.UserTraffic.UsedTrafficBytes), strOrEmpty(u.UserTraffic.OnlineAt)
	} else if u.UsedTrafficBytes != nil {
		used, online = int64(*u.UsedTrafficBytes), strOrEmpty(u.OnlineAt)
	}
	if used > 0 || online != "" {
		mc.traffic = &ClientPortableTraffic{Down: used}
		if t, err := time.Parse(time.RFC3339Nano, online); err == nil {
			mc.traffic.LastOnline = t.UnixMilli()
		}
	}
	return mc, conflict, nil
}

// inboundFromXrayJSON converts a plain Xray server inbound (as Remnawave keeps
// it in a config profile) into a panel inbound without clients.
func inboundFromXrayJSON(raw map[string]any) (*model.Inbound, error) {
	protocol, _ := raw["protocol"].(string)
	switch model.Protocol(protocol) {
	case model.VLESS, model.Trojan, model.Shadowsocks:
	default:
		return nil, fmt.Errorf("protocol %q is not supported by the importer", protocol)
	}
	port, err := xrayPort(raw["port"])
	if err != nil {
		return nil, err
	}
	settings, _ := raw["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	settings = cloneJSONMap(settings)
	settings["clients"] = []any{}
	switch model.Protocol(protocol) {
	case model.VLESS:
		if _, ok := settings["decryption"]; !ok {
			settings["decryption"] = "none"
		}
	case model.Shadowsocks:
		if m, _ := settings["method"].(string); m == "" {
			settings["method"] = "chacha20-ietf-poly1305"
		}
		if _, ok := settings["network"]; !ok {
			settings["network"] = "tcp,udp"
		}
	}

	stream, _ := raw["streamSettings"].(map[string]any)
	if stream == nil {
		stream = map[string]any{"network": "tcp", "security": "none"}
	}
	stream = cloneJSONMap(stream)
	if n, _ := stream["network"].(string); n == "" || n == "raw" {
		stream["network"] = "tcp"
		if rs, ok := stream["rawSettings"]; ok {
			stream["tcpSettings"] = rs
			delete(stream, "rawSettings")
		}
	}
	if sec, _ := stream["security"].(string); sec == "reality" {
		if err := fillRealityPublicKey(stream); err != nil {
			return nil, err
		}
	}

	sniffing, _ := raw["sniffing"].(map[string]any)
	if sniffing == nil {
		sniffing = map[string]any{"enabled": false, "destOverride": []any{}}
	}
	tag, _ := raw["tag"].(string)
	listen, _ := raw["listen"].(string)

	enc := func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
	return &model.Inbound{
		Remark:         tag,
		Enable:         true,
		Listen:         listen,
		Port:           port,
		Protocol:       model.Protocol(protocol),
		Settings:       enc(settings),
		StreamSettings: enc(stream),
		Sniffing:       enc(sniffing),
		Tag:            tag,
		TrafficReset:   "never",
	}, nil
}

func xrayPort(v any) (int, error) {
	var port int
	switch p := v.(type) {
	case float64:
		port = int(p)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0, fmt.Errorf("port %q is not a single port", p)
		}
		port = n
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %v", v)
	}
	return port, nil
}

// fillRealityPublicKey stores the public key clients need next to the private
// one, which is where share links read it from.
func fillRealityPublicKey(stream map[string]any) error {
	rs, _ := stream["realitySettings"].(map[string]any)
	if rs == nil {
		return errors.New("reality inbound without realitySettings")
	}
	priv, _ := rs["privateKey"].(string)
	pub, err := x25519PublicKey(priv)
	if err != nil {
		return fmt.Errorf("reality privateKey: %w", err)
	}
	client, _ := rs["settings"].(map[string]any)
	if client == nil {
		client = map[string]any{}
	}
	client["publicKey"] = pub
	if fp, _ := client["fingerprint"].(string); fp == "" {
		client["fingerprint"] = "chrome"
	}
	if _, ok := client["spiderX"]; !ok {
		client["spiderX"] = "/"
	}
	if _, ok := client["serverName"]; !ok {
		client["serverName"] = ""
	}
	rs["settings"] = client
	if seed, _ := rs["mldsa65Seed"].(string); seed != "" {
		if _, ok := client["mldsa65Verify"]; !ok {
			return errors.New("reality mldsa65Seed without mldsa65Verify cannot be imported")
		}
	}
	return nil
}

func x25519PublicKey(privateKey string) (string, error) {
	privateKey = strings.TrimSpace(privateKey)
	var key []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if key, err = enc.DecodeString(privateKey); err == nil && len(key) == curve25519.ScalarSize {
			break
		}
	}
	if err != nil || len(key) != curve25519.ScalarSize {
		return "", errors.New("not a base64 X25519 key")
	}
	pub, err := curve25519.X25519(key, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub), nil
}

func cloneJSONMap(m map[string]any) map[string]any {
	b, err := json.Marshal(m)
	if err != nil {
		return m
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return m
	}
	return out
}

// PreviewRemnawave plans an import and reports what it would create.
func (s *MigrationService) PreviewRemnawave(snap *RemnawaveSnapshot, targets map[string]string) (*MigrationPreview, error) {
	plan, err := buildRemnawavePlan(snap)
	if err != nil {
		return nil, err
	}
	return s.preview(plan, targets)
}

// ApplyRemnawave runs the import planned from snap.
func (s *MigrationService) ApplyRemnawave(snap *RemnawaveSnapshot, targets map[string]string, applySub bool, userID int) (*MigrationResult, error) {
	plan, err := buildRemnawavePlan(snap)
	if err != nil {
		return nil, err
	}
	return s.apply(plan, targets, applySub, userID)
}
