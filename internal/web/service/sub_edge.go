package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
	"github.com/mhsanaei/3x-ui/v3/internal/web/global"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

const (
	subEdgeStatusOnline  = "online"
	subEdgeStatusOffline = "offline"
	subEdgeMaxBackoff    = time.Minute
)

var (
	subEdgeNode     = &subedge.Node{CacheDir: func() string { return filepath.Join(config.GetDBFolderPath(), "edge-cache") }}
	subEdgeRecorder = &subedge.Recorder{}
	subEdgePushed   = struct {
		sync.Mutex
		seq map[int]uint64
	}{seq: map[int]uint64{}}

	errSubServerOff = errors.New("the subscription server is turned off on this master")
)

// SubEdgeNode is this panel's own edge listener, used when it is a node.
func SubEdgeNode() *subedge.Node { return subEdgeNode }

// SubEdgeRecorder keeps the subscription responses this master gave, for its
// edge nodes' caches.
func SubEdgeRecorder() *subedge.Recorder { return subEdgeRecorder }

// SubEdgeRequest is the admin's add/update body for an edge node.
type SubEdgeRequest struct {
	NodeId int  `json:"nodeId" validate:"gte=1" example:"2"`
	Enable bool `json:"enable" example:"true"`
	Port   int  `json:"port" validate:"gte=1,lte=65535" example:"443"`
	CertId int  `json:"certId" validate:"gte=0" example:"1"`
}

// SubEdgeService manages the nodes that serve this panel's subscriptions.
type SubEdgeService struct {
	nodeService NodeService
}

func (s *SubEdgeService) List() ([]model.SubEdge, error) {
	var rows []model.SubEdge
	err := database.GetDB().Order("id").Find(&rows).Error
	return rows, err
}

func (s *SubEdgeService) Create(ctx context.Context, req SubEdgeRequest) (*model.SubEdge, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	row := &model.SubEdge{NodeId: req.NodeId, Enable: req.Enable, Port: req.Port, CertId: req.CertId, Status: subEdgeStatusOffline}
	if err := database.GetDB().Select("*").Omit("id").Create(row).Error; err != nil {
		return nil, err
	}
	s.configure(ctx, row)
	subEdgeTunnels.kick(s)
	return row, nil
}

func (s *SubEdgeService) Update(ctx context.Context, id int, req SubEdgeRequest) (*model.SubEdge, error) {
	row, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(req); err != nil {
		return nil, err
	}
	if req.NodeId != row.NodeId {
		s.disableOn(ctx, row.NodeId)
	}
	row.NodeId, row.Enable, row.Port, row.CertId = req.NodeId, req.Enable, req.Port, req.CertId
	if err := database.GetDB().Model(row).Select("NodeId", "Enable", "Port", "CertId").Updates(row).Error; err != nil {
		return nil, err
	}
	s.configure(ctx, row)
	subEdgeTunnels.kick(s)
	return row, nil
}

func (s *SubEdgeService) Delete(ctx context.Context, id int) error {
	row, err := s.get(id)
	if err != nil {
		return err
	}
	if err := database.GetDB().Delete(&model.SubEdge{}, id).Error; err != nil {
		return err
	}
	s.disableOn(ctx, row.NodeId)
	subEdgeTunnels.kick(s)
	return nil
}

func (s *SubEdgeService) get(id int) (*model.SubEdge, error) {
	row := &model.SubEdge{}
	if err := database.GetDB().First(row, id).Error; err != nil {
		return nil, fmt.Errorf("subscription edge %d not found: %w", id, err)
	}
	return row, nil
}

func (s *SubEdgeService) validate(req SubEdgeRequest) error {
	if _, err := s.nodeService.GetById(req.NodeId); err != nil {
		return fmt.Errorf("node %d not found", req.NodeId)
	}
	if req.CertId == 0 {
		return nil
	}
	if _, err := subEdgeCert(req.CertId); err != nil {
		return err
	}
	return nil
}

func subEdgeCert(id int) (*model.TlsCertificate, error) {
	cert := &model.TlsCertificate{}
	if err := database.GetDB().First(cert, id).Error; err != nil {
		return nil, fmt.Errorf("certificate %d not found", id)
	}
	return cert, nil
}

// nodeConfig is what the edge's node should run: off when either is disabled.
func (s *SubEdgeService) nodeConfig(row *model.SubEdge, node *model.Node) (subedge.ListenerConfig, error) {
	cfg := subedge.ListenerConfig{Enable: row.Enable && node.Enable, Port: row.Port}
	if !cfg.Enable || row.CertId == 0 {
		return cfg, nil
	}
	cert, err := subEdgeCert(row.CertId)
	if err != nil {
		return cfg, err
	}
	installed := cert.NodeInstalls[node.Id]
	if installed.CertFile == "" {
		return cfg, fmt.Errorf("certificate %s is not on node %s yet", strings.Join(cert.Domains, ", "), node.Name)
	}
	cfg.CertFile, cfg.KeyFile = installed.CertFile, installed.KeyFile
	return cfg, nil
}

// configure pushes the edge's config to its node; a failure is recorded on the
// row and retried by the tunnel keeper.
func (s *SubEdgeService) configure(ctx context.Context, row *model.SubEdge) {
	if err := s.pushConfig(ctx, row); err != nil {
		row.Status, row.LastError = subEdgeStatusOffline, err.Error()
		setSubEdgeStatus(row.Id, row.Status, row.LastError)
	}
}

func (s *SubEdgeService) pushConfig(ctx context.Context, row *model.SubEdge) error {
	node, err := s.nodeService.GetById(row.NodeId)
	if err != nil {
		return err
	}
	cfg, err := s.nodeConfig(row, node)
	if err != nil {
		return err
	}
	remote, err := runtime.GetManager().RemoteFor(node)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return remote.ConfigureSubEdge(cctx, cfg)
}

func (s *SubEdgeService) disableOn(ctx context.Context, nodeID int) {
	node, err := s.nodeService.GetById(nodeID)
	if err != nil {
		return
	}
	remote, err := runtime.GetManager().RemoteFor(node)
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		err = remote.ConfigureSubEdge(cctx, subedge.ListenerConfig{})
	}
	if err != nil {
		logger.Warning("subscription edge left running on node", node.Name, ":", err)
	}
}

func setSubEdgeStatus(id int, status, lastError string) {
	updates := map[string]any{"status": status, "last_error": lastError}
	if status == subEdgeStatusOnline {
		updates["last_seen"] = time.Now().Unix()
	}
	if err := database.GetDB().Model(&model.SubEdge{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		logger.Warning("subscription edge status not saved:", err)
	}
}

// KeepTunnels runs one tunnel per enabled edge and refreshes the status of the
// live ones; the cron job calls it on every master.
func (s *SubEdgeService) KeepTunnels() { subEdgeTunnels.kick(s) }

// StopSubEdgeTunnels closes every tunnel this master runs.
func StopSubEdgeTunnels() { subEdgeTunnels.stopAll() }

type subEdgeKeeper struct {
	cancel context.CancelFunc
	key    string
	online func() bool
}

type subEdgeTunnelSet struct {
	mu      sync.Mutex
	keepers map[int]*subEdgeKeeper
}

var subEdgeTunnels = &subEdgeTunnelSet{keepers: map[int]*subEdgeKeeper{}}

func (t *subEdgeTunnelSet) kick(s *SubEdgeService) {
	rows, err := s.List()
	if err != nil {
		logger.Warning("subscription edges not loaded:", err)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	wanted := map[int]bool{}
	for i := range rows {
		row := rows[i]
		if !row.Enable {
			continue
		}
		wanted[row.Id] = true
		key := fmt.Sprintf("%d/%d/%d", row.NodeId, row.Port, row.CertId)
		if k := t.keepers[row.Id]; k != nil {
			if k.key == key {
				if k.online() {
					setSubEdgeStatus(row.Id, subEdgeStatusOnline, "")
				}
				continue
			}
			k.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		var up sync.Mutex
		online := false
		t.keepers[row.Id] = &subEdgeKeeper{cancel: cancel, key: key, online: func() bool {
			up.Lock()
			defer up.Unlock()
			return online
		}}
		go s.runTunnel(ctx, row.Id, func(v bool) {
			up.Lock()
			online = v
			up.Unlock()
		})
	}
	for id, k := range t.keepers {
		if !wanted[id] {
			k.cancel()
			delete(t.keepers, id)
		}
	}
	subEdgeRecorder.SetActive(len(wanted) > 0)
}

func (t *subEdgeTunnelSet) stopAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, k := range t.keepers {
		k.cancel()
		delete(t.keepers, id)
	}
}

func (s *SubEdgeService) runTunnel(ctx context.Context, id int, setOnline func(bool)) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.tunnelOnce(ctx, id, setOnline)
		setOnline(false)
		if ctx.Err() != nil {
			return
		}
		setSubEdgeStatus(id, subEdgeStatusOffline, err.Error())
		if time.Since(started) > subEdgeMaxBackoff {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, subEdgeMaxBackoff)
	}
}

func (s *SubEdgeService) tunnelOnce(ctx context.Context, id int, setOnline func(bool)) error {
	row, err := s.get(id)
	if err != nil {
		return err
	}
	if err := s.pushConfig(ctx, row); err != nil {
		return err
	}
	if currentSubHandler() == nil {
		return errSubServerOff
	}
	node, err := s.nodeService.GetById(row.NodeId)
	if err != nil {
		return err
	}
	remote, err := runtime.GetManager().RemoteFor(node)
	if err != nil {
		return err
	}
	conn, err := remote.OpenSubEdgeTunnel(ctx, thisMaster())
	if err != nil {
		return err
	}
	setOnline(true)
	setSubEdgeStatus(id, subEdgeStatusOnline, "")
	subedge.ServeTunnel(ctx, conn, subedge.FromEdge(http.HandlerFunc(serveSubThroughEdge)))
	return errors.New("the tunnel to the node closed")
}

// PushCaches sends each enabled edge node the responses recorded since its
// last push, and the subscriptions it may still serve.
func (s *SubEdgeService) PushCaches(ctx context.Context) {
	rows, err := s.List()
	if err != nil {
		logger.Warning("subscription edges not loaded:", err)
		return
	}
	var subIDs []string
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("enable = ? AND sub_id <> ''", true).
		Distinct().Pluck("sub_id", &subIDs).Error; err != nil {
		logger.Warning("subscriptions for edge caches not loaded:", err)
		return
	}
	for i := range rows {
		if !rows[i].Enable {
			continue
		}
		if err := s.pushCache(ctx, &rows[i], subIDs); err != nil {
			logger.Warning("subscription edge cache not sent to node", rows[i].NodeId, ":", err)
		}
	}
}

func (s *SubEdgeService) pushCache(ctx context.Context, row *model.SubEdge, subIDs []string) error {
	node, err := s.nodeService.GetById(row.NodeId)
	if err != nil {
		return err
	}
	remote, err := runtime.GetManager().RemoteFor(node)
	if err != nil {
		return err
	}
	subEdgePushed.Lock()
	defer subEdgePushed.Unlock()
	responses, next := subEdgeRecorder.Since(subEdgePushed.seq[row.Id])
	for _, update := range subedge.SplitCacheUpdate(subIDs, responses) {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		err := remote.UpdateSubEdgeCache(cctx, update)
		cancel()
		if err != nil {
			return err
		}
	}
	subEdgePushed.seq[row.Id] = next
	return nil
}

func thisMaster() subedge.Master {
	priority, configured, err := config.GetMasterPriority()
	if !configured || err != nil {
		priority = 1
	}
	return subedge.Master{Name: config.GetMasterName(), Priority: priority}
}

// currentSubHandler is the running subscription server, looked up per request
// so a restarted server with new settings takes over existing tunnels.
func currentSubHandler() http.Handler {
	srv, ok := global.GetSubServer().(interface{ Handler() http.Handler })
	if !ok {
		return nil
	}
	return srv.Handler()
}

func serveSubThroughEdge(w http.ResponseWriter, r *http.Request) {
	h := currentSubHandler()
	if h == nil {
		http.Error(w, errSubServerOff.Error(), http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

// ConfigureSubEdgeNode is the node side of ConfigureSubEdge: keep the config
// for restarts and apply it now.
func (s *SettingService) ConfigureSubEdgeNode(cfg subedge.ListenerConfig) error {
	if cfg.Enable && (cfg.Port < 1 || cfg.Port > 65535) {
		return fmt.Errorf("invalid edge port %d", cfg.Port)
	}
	for _, f := range []string{cfg.CertFile, cfg.KeyFile} {
		if f != "" && !strings.HasPrefix(filepath.Clean(f), filepath.Clean(config.GetCertFolderPath())+string(filepath.Separator)) {
			return fmt.Errorf("edge certificate %s is outside the certificate folder", f)
		}
	}
	if err := subEdgeNode.Apply(cfg); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.setString("subEdgeNode", string(raw))
}

// ApplySavedSubEdge restarts the edge listener a master configured earlier.
func (s *SettingService) ApplySavedSubEdge() {
	raw, err := s.getString("subEdgeNode")
	if err != nil || raw == "" {
		return
	}
	var cfg subedge.ListenerConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		logger.Warning("saved subscription edge config unreadable:", err)
		return
	}
	if err := subEdgeNode.Apply(cfg); err != nil {
		logger.Warning("subscription edge not started:", err)
	}
}
