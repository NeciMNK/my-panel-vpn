package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/shaper"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// SpeedService keeps users' and groups' speed contracts: a cap and a guarantee
// enforced with tc on the server a user connects to.
type SpeedService struct{}

// ClientSpeedRequest sets one user's limits; both 0 removes them.
type ClientSpeedRequest struct {
	Email   string `json:"email" example:"anna"`
	MinMbps int    `json:"minMbps" example:"20"`
	MaxMbps int    `json:"maxMbps" example:"100"`
}

// ServerCapacityRequest sets one server's link speed; 0 removes it.
type ServerCapacityRequest struct {
	NodeId       int `json:"nodeId" example:"0"`
	CapacityMbps int `json:"capacityMbps" example:"1000"`
}

// ServerFreeSpeed is the live load of a server with a known capacity;
// ReservedMbps is what the guarantees of the users it carries add up to.
type ServerFreeSpeed struct {
	NodeId          int     `json:"nodeId" example:"0"`
	CapacityMbps    int     `json:"capacityMbps" example:"1000"`
	UsedMbps        float64 `json:"usedMbps" example:"420.5"`
	Online          int     `json:"online" example:"12"`
	FreePerUserMbps float64 `json:"freePerUserMbps" example:"44.5"`
	ReservedMbps    int     `json:"reservedMbps" example:"300"`
}

// SpeedOverview is everything the speed card shows.
type SpeedOverview struct {
	Clients      []model.ClientSpeed    `json:"clients"`
	Groups       []model.GroupSpeed     `json:"groups"`
	Servers      []model.ServerCapacity `json:"servers"`
	Load         []ServerFreeSpeed      `json:"load"`
	Testing      []int                  `json:"testing" example:"[0]"`
	TestStats    []SpeedTestStats       `json:"testStats"`
	TestSettings SpeedTestSettings      `json:"testSettings"`
}

// clientIPWindow is how recently an IP must have been seen to be capped.
const clientIPWindow = 30 * time.Minute

var localThroughputBits atomic.Uint64

// SetLocalThroughputMbps records this panel's current busiest direction.
func SetLocalThroughputMbps(mbps float64) { localThroughputBits.Store(math.Float64bits(mbps)) }

func localThroughputMbps() float64 { return math.Float64frombits(localThroughputBits.Load()) }

func (s *SpeedService) SetClient(req *ClientSpeedRequest) error {
	if req == nil || strings.TrimSpace(req.Email) == "" {
		return common.NewError("pick a user")
	}
	if req.MinMbps < 0 || req.MaxMbps < 0 {
		return common.NewError("speeds cannot be negative")
	}
	if req.MaxMbps > 0 && req.MinMbps > req.MaxMbps {
		return common.NewError("the minimum speed cannot be above the maximum")
	}
	email := strings.TrimSpace(req.Email)
	db := database.GetDB()
	if req.MinMbps == 0 && req.MaxMbps == 0 {
		return db.Where("email = ?", email).Delete(&model.ClientSpeed{}).Error
	}
	row := &model.ClientSpeed{}
	db.Where("email = ?", email).First(row)
	row.Email, row.MinMbps, row.MaxMbps = email, req.MinMbps, req.MaxMbps
	return db.Save(row).Error
}

func (s *SpeedService) SetCapacity(req *ServerCapacityRequest) error {
	if req == nil || req.CapacityMbps < 0 {
		return common.NewError("capacity cannot be negative")
	}
	db := database.GetDB()
	if req.CapacityMbps == 0 {
		return db.Where("node_id = ?", req.NodeId).Delete(&model.ServerCapacity{}).Error
	}
	row := &model.ServerCapacity{}
	db.Where("node_id = ?", req.NodeId).First(row)
	row.NodeId, row.CapacityMbps = req.NodeId, req.CapacityMbps
	return db.Save(row).Error
}

// serverLoad is one server's capacity, use, user count and the sum of its
// users' guarantees.
type serverLoad struct {
	Capacity float64
	Used     float64
	Online   int
	Reserved float64
}

// roomFor reports whether the server can still guarantee need Mbit/s to one more user.
func (l serverLoad) roomFor(need int) bool { return l.Capacity-l.Reserved >= float64(need) }

// freeForNewUser is what one more user would get if the free speed were shared.
func (l serverLoad) freeForNewUser() float64 { return (l.Capacity - l.Used) / float64(l.Online+1) }

func (l serverLoad) freePerUser() float64 {
	return (l.Capacity - l.Used) / float64(max(l.Online, 1))
}

// speedWorld is the shared view of nodes, online users, their speed rules and
// the cascade exits carrying them that one pass works from.
type speedWorld struct {
	nodes   []*model.Node
	guidOf  map[int]string
	byGuid  map[string][]string
	rules   map[string]speedRule
	viaExit map[int][]string
}

func loadSpeedWorld() (*speedWorld, error) {
	var nodes []*model.Node
	if err := database.GetDB().Find(&nodes).Error; err != nil {
		return nil, err
	}
	selfGuid, _ := (&SettingService{}).GetPanelGuid()
	ambiguous := ambiguousNodeGuids(nodes, selfGuid)
	w := &speedWorld{nodes: nodes, guidOf: map[int]string{0: selfGuid}, byGuid: (&InboundService{}).GetOnlineClientsByGuid()}
	for _, n := range nodes {
		w.guidOf[n.Id] = effectiveNodeGuid(n, ambiguous)
	}
	return w, w.loadRoutes()
}

// loadRoutes reads every user's speed rule and which cascade exit carries
// each online user.
func (w *speedWorld) loadRoutes() error {
	rules, err := speedRules()
	if err != nil {
		return err
	}
	w.rules = rules
	db := database.GetDB()
	var cascades []model.Cascade
	if err := db.Where("enable = ?", true).Find(&cascades).Error; err != nil {
		return err
	}
	online := map[int][]string{}
	var emails []string
	for _, c := range cascades {
		if _, done := online[c.EntryNodeId]; !done {
			online[c.EntryNodeId] = w.online(c.EntryNodeId)
			emails = append(emails, online[c.EntryNodeId]...)
		}
	}
	if len(emails) == 0 {
		w.viaExit = map[int][]string{}
		return nil
	}
	var attached []struct {
		Email   string
		Tag     string
		NodeID  *int
		Inbound int
	}
	if err := db.Table("clients").
		Select("clients.email AS email, inbounds.tag AS tag, inbounds.node_id AS node_id, inbounds.id AS inbound").
		Joins("JOIN client_inbounds ON client_inbounds.client_id = clients.id").
		Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
		Where("clients.email IN ?", emails).Scan(&attached).Error; err != nil {
		return err
	}
	entered := map[int]map[string]bool{}
	for _, c := range cascades {
		entered[c.Id] = map[string]bool{}
		for _, a := range attached {
			if cascadeEnters(&c, &model.Inbound{Id: a.Inbound, Tag: a.Tag, NodeID: a.NodeID}) {
				entered[c.Id][a.Email] = true
			}
		}
	}
	var pinRows []model.CascadePin
	if err := db.Find(&pinRows).Error; err != nil {
		return err
	}
	pins := map[int]map[string]int{}
	for _, p := range pinRows {
		if pins[p.CascadeId] == nil {
			pins[p.CascadeId] = map[string]int{}
		}
		pins[p.CascadeId][p.Email] = p.NodeId
	}
	w.viaExit = cascadeExitUsers(cascades, online, entered, pins)
	return nil
}

// users is everyone a server carries: users connected to it and cascade users
// leaving through it.
func (w *speedWorld) users(nodeID int) []string {
	out := w.online(nodeID)
	for _, e := range w.viaExit[nodeID] {
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// allUsers is users for every server with someone on it.
func (w *speedWorld) allUsers() map[int][]string {
	out := map[int][]string{0: w.users(0)}
	for _, n := range w.nodes {
		out[n.Id] = w.users(n.Id)
	}
	for id := range w.viaExit {
		out[id] = w.users(id)
	}
	return out
}

func (w *speedWorld) online(nodeID int) []string {
	var out []string
	for _, e := range w.byGuid[w.guidOf[nodeID]] {
		if !strings.HasPrefix(e, managedTagPrefix) {
			out = append(out, e)
		}
	}
	return out
}

// used is every server's current busiest direction in Mbit/s.
func (w *speedWorld) used() map[int]float64 {
	out := map[int]float64{0: localThroughputMbps()}
	for _, n := range w.nodes {
		out[n.Id] = float64(max(n.NetUp, n.NetDown)) * 8 / 1e6
	}
	return out
}

// loads covers the servers with a known capacity.
func (w *speedWorld) loads() (map[int]serverLoad, error) {
	var caps []model.ServerCapacity
	if err := database.GetDB().Find(&caps).Error; err != nil {
		return nil, err
	}
	used := w.used()
	out := map[int]serverLoad{}
	for _, c := range caps {
		users := w.users(c.NodeId)
		reserved := reservedMbps(w.rules, map[int][]string{c.NodeId: users})[c.NodeId]
		out[c.NodeId] = serverLoad{Capacity: float64(c.CapacityMbps), Used: used[c.NodeId], Online: len(users), Reserved: float64(reserved)}
	}
	return out, nil
}

// guarantees is every user's guaranteed speed, from their own setting or their group's.
func guarantees(rules map[string]speedRule) map[string]int {
	out := map[string]int{}
	for e, r := range rules {
		if r.Min > 0 {
			out[e] = r.Min
		}
	}
	return out
}

func (s *SpeedService) Overview() (*SpeedOverview, error) {
	out := &SpeedOverview{Clients: []model.ClientSpeed{}, Servers: []model.ServerCapacity{}, Load: []ServerFreeSpeed{}, Testing: speedTestsRunning()}
	db := database.GetDB()
	if err := db.Order("email asc").Find(&out.Clients).Error; err != nil {
		return nil, err
	}
	if err := db.Order("group_name asc").Find(&out.Groups).Error; err != nil {
		return nil, err
	}
	if err := db.Order("node_id asc").Find(&out.Servers).Error; err != nil {
		return nil, err
	}
	w, err := loadSpeedWorld()
	if err != nil {
		return nil, err
	}
	loads, err := w.loads()
	if err != nil {
		return nil, err
	}
	for _, c := range out.Servers {
		l := loads[c.NodeId]
		out.Load = append(out.Load, ServerFreeSpeed{
			NodeId: c.NodeId, CapacityMbps: c.CapacityMbps, UsedMbps: math.Round(l.Used*10) / 10,
			Online: l.Online, FreePerUserMbps: math.Round(l.freePerUser()*10) / 10, ReservedMbps: int(l.Reserved),
		})
	}
	if out.TestStats, err = speedTestStats(time.Now()); err != nil {
		return nil, err
	}
	if out.TestSettings, err = s.TestSettings(); err != nil {
		return nil, err
	}
	return out, nil
}

// SpeedShortage is a server whose link no longer covers its users' guarantees
// while no other server could take them either.
type SpeedShortage struct {
	NodeId          int
	FreePerUserMbps float64
	MinMbps         int
	Users           []string
}

func speedShortages(loads map[int]serverLoad, users map[int][]string, mins map[string]int) []SpeedShortage {
	var out []SpeedShortage
	ids := make([]int, 0, len(loads))
	for id := range loads {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		l := loads[id]
		if l.Reserved <= l.Capacity {
			continue
		}
		short := SpeedShortage{NodeId: id, FreePerUserMbps: math.Round(l.freePerUser()*10) / 10}
		for _, email := range users[id] {
			need := mins[email]
			if need == 0 {
				continue
			}
			roomElsewhere := false
			for other, o := range loads {
				if other != id && o.roomFor(need) {
					roomElsewhere = true
				}
			}
			if roomElsewhere {
				continue
			}
			short.Users = append(short.Users, email)
			short.MinMbps = max(short.MinMbps, need)
		}
		if len(short.Users) > 0 {
			sort.Strings(short.Users)
			out = append(out, short)
		}
	}
	return out
}

// Shortages lists the servers the admin must hear about.
func (s *SpeedService) Shortages() ([]SpeedShortage, error) {
	w, err := loadSpeedWorld()
	if err != nil {
		return nil, err
	}
	loads, err := w.loads()
	if err != nil {
		return nil, err
	}
	return speedShortages(loads, w.allUsers(), guarantees(w.rules)), nil
}

// shapingPlans limits every recently seen IP of a limited user on the server
// the IP was seen on, keyed by server.
func shapingPlans(limits map[string]speedLimit, ips []model.NodeClientIp, serverOf map[string]int, now int64) map[int]shaper.Plan {
	out := map[int]shaper.Plan{}
	for _, row := range ips {
		limit, ok := limits[row.Email]
		server, known := serverOf[row.NodeGuid]
		if !ok || !known {
			continue
		}
		var entries []model.ClientIpEntry
		if err := json.Unmarshal([]byte(row.Ips), &entries); err != nil {
			continue
		}
		plan := out[server]
		for _, e := range entries {
			if now-e.Timestamp <= int64(clientIPWindow/time.Second) {
				plan.Limits = append(plan.Limits, shaper.Limit{IP: e.IP, Mbps: limit.Max, MinMbps: limit.Min})
			}
		}
		out[server] = plan
	}
	return out
}

var (
	speedPushMu   sync.Mutex
	speedPushedAt = map[int]time.Time{}
	speedPushed   = map[int]string{}
)

// speedRepush re-sends an unchanged plan this often, so a node that rebooted
// (and lost its tc rules) gets them back.
const speedRepush = 10 * time.Minute

// PushShaping sends every server its current limits; unchanged plans are only
// re-sent every speedRepush.
func (s *SpeedService) PushShaping(ctx context.Context) {
	speedPushMu.Lock()
	defer speedPushMu.Unlock()
	db := database.GetDB()
	w, err := loadSpeedWorld()
	if err != nil {
		logger.Warning("speed:", err)
		return
	}
	var capRows []model.ServerCapacity
	if err := db.Find(&capRows).Error; err != nil {
		logger.Warning("speed: list capacities:", err)
		return
	}
	links := map[int]int{}
	for _, c := range capRows {
		links[c.NodeId] = c.CapacityMbps
	}
	limits := speedLimits(w.rules, w.allUsers(), links)
	var ips []model.NodeClientIp
	if len(limits) > 0 {
		if err := db.Find(&ips).Error; err != nil {
			logger.Warning("speed: list client IPs:", err)
			return
		}
	}
	serverOf := map[string]int{}
	targets := []int{0}
	for id, guid := range w.guidOf {
		serverOf[guid] = id
	}
	for _, n := range w.nodes {
		if n.Enable && n.Status == "online" {
			targets = append(targets, n.Id)
		}
	}
	plans := shapingPlans(limits, ips, serverOf, time.Now().Unix())
	for _, id := range targets {
		plan := plans[id]
		plan.LinkMbps = links[id]
		if err := plan.Normalize(); err != nil {
			logger.Warning("speed: plan for", nodeLabel(id), ":", err)
			continue
		}
		raw, _ := json.Marshal(plan)
		if speedPushed[id] == string(raw) && time.Since(speedPushedAt[id]) < speedRepush {
			continue
		}
		if _, err := applyShaping(ctx, id, plan); err != nil {
			logger.Warning("speed: apply on", nodeLabel(id), ":", err)
			continue
		}
		speedPushed[id], speedPushedAt[id] = string(raw), time.Now()
	}
}

func applyShaping(ctx context.Context, nodeID int, plan shaper.Plan) (shaper.Status, error) {
	if nodeID == 0 {
		return shaper.Apply(plan, shaper.OwnerLocal)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return shaper.Status{}, err
	}
	resp, err := (&NodeService{}).ProxyNodeXray(ctx, nodeID, http.MethodPost, "/speedPlan", "", "application/json", body)
	if err != nil {
		return shaper.Status{}, err
	}
	obj, err := proxyEnvelopeObj(resp)
	if err != nil {
		return shaper.Status{}, err
	}
	var st shaper.Status
	if err := json.Unmarshal(obj, &st); err != nil {
		return shaper.Status{}, fmt.Errorf("node reply: %w", err)
	}
	return st, nil
}
