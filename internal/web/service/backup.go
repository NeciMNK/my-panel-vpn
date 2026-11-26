package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

var (
	backupPasswordAAD = []byte("settings/backup_password")
	backupKeyAAD      = []byte("settings/backup_private_key")
)

const (
	backupDbFolder = "db"
	// An on-change backup waits for edits to settle, and runs at most this often.
	backupChangeSettle  = 2 * time.Minute
	backupChangeMinGap  = 10 * time.Minute
	nodeBackupTimeout   = 5 * time.Minute
	backupFolderPerNode = "node-"
)

// BackupSettings is the backup server and schedule; the password and key are
// never sent back, only whether they are set.
type BackupSettings struct {
	Host            string `json:"host" example:"backup.example.com"`
	Port            int    `json:"port" example:"22"`
	User            string `json:"user" example:"root"`
	Dir             string `json:"dir" example:"/root/my-panel-vpn-backups"`
	HasPassword     bool   `json:"hasPassword" example:"false"`
	HasPrivateKey   bool   `json:"hasPrivateKey" example:"true"`
	HostKey         string `json:"hostKey" example:"SHA256:Vf0Yb0kq3y4I2n8M2m0p1Yc3Qz8jv8p0Hq4l5r6s7t8"`
	DbEnable        bool   `json:"dbEnable" example:"true"`
	DbEveryHours    int    `json:"dbEveryHours" example:"24"`
	DbOnChange      bool   `json:"dbOnChange" example:"true"`
	NodesEnable     bool   `json:"nodesEnable" example:"true"`
	NodesEveryHours int    `json:"nodesEveryHours" example:"24"`
	Keep            int    `json:"keep" example:"7"`
}

// BackupSettingsRequest saves BackupSettings; a nil password or key keeps the
// stored one and an empty string clears it.
type BackupSettingsRequest struct {
	Host            string  `json:"host"`
	Port            int     `json:"port"`
	User            string  `json:"user"`
	Dir             string  `json:"dir"`
	Password        *string `json:"password,omitempty"`
	PrivateKey      *string `json:"privateKey,omitempty"`
	DbEnable        bool    `json:"dbEnable"`
	DbEveryHours    int     `json:"dbEveryHours"`
	DbOnChange      bool    `json:"dbOnChange"`
	NodesEnable     bool    `json:"nodesEnable"`
	NodesEveryHours int     `json:"nodesEveryHours"`
	Keep            int     `json:"keep"`
}

// BackupRun is the outcome of the last backup of one thing.
type BackupRun struct {
	At    int64  `json:"at" example:"1764039600"`
	File  string `json:"file" example:"panel.example.com_2026-11-25_030000.dump"`
	Error string `json:"error" example:""`
}

// BackupNodeRun is the last backup of one node.
type BackupNodeRun struct {
	NodeId int    `json:"nodeId" example:"3"`
	Name   string `json:"name" example:"ru-msk-1"`
	At     int64  `json:"at" example:"1764039600"`
	File   string `json:"file" example:"ru-msk-1_2026-11-25_030000.tar.gz"`
	Error  string `json:"error" example:""`
}

// BackupStatus is what the backups tab shows.
type BackupStatus struct {
	Running string          `json:"running" example:""`
	Db      BackupRun       `json:"db"`
	NodesAt int64           `json:"nodesAt" example:"1764039600"`
	Nodes   []BackupNodeRun `json:"nodes"`
}

// BackupService runs backups to the backup server and lists them.
type BackupService struct {
	settingService SettingService
	serverService  ServerService
	nodeService    NodeService
}

var backupState struct {
	mu       sync.Mutex
	running  string
	savedSeq uint64
}

func (s *BackupService) Settings() (*BackupSettings, error) {
	set := &s.settingService
	var out BackupSettings
	var err error
	str := func(key string) string {
		v, e := set.getString(key)
		if e != nil && err == nil {
			err = e
		}
		return v
	}
	num := func(key string) int {
		v, e := strconv.Atoi(effectiveSettingValue(key, str(key)))
		if e != nil && err == nil {
			err = fmt.Errorf("setting %s: %w", key, e)
		}
		return v
	}
	flag := func(key string) bool { return effectiveSettingValue(key, str(key)) == "true" }
	out.Host = str("backupHost")
	out.Port = num("backupPort")
	out.User = str("backupUser")
	out.Dir = str("backupDir")
	out.HasPassword = str("backupPassword") != ""
	out.HasPrivateKey = str("backupPrivateKey") != ""
	out.HostKey = str("backupHostKey")
	out.DbEnable = flag("backupDbEnable")
	out.DbEveryHours = num("backupDbEveryHours")
	out.DbOnChange = flag("backupDbOnChange")
	out.NodesEnable = flag("backupNodesEnable")
	out.NodesEveryHours = num("backupNodesEveryHours")
	out.Keep = num("backupKeep")
	return &out, err
}

func (s *BackupService) SaveSettings(req BackupSettingsRequest) error {
	host := strings.TrimSpace(req.Host)
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 {
		return errors.New("SSH port must be 1-65535")
	}
	if req.DbEveryHours < 1 || req.NodesEveryHours < 1 {
		return errors.New("backup interval must be at least one hour")
	}
	if req.Keep < 1 || req.Keep > 1000 {
		return errors.New("keep 1-1000 backups")
	}
	dir := strings.TrimSpace(req.Dir)
	if host != "" && dir == "" {
		return errors.New("backup folder is empty")
	}
	old, err := s.Settings()
	if err != nil {
		return err
	}
	set := &s.settingService
	values := map[string]string{
		"backupHost":            host,
		"backupPort":            strconv.Itoa(req.Port),
		"backupUser":            strings.TrimSpace(req.User),
		"backupDir":             dir,
		"backupDbEnable":        strconv.FormatBool(req.DbEnable),
		"backupDbEveryHours":    strconv.Itoa(req.DbEveryHours),
		"backupDbOnChange":      strconv.FormatBool(req.DbOnChange),
		"backupNodesEnable":     strconv.FormatBool(req.NodesEnable),
		"backupNodesEveryHours": strconv.Itoa(req.NodesEveryHours),
		"backupKeep":            strconv.Itoa(req.Keep),
	}
	if old.Host != host || old.Port != req.Port {
		values["backupHostKey"] = ""
	}
	if req.Password != nil {
		enc, err := nodetoken.EncryptBound(backupPasswordAAD, *req.Password)
		if err != nil {
			return err
		}
		values["backupPassword"] = enc
	}
	if req.PrivateKey != nil {
		key := strings.TrimSpace(*req.PrivateKey)
		if key != "" {
			if err := backup.ValidatePrivateKey(key); err != nil {
				return err
			}
		}
		enc, err := nodetoken.EncryptBound(backupKeyAAD, key)
		if err != nil {
			return err
		}
		values["backupPrivateKey"] = enc
	}
	for k, v := range values {
		if err := set.setString(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *BackupService) secret(key string, aad []byte) (string, error) {
	stored, err := s.settingService.getString(key)
	if err != nil || stored == "" {
		return "", err
	}
	return nodetoken.DecryptBound(aad, stored)
}

func (s *BackupService) login() (backup.SSHLogin, string, error) {
	cfg, err := s.Settings()
	if err != nil {
		return backup.SSHLogin{}, "", err
	}
	if cfg.Host == "" {
		return backup.SSHLogin{}, "", errors.New("no backup server is set")
	}
	password, err := s.secret("backupPassword", backupPasswordAAD)
	if err != nil {
		return backup.SSHLogin{}, "", err
	}
	key, err := s.secret("backupPrivateKey", backupKeyAAD)
	if err != nil {
		return backup.SSHLogin{}, "", err
	}
	return backup.SSHLogin{Host: cfg.Host, Port: cfg.Port, User: cfg.User, Password: password, PrivateKey: key, HostKey: cfg.HostKey}, cfg.Dir, nil
}

// openStore connects to the backup server, pinning its host key on first use.
func (s *BackupService) openStore(ctx context.Context) (*backup.Store, error) {
	l, dir, err := s.login()
	if err != nil {
		return nil, err
	}
	st, err := backup.OpenStore(ctx, l, dir)
	if err != nil {
		return nil, err
	}
	if l.HostKey == "" && st.HostKey != "" {
		if err := s.settingService.setString("backupHostKey", st.HostKey); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	return st, nil
}

// Test logs in to the backup server and returns the host key it pinned.
func (s *BackupService) Test(ctx context.Context) (string, error) {
	st, err := s.openStore(ctx)
	if err != nil {
		return "", err
	}
	defer st.Close()
	return st.HostKey, nil
}

func (s *BackupService) loadStatus() BackupStatus {
	var st BackupStatus
	raw, err := s.settingService.getString("backupStatus")
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s *BackupService) saveStatus(update func(*BackupStatus)) {
	backupState.mu.Lock()
	defer backupState.mu.Unlock()
	st := s.loadStatus()
	update(&st)
	st.Running = ""
	raw, _ := json.Marshal(st)
	if err := s.settingService.setString("backupStatus", string(raw)); err != nil {
		logger.Warning("backup status not saved:", err)
	}
}

func (s *BackupService) Status() BackupStatus {
	st := s.loadStatus()
	backupState.mu.Lock()
	st.Running = backupState.running
	backupState.mu.Unlock()
	if st.Nodes == nil {
		st.Nodes = []BackupNodeRun{}
	}
	return st
}

// ErrBackupRunning means another backup is still running.
var ErrBackupRunning = errors.New("a backup is already running")

func beginBackup(what string) bool {
	backupState.mu.Lock()
	defer backupState.mu.Unlock()
	if backupState.running != "" {
		return false
	}
	backupState.running = what
	return true
}

func endBackup() {
	backupState.mu.Lock()
	backupState.running = ""
	backupState.mu.Unlock()
}

// BackupDB copies the panel database to the backup server now.
func (s *BackupService) BackupDB(ctx context.Context) error {
	if !beginBackup("db") {
		return ErrBackupRunning
	}
	defer endBackup()
	seq, _ := database.ConfigChanges()
	name, err := s.backupDB(ctx)
	run := BackupRun{At: time.Now().Unix(), File: name}
	if err != nil {
		run.Error = err.Error()
		logger.Warning("database backup failed:", err)
	} else {
		backupState.mu.Lock()
		backupState.savedSeq = seq
		backupState.mu.Unlock()
	}
	s.saveStatus(func(st *BackupStatus) { st.Db = run })
	return err
}

func (s *BackupService) backupDB(ctx context.Context) (string, error) {
	data, err := s.serverService.GetDb()
	if err != nil {
		return "", err
	}
	st, err := s.openStore(ctx)
	if err != nil {
		return "", err
	}
	defer st.Close()
	name := s.serverService.BackupFilename("")
	if err := st.Put(backupDbFolder, name, bytes.NewReader(data)); err != nil {
		return "", err
	}
	return name, st.Prune(backupDbFolder, s.keep())
}

func (s *BackupService) keep() int {
	cfg, err := s.Settings()
	if err != nil || cfg.Keep < 1 {
		return 7
	}
	return cfg.Keep
}

// NodeBackupFolder is where the backups of one node are kept; the id, not the
// renameable name, keeps a node's history in one place.
func NodeBackupFolder(nodeID int) string { return backupFolderPerNode + strconv.Itoa(nodeID) }

// BackupNodes copies every enabled node's state to the backup server now.
func (s *BackupService) BackupNodes(ctx context.Context) error {
	if !beginBackup("nodes") {
		return ErrBackupRunning
	}
	defer endBackup()
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		return err
	}
	st, storeErr := s.openStore(ctx)
	if storeErr == nil {
		defer st.Close()
	}
	runs := make([]BackupNodeRun, 0, len(nodes))
	var failed []string
	for _, n := range nodes {
		if !n.Enable {
			continue
		}
		run := BackupNodeRun{NodeId: n.Id, Name: n.Name, At: time.Now().Unix()}
		name, err := "", storeErr
		if err == nil {
			name, err = s.backupNode(ctx, st, n)
		}
		run.File = name
		if err != nil {
			run.Error = err.Error()
			failed = append(failed, n.Name)
			logger.Warningf("backup of node %s failed: %v", n.Name, err)
		}
		runs = append(runs, run)
	}
	s.saveStatus(func(bs *BackupStatus) {
		bs.NodesAt = time.Now().Unix()
		bs.Nodes = runs
	})
	if len(failed) > 0 {
		return fmt.Errorf("backup failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

func (s *BackupService) backupNode(ctx context.Context, st *backup.Store, n *model.Node) (string, error) {
	tmp, err := os.CreateTemp("", "node-backup-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	ctx, cancel := context.WithTimeout(ctx, nodeBackupTimeout)
	defer cancel()
	if err := fetchNodeArchive(ctx, n, tmp); err != nil {
		return "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := backup.InspectNodeArchive(tmp); err != nil {
		return "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	name := backup.SafeName(n.Name) + backupDateSuffix(time.Now()) + ".tar.gz"
	folder := NodeBackupFolder(n.Id)
	if err := st.Put(folder, name, tmp); err != nil {
		return "", err
	}
	return name, st.Prune(folder, s.keep())
}

// fetchNodeArchive downloads a node's own state through its panel API.
func fetchNodeArchive(ctx context.Context, n *model.Node, w io.Writer) error {
	addr, err := netsafe.NormalizeHost(n.Address)
	if err != nil {
		return err
	}
	scheme := n.Scheme
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(addr, strconv.Itoa(n.Port)),
		Path:   normalizeBasePath(n.BasePath) + "panel/api/server/nodeBackup",
	}
	req, err := http.NewRequestWithContext(netsafe.ContextWithAllowPrivate(ctx, n.AllowPrivateAddress), http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if n.ApiToken != "" {
		token, err := nodetoken.Decrypt(n.Id, n.ApiToken)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	proxyURL := ""
	if n.OutboundTag != "" {
		if mgr := runtime.GetManager(); mgr != nil {
			proxyURL = mgr.NodeEgressProxyURL(n.Id)
		}
	}
	client, err := runtime.HTTPClientForNode(n, proxyURL)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from the node; update the node's panel if it predates backups", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		var env struct{ Msg string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&env)
		return fmt.Errorf("the node refused the backup: %s", env.Msg)
	}
	n64, err := io.Copy(w, io.LimitReader(resp.Body, backup.MaxNodeArchiveBytes+1))
	if err != nil {
		return err
	}
	if n64 > backup.MaxNodeArchiveBytes {
		return errors.New("node backup is larger than 512 MB")
	}
	return nil
}

// NodeBackups lists the backups kept for a node, newest first.
func (s *BackupService) NodeBackups(ctx context.Context, nodeID int) ([]backup.StoredFile, error) {
	st, err := s.openStore(ctx)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	files, err := st.List(NodeBackupFolder(nodeID))
	if files == nil {
		files = []backup.StoredFile{}
	}
	return files, err
}

// Tick runs the backups that are due; every master calls it each minute.
func (s *BackupService) Tick(ctx context.Context, now time.Time) {
	cfg, err := s.Settings()
	if err != nil || cfg.Host == "" {
		return
	}
	st := s.loadStatus()
	unix := now.Unix()
	leader := IsClusterLeader()
	seq, changedAt := database.ConfigChanges()
	backupState.mu.Lock()
	unsaved := seq != backupState.savedSeq
	backupState.mu.Unlock()

	dbDue := leader && cfg.DbEnable && unix-st.Db.At >= int64(cfg.DbEveryHours)*3600
	changeDue := cfg.DbOnChange && unsaved &&
		unix-changedAt >= int64(backupChangeSettle/time.Second) && unix-st.Db.At >= int64(backupChangeMinGap/time.Second)
	if dbDue || changeDue {
		_ = s.BackupDB(ctx)
	}
	if leader && cfg.NodesEnable && unix-st.NodesAt >= int64(cfg.NodesEveryHours)*3600 {
		_ = s.BackupNodes(ctx)
	}
}
