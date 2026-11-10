package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
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

// SpeedService keeps users' speed contracts: a hard cap enforced with tc on the
// server they use, and a minimum new users must find free on their server.
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

// ServerFreeSpeed is the live load of a server with a known capacity.
type ServerFreeSpeed struct {
	NodeId          int     `json:"nodeId" example:"0"`
	CapacityMbps    int     `json:"capacityMbps" example:"1000"`
	UsedMbps        float64 `json:"usedMbps" example:"420.5"`
	Online          int     `json:"online" example:"12"`
	FreePerUserMbps float64 `json:"freePerUserMbps" example:"44.5"`
}

// SpeedOverview is everything the speed card shows.
type SpeedOverview struct {
	Clients []model.ClientSpeed    `json:"clients"`
	Servers []model.ServerCapacity `json:"servers"`
	Load    []ServerFreeSpeed      `json:"load"`
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

// serverLoad is one server's capacity, use and online user count.
type serverLoad struct {
	Capacity float64
	Used     float64
	Online   int
}

// freeForNewUser is what one more user would get if the free speed were shared.
func (l serverLoad) freeForNewUser() float64 { return (l.Capacity - l.Used) / float64(l.Online+1) }

func (l serverLoad) freePerUser() float64 {
	return (l.Capacity - l.Used) / float64(max(l.Online, 1))
}

// speedWorld is the shared view of nodes and online users one pass works from.
type speedWorld struct {
	nodes  []*model.Node
	guidOf map[int]string
	byGuid map[string][]string
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
	return w, nil
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

// loads covers the servers with a known capacity.
func (w *speedWorld) loads() (map[int]serverLoad, error) {
	var caps []model.ServerCapacity
	if err := database.GetDB().Find(&caps).Error; err != nil {
		return nil, err
	}
	used := map[int]float64{0: localThroughputMbps()}
	for _, n := range w.nodes {
		used[n.Id] = float64(max(n.NetUp, n.NetDown)) * 8 / 1e6
	}
	out := map[int]serverLoad{}
	for _, c := range caps {
		out[c.NodeId] = serverLoad{Capacity: float64(c.CapacityMbps), Used: used[c.NodeId], Online: len(w.online(c.NodeId))}
	}
	return out, nil
}

func minSpeeds() (map[string]int, error) {
	var rows []model.ClientSpeed
	if err := database.GetDB().Where("min_mbps > 0").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Email] = r.MinMbps
	}
	return out, nil
}

func (s *SpeedService) Overview() (*SpeedOverview, error) {
	out := &SpeedOverview{Clients: []model.ClientSpeed{}, Servers: []model.ServerCapacity{}, Load: []ServerFreeSpeed{}}
	db := database.GetDB()
	if err := db.Order("email asc").Find(&out.Clients).Error; err != nil {
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
			Online: l.Online, FreePerUserMbps: math.Round(l.freePerUser()*10) / 10,
		})
	}
	return out, nil
}

// SpeedShortage is a server that cannot give some online users their minimum
// while no other server has room for them either.
type SpeedShortage struct {
	NodeId          int
	FreePerUserMbps float64
	MinMbps         int
	Users           []string
}

func speedShortages(loads map[int]serverLoad, online map[int][]string, mins map[string]int) []SpeedShortage {
	var out []SpeedShortage
	ids := make([]int, 0, len(loads))
	for id := range loads {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		free := loads[id].freePerUser()
		short := SpeedShortage{NodeId: id, FreePerUserMbps: math.Round(free*10) / 10}
		for _, email := range online[id] {
			need := mins[email]
			if need == 0 || free >= float64(need) {
				continue
			}
			roomElsewhere := false
			for other, l := range loads {
				if other != id && l.freeForNewUser() >= float64(need) {
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
	mins, err := minSpeeds()
	if err != nil {
		return nil, err
	}
	online := map[int][]string{}
	for id := range loads {
		online[id] = w.online(id)
	}
	return speedShortages(loads, online, mins), nil
}

// shapingPlans caps every recently seen IP of a capped user on the server the
// IP was seen on, keyed by server.
func shapingPlans(caps map[string]int, ips []model.NodeClientIp, serverOf map[string]int, now int64) map[int]shaper.Plan {
	out := map[int]shaper.Plan{}
	for _, row := range ips {
		mbps, ok := caps[row.Email]
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
				plan.Limits = append(plan.Limits, shaper.Limit{IP: e.IP, Mbps: mbps})
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

// PushShaping sends every server its current caps; unchanged plans are only
// re-sent every speedRepush.
func (s *SpeedService) PushShaping(ctx context.Context) {
	speedPushMu.Lock()
	defer speedPushMu.Unlock()
	db := database.GetDB()
	var rows []model.ClientSpeed
	if err := db.Where("max_mbps > 0").Find(&rows).Error; err != nil {
		logger.Warning("speed: list caps:", err)
		return
	}
	caps := map[string]int{}
	for _, r := range rows {
		caps[r.Email] = r.MaxMbps
	}
	w, err := loadSpeedWorld()
	if err != nil {
		logger.Warning("speed:", err)
		return
	}
	var ips []model.NodeClientIp
	if len(caps) > 0 {
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
	plans := shapingPlans(caps, ips, serverOf, time.Now().Unix())
	for _, id := range targets {
		plan := plans[id]
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
