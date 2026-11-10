package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const xuiLocalServer = "local"

type xuiInboundRow struct {
	ID             int
	Up             int64
	Down           int64
	Total          int64
	Remark         string
	Enable         bool
	ExpiryTime     int64
	Listen         string
	Port           int
	Protocol       string
	Settings       string
	StreamSettings string
	Tag            string
	Sniffing       string
	TrafficReset   sql.NullString
	NodeID         sql.NullInt64
}

// readXUIPlan reads a 3x-ui x-ui.db (any release that keeps clients in the
// inbound settings) without touching it, and plans a merge into this panel.
func readXUIPlan(path string) (*migrationPlan, error) {
	if err := database.ValidateSQLiteDB(path); err != nil {
		return nil, fmt.Errorf("not a readable SQLite database: %w", err)
	}
	gdb, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()
	if !gdb.Migrator().HasTable("inbounds") {
		return nil, errors.New("not a 3x-ui database: no inbounds table")
	}

	cols := "id, up, down, total, remark, enable, expiry_time, listen, port, protocol, settings, stream_settings, tag, sniffing"
	for _, opt := range []string{"traffic_reset", "node_id"} {
		if gdb.Migrator().HasColumn("inbounds", opt) {
			cols += ", " + opt
		} else {
			cols += ", NULL AS " + opt
		}
	}
	var rows []xuiInboundRow
	if err := gdb.Raw("SELECT " + cols + " FROM inbounds ORDER BY id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read inbounds: %w", err)
	}

	settings := map[string]string{}
	if gdb.Migrator().HasTable("settings") {
		var kv []struct{ Key, Value string }
		if err := gdb.Raw("SELECT key, value FROM settings").Scan(&kv).Error; err != nil {
			return nil, fmt.Errorf("read settings: %w", err)
		}
		for _, r := range kv {
			settings[r.Key] = r.Value
		}
	}

	plan := &migrationPlan{source: MigrationSourceXUI}
	localAddr := settings["subDomain"]
	if localAddr == "" {
		localAddr = settings["webDomain"]
	}
	servers := map[string]*MigrationSourceServer{}
	serverOrder := []string{}
	addServer := func(key, name, addr string) {
		if _, ok := servers[key]; ok {
			return
		}
		servers[key] = &MigrationSourceServer{Key: key, Name: name, Address: addr}
		serverOrder = append(serverOrder, key)
	}
	nodeInfo := map[int64][2]string{}
	if gdb.Migrator().HasTable("nodes") {
		var nodes []struct {
			ID      int64
			Name    string
			Address string
		}
		if err := gdb.Raw("SELECT id, name, address FROM nodes").Scan(&nodes).Error; err == nil {
			for _, n := range nodes {
				nodeInfo[n.ID] = [2]string{n.Name, n.Address}
			}
		}
	}

	traffic, err := readXUITraffic(gdb)
	if err != nil {
		return nil, err
	}

	merger := newXUIClientMerger(plan)
	for _, r := range rows {
		serverKey := xuiLocalServer
		if r.NodeID.Valid {
			serverKey = "node-" + strconv.FormatInt(r.NodeID.Int64, 10)
			info := nodeInfo[r.NodeID.Int64]
			name := info[0]
			if name == "" {
				name = serverKey
			}
			addServer(serverKey, name, info[1])
		} else {
			addServer(serverKey, "3x-ui", localAddr)
		}
		key := "inbound-" + strconv.Itoa(r.ID)
		inbound, clients, convErr := xuiInbound(r)
		if convErr != nil {
			plan.warnf("inbound %q (port %d): %v, skipped", r.Remark, r.Port, convErr)
			continue
		}
		plan.inbounds = append(plan.inbounds, migrationInbound{key: key, serverKey: serverKey, inbound: inbound})
		for _, c := range clients {
			merger.add(c, key, model.Protocol(r.Protocol))
		}
	}
	for _, k := range serverOrder {
		plan.servers = append(plan.servers, *servers[k])
	}
	plan.clients = merger.result(traffic)
	plan.sub = xuiSubInfo(settings, plan.clients)
	return plan, nil
}

func readXUITraffic(gdb *gorm.DB) (map[string]ClientPortableTraffic, error) {
	out := map[string]ClientPortableTraffic{}
	if !gdb.Migrator().HasTable("client_traffics") {
		return out, nil
	}
	cols := "email, up, down"
	if gdb.Migrator().HasColumn("client_traffics", "last_online") {
		cols += ", last_online"
	} else {
		cols += ", 0 AS last_online"
	}
	var rows []struct {
		Email      string
		Up         int64
		Down       int64
		LastOnline int64
	}
	if err := gdb.Raw("SELECT " + cols + " FROM client_traffics").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read client_traffics: %w", err)
	}
	for _, r := range rows {
		k := strings.ToLower(strings.TrimSpace(r.Email))
		t := out[k]
		t.Up += r.Up
		t.Down += r.Down
		t.LastOnline = max(t.LastOnline, r.LastOnline)
		out[k] = t
	}
	return out, nil
}

func xuiInbound(r xuiInboundRow) (model.Inbound, []model.Client, error) {
	var settings map[string]any
	if strings.TrimSpace(r.Settings) != "" {
		if err := json.Unmarshal([]byte(r.Settings), &settings); err != nil {
			return model.Inbound{}, nil, fmt.Errorf("invalid settings JSON: %w", err)
		}
	}
	if settings == nil {
		settings = map[string]any{}
	}
	var clients []model.Client
	if list, ok := settings["clients"].([]any); ok {
		for _, raw := range list {
			obj, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			model.NormalizeLegacyClientFields(obj)
			if obj["enable"] == nil {
				obj["enable"] = true
			}
			blob, _ := json.Marshal(obj)
			var c model.Client
			if err := json.Unmarshal(blob, &c); err != nil || strings.TrimSpace(c.Email) == "" {
				continue
			}
			c.Email = strings.TrimSpace(c.Email)
			clients = append(clients, c)
		}
		settings["clients"] = []any{}
	}
	blob, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return model.Inbound{}, nil, err
	}
	ib := model.Inbound{
		Up:             r.Up,
		Down:           r.Down,
		Total:          r.Total,
		Remark:         r.Remark,
		Enable:         r.Enable,
		ExpiryTime:     r.ExpiryTime,
		Listen:         r.Listen,
		Port:           r.Port,
		Protocol:       model.Protocol(r.Protocol),
		Settings:       string(blob),
		StreamSettings: r.StreamSettings,
		Sniffing:       r.Sniffing,
		Tag:            r.Tag,
		TrafficReset:   "never",
	}
	if r.TrafficReset.Valid && r.TrafficReset.String != "" {
		ib.TrafficReset = r.TrafficReset.String
	}
	return ib, clients, nil
}

// xuiClientMerger folds the per-inbound copies of a 3x-ui client into one
// panel client: same email, or same subscription id, with matching keys.
type xuiClientMerger struct {
	plan    *migrationPlan
	clients []*migrationClient
	byEmail map[string]*migrationClient
	bySub   map[string]*migrationClient
}

func newXUIClientMerger(plan *migrationPlan) *xuiClientMerger {
	return &xuiClientMerger{
		plan:    plan,
		byEmail: map[string]*migrationClient{},
		bySub:   map[string]*migrationClient{},
	}
}

func (m *xuiClientMerger) add(c model.Client, inboundKey string, protocol model.Protocol) {
	email := strings.ToLower(c.Email)
	target := m.byEmail[email]
	if target == nil && c.SubID != "" {
		if same := m.bySub[c.SubID]; same != nil && credentialsCompatible(same.client, c, protocol) {
			target = same
			m.byEmail[email] = same
		}
	}
	if target == nil {
		mc := &migrationClient{client: c}
		m.clients = append(m.clients, mc)
		m.byEmail[email] = mc
		if c.SubID != "" {
			if _, taken := m.bySub[c.SubID]; taken {
				m.plan.warnf("clients %q and %q share subscription %q with different keys; only the first keeps that subscription", m.bySub[c.SubID].client.Email, c.Email, c.SubID)
			} else {
				m.bySub[c.SubID] = mc
			}
		}
		target = mc
	} else {
		if !credentialsCompatible(target.client, c, protocol) {
			m.plan.warnf("client %q has different keys in different inbounds; the first one is kept", c.Email)
		}
		mergeClientCredentials(&target.client, c)
	}
	if !slices.Contains(target.inboundKeys, inboundKey) {
		target.inboundKeys = append(target.inboundKeys, inboundKey)
	}
}

func (m *xuiClientMerger) result(traffic map[string]ClientPortableTraffic) []migrationClient {
	emailsOf := map[*migrationClient][]string{}
	for email, mc := range m.byEmail {
		emailsOf[mc] = append(emailsOf[mc], email)
	}
	out := make([]migrationClient, 0, len(m.clients))
	for _, mc := range m.clients {
		var sum ClientPortableTraffic
		found := false
		for _, e := range emailsOf[mc] {
			if t, ok := traffic[e]; ok {
				found = true
				sum.Up += t.Up
				sum.Down += t.Down
				sum.LastOnline = max(sum.LastOnline, t.LastOnline)
			}
		}
		if found {
			mc.traffic = &sum
		}
		out = append(out, *mc)
	}
	return out
}

// credentialsCompatible reports whether b can join a's client without
// changing a key either of them already hands out for protocol.
func credentialsCompatible(a, b model.Client, protocol model.Protocol) bool {
	differ := func(x, y string) bool { return x != "" && y != "" && x != y }
	switch protocol {
	case model.VMESS, model.VLESS:
		return !differ(a.ID, b.ID)
	case model.Trojan, model.Shadowsocks:
		return !differ(a.Password, b.Password)
	case model.Hysteria:
		return !differ(a.Auth, b.Auth)
	}
	return true
}

func mergeClientCredentials(dst *model.Client, src model.Client) {
	if dst.ID == "" {
		dst.ID = src.ID
	}
	if dst.Password == "" {
		dst.Password = src.Password
	}
	if dst.Auth == "" {
		dst.Auth = src.Auth
	}
	if dst.Flow == "" {
		dst.Flow = src.Flow
	}
	if dst.Security == "" {
		dst.Security = src.Security
	}
	if dst.TgID == 0 {
		dst.TgID = src.TgID
	}
}

func xuiSubInfo(settings map[string]string, clients []migrationClient) MigrationSubInfo {
	info := MigrationSubInfo{
		Domain:    settings["subDomain"],
		Path:      settings["subPath"],
		JsonPath:  settings["subJsonPath"],
		ClashPath: settings["subClashPath"],
		URI:       settings["subURI"],
		Scheme:    "http",
	}
	if info.Path == "" {
		info.Path = "/sub/"
	}
	if settings["subCertFile"] != "" {
		info.Scheme = "https"
	}
	info.Port, _ = strconv.Atoi(settings["subPort"])
	if info.Port == 0 {
		info.Port = 2096
	}
	example := ""
	for _, c := range clients {
		if c.client.SubID != "" {
			example = c.client.SubID
			break
		}
	}
	if example == "" {
		return info
	}
	switch {
	case info.URI != "":
		info.ExampleURL = strings.TrimRight(info.URI, "/") + "/" + example
	case info.Domain != "":
		info.ExampleURL = fmt.Sprintf("%s://%s:%d%s%s", info.Scheme, info.Domain, info.Port, info.Path, example)
	}
	return info
}

// PreviewXUI plans a merge of the 3x-ui database at path.
func (s *MigrationService) PreviewXUI(path string, targets map[string]string) (*MigrationPreview, error) {
	plan, err := readXUIPlan(path)
	if err != nil {
		return nil, err
	}
	s.defaultXUITargets(plan)
	return s.preview(plan, targets)
}

// ApplyXUI merges the 3x-ui database at path into this panel.
func (s *MigrationService) ApplyXUI(path string, targets map[string]string, applySub bool, userID int) (*MigrationResult, error) {
	plan, err := readXUIPlan(path)
	if err != nil {
		return nil, err
	}
	s.defaultXUITargets(plan)
	return s.apply(plan, targets, applySub, userID)
}

// defaultXUITargets keeps the source panel's own inbounds here unless a node
// sits at its address.
func (s *MigrationService) defaultXUITargets(plan *migrationPlan) {
	if err := s.suggestTargets(plan); err != nil {
		return
	}
	for i := range plan.servers {
		if plan.servers[i].Key == xuiLocalServer && plan.servers[i].Suggested == "" {
			plan.servers[i].Suggested = MigrationTargetLocal
		}
	}
}
