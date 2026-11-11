package service

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	// MasterBeatSeconds is how often every master writes its heartbeat.
	MasterBeatSeconds = 5
	// masterAliveSeconds without a heartbeat and a master counts as down.
	masterAliveSeconds = 20
	// masterSettleSeconds a returning master waits before it takes the lead
	// back, so a flapping primary does not bounce leadership every beat.
	masterSettleSeconds = 30
)

// electLeader picks the best-ranked alive master that has been up long enough
// to settle, or the best alive one when none has. Every master runs the same
// function over the same rows, so they agree without talking to each other.
func electLeader(members []model.MasterMember, now int64) string {
	alive := make([]model.MasterMember, 0, len(members))
	for _, m := range members {
		if now-m.LastSeen <= masterAliveSeconds {
			alive = append(alive, m)
		}
	}
	sort.Slice(alive, func(i, j int) bool {
		if alive[i].Priority != alive[j].Priority {
			return alive[i].Priority < alive[j].Priority
		}
		return alive[i].Name < alive[j].Name
	})
	for _, m := range alive {
		if now-m.UpSince >= masterSettleSeconds {
			return m.Name
		}
	}
	if len(alive) > 0 {
		return alive[0].Name
	}
	return ""
}

// clusterLeader is true on a single master and, in a cluster, only on the
// master that runs the jobs which must not run twice.
var clusterLeader atomic.Bool

func init() { clusterLeader.Store(true) }

// InitClusterRole makes a cluster master start as a follower until its
// heartbeat wins an election; a single master leads from the start.
func InitClusterRole() { clusterLeader.Store(!ClusterEnabled()) }

// IsClusterLeader reports whether this panel must run leader-only work.
func IsClusterLeader() bool { return clusterLeader.Load() }

// MasterClusterService keeps this master's heartbeat and leadership.
type MasterClusterService struct {
	mu      sync.Mutex
	elected int
}

// MasterStatus is one master as the masters card shows it.
type MasterStatus struct {
	Name     string `json:"name" example:"master-1"`
	Priority int    `json:"priority" example:"1"`
	Url      string `json:"url" example:"https://panel-1.example.com:2053"`
	Alive    bool   `json:"alive" example:"true"`
	Leader   bool   `json:"leader" example:"true"`
	Self     bool   `json:"self" example:"true"`
	LastSeen int64  `json:"lastSeen" example:"1762736400"`
	// Pending marks a master added in the panel that has not started yet.
	Pending bool `json:"pending" example:"false"`
}

// MasterSetup is what a newly added master needs in its env file.
type MasterSetup struct {
	Name     string `json:"name" example:"master-3"`
	Priority int    `json:"priority" example:"3"`
	Env      string `json:"env" example:"XUI_DB_TYPE=postgres\nXUI_MASTER_NAME=master-3\n"`
	// KeyFile must be copied from this master when node tokens are encrypted.
	KeyFile string `json:"keyFile,omitempty" example:"/etc/x-ui/node_token_key.json"`
}

// Reasons a change to the masters list is refused.
const (
	MasterChangeNotCluster = "notCluster"
	MasterChangeNotPrimary = "notPrimary"
	MasterChangeOffline    = "offline"
	MasterChangeUnknown    = "unknown"
	MasterChangeRunning    = "running"
	MasterChangeNameTaken  = "nameTaken"
	MasterChangeBadOrder   = "badOrder"
)

// MasterChangeError refuses a change to the masters list; Master names the
// master the reason is about.
type MasterChangeError struct {
	Reason string
	Master string
}

func (e *MasterChangeError) Error() string {
	return "masters cannot be changed: " + e.Reason + " " + e.Master
}

// ClusterEnabled reports whether XUI_MASTER_PRIORITY made this panel one of
// several masters.
func ClusterEnabled() bool {
	_, configured, err := config.GetMasterPriority()
	return configured && err == nil
}

func dbNow(db *gorm.DB) (int64, error) {
	query := "SELECT CAST(strftime('%s','now') AS INTEGER)"
	if database.IsPostgres() {
		query = "SELECT CAST(EXTRACT(EPOCH FROM now()) AS BIGINT)"
	}
	var now int64
	err := db.Raw(query).Scan(&now).Error
	return now, err
}

// Beat writes this master's heartbeat and re-runs the election. It returns
// the leader's name and whether this master's own role flipped.
func (s *MasterClusterService) Beat() (leader string, flipped bool, err error) {
	priority, configured, err := config.GetMasterPriority()
	if !configured || err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := config.GetMasterName()
	leader, err = s.beat(name, priority)
	was := clusterLeader.Load()
	switch {
	case err != nil || leader != name:
		s.elected = 0
	default:
		s.elected++
	}
	// Claiming waits for a second winning beat so the master stepping down
	// has had a beat to see the new leader; stepping down is immediate.
	leads := s.elected >= 2
	clusterLeader.Store(leads)
	writeClusterRole(leads)
	return leader, leads != was, err
}

func (s *MasterClusterService) beat(name string, priority int) (string, error) {
	db := database.GetDB()
	var members []model.MasterMember
	var now int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		now, err = dbNow(tx)
		if err != nil {
			return fmt.Errorf("read database clock: %w", err)
		}
		self := model.MasterMember{}
		err = tx.Where("name = ?", name).First(&self).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Find(&members).Error; err != nil {
			return err
		}
		if self.Id == 0 {
			self.Name, self.Priority = name, joinPriority(members, priority)
		}
		if self.Id == 0 || now-self.LastSeen > masterAliveSeconds {
			self.UpSince = now
		}
		if url := config.GetMasterURL(); url != "" {
			self.Url = url
		}
		self.LastSeen = now
		if err := tx.Save(&self).Error; err != nil {
			return err
		}
		members = append(slices.DeleteFunc(members, func(m model.MasterMember) bool { return m.Name == name }), self)
		return nil
	})
	if err != nil {
		return "", err
	}
	return electLeader(members, now), nil
}

// joinPriority is the rank a master new to the list takes: the one its env
// asks for while free, else last, so a forgotten master cannot return as primary.
func joinPriority(members []model.MasterMember, wanted int) int {
	last := 0
	free := true
	for _, m := range members {
		last = max(last, m.Priority)
		free = free && m.Priority != wanted
	}
	if free {
		return wanted
	}
	return last + 1
}

// List reports every master that ever joined, for the masters card.
func (s *MasterClusterService) List() ([]MasterStatus, error) {
	if !ClusterEnabled() {
		return []MasterStatus{}, nil
	}
	db := database.GetDB()
	now, err := dbNow(db)
	if err != nil {
		return nil, err
	}
	var members []model.MasterMember
	if err := db.Order("priority asc, name asc").Find(&members).Error; err != nil {
		return nil, err
	}
	leader := electLeader(members, now)
	self := config.GetMasterName()
	out := make([]MasterStatus, 0, len(members))
	for _, m := range members {
		out = append(out, MasterStatus{
			Name: m.Name, Priority: m.Priority, Url: m.Url, LastSeen: m.LastSeen,
			Alive: now-m.LastSeen <= masterAliveSeconds, Leader: m.Name == leader, Self: m.Name == self,
			Pending: m.LastSeen == 0,
		})
	}
	return out, nil
}

// change runs edit on the masters list only on the leading primary and only
// while every master except spare is online, so no master misses the change.
func (s *MasterClusterService) change(spare string, edit func(tx *gorm.DB, members []model.MasterMember, now int64) error) error {
	if !ClusterEnabled() {
		return &MasterChangeError{Reason: MasterChangeNotCluster}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		now, err := dbNow(tx)
		if err != nil {
			return fmt.Errorf("read database clock: %w", err)
		}
		var members []model.MasterMember
		if err := tx.Order("priority asc, name asc").Find(&members).Error; err != nil {
			return err
		}
		for _, m := range members {
			if m.Name != spare && now-m.LastSeen > masterAliveSeconds {
				return &MasterChangeError{Reason: MasterChangeOffline, Master: m.Name}
			}
		}
		if len(members) == 0 || members[0].Name != config.GetMasterName() || !IsClusterLeader() {
			primary := ""
			if len(members) > 0 {
				primary = members[0].Name
			}
			return &MasterChangeError{Reason: MasterChangeNotPrimary, Master: primary}
		}
		return edit(tx, members, now)
	})
}

// Reorder ranks the masters in the given order, first = primary; the new
// primary takes the lead on its next beats.
func (s *MasterClusterService) Reorder(names []string) error {
	return s.change("", func(tx *gorm.DB, members []model.MasterMember, _ int64) error {
		known := make(map[string]bool, len(members))
		for _, m := range members {
			known[m.Name] = true
		}
		if len(names) != len(members) {
			return &MasterChangeError{Reason: MasterChangeBadOrder}
		}
		for _, name := range names {
			if !known[name] {
				return &MasterChangeError{Reason: MasterChangeBadOrder}
			}
			delete(known, name)
		}
		for i, name := range names {
			if err := tx.Model(&model.MasterMember{}).Where("name = ?", name).Update("priority", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Add lists a new master last in priority; it shows as pending until it
// starts with the returned env.
func (s *MasterClusterService) Add(name, url string) (MasterSetup, error) {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	var setup MasterSetup
	err := s.change("", func(tx *gorm.DB, members []model.MasterMember, _ int64) error {
		last := 0
		for _, m := range members {
			if m.Name == name {
				return &MasterChangeError{Reason: MasterChangeNameTaken, Master: name}
			}
			last = max(last, m.Priority)
		}
		added := model.MasterMember{Name: name, Url: url, Priority: last + 1}
		if err := tx.Create(&added).Error; err != nil {
			return err
		}
		setup = MasterSetup{Name: name, Priority: added.Priority, Env: masterEnv(name, url, added.Priority)}
		if config.GetNodeTokenEncryptionMode() != "" {
			setup.KeyFile = config.GetNodeTokenKeyFile()
		}
		return nil
	})
	return setup, err
}

func masterEnv(name, url string, priority int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "XUI_DB_TYPE=%s\n", config.GetDBKind())
	if dsn := config.GetDBDSN(); dsn != "" {
		fmt.Fprintf(&b, "XUI_DB_DSN=%s\n", dsn)
	}
	if mode := config.GetNodeTokenEncryptionMode(); mode != "" {
		fmt.Fprintf(&b, "NODE_TOKEN_ENCRYPTION=%s\nXUI_NODE_TOKEN_KEY_FILE=%s\n", mode, config.GetNodeTokenKeyFile())
	}
	fmt.Fprintf(&b, "XUI_MASTER_PRIORITY=%d\nXUI_MASTER_NAME=%s\n", priority, name)
	if url != "" {
		fmt.Fprintf(&b, "XUI_MASTER_URL=%s\n", url)
	}
	return b.String()
}

// Forget drops a master that is down for good from the list.
func (s *MasterClusterService) Forget(name string) error {
	return s.change(name, func(tx *gorm.DB, members []model.MasterMember, now int64) error {
		i := slices.IndexFunc(members, func(m model.MasterMember) bool { return m.Name == name })
		if i < 0 {
			return &MasterChangeError{Reason: MasterChangeUnknown, Master: name}
		}
		if now-members[i].LastSeen <= masterAliveSeconds {
			return &MasterChangeError{Reason: MasterChangeRunning, Master: name}
		}
		return tx.Delete(&members[i]).Error
	})
}

// writeClusterRole leaves "<role> <unix time>" for `x-ui cluster is-leader`.
func writeClusterRole(leader bool) {
	role := "follower"
	if leader {
		role = "leader"
	}
	_ = os.WriteFile(config.GetClusterRolePath(), []byte(role+" "+strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o600)
}

// ReadClusterRole tells keepalived whether this master leads: the role file
// must say leader and be fresher than two missed beats.
func ReadClusterRole(now int64) (bool, error) {
	raw, err := os.ReadFile(config.GetClusterRolePath())
	if err != nil {
		return false, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		return false, fmt.Errorf("cluster role file is malformed")
	}
	at, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return false, fmt.Errorf("cluster role file is malformed")
	}
	if now-at > 3*MasterBeatSeconds {
		return false, fmt.Errorf("the panel has not written its role for %ds", now-at)
	}
	return fields[0] == "leader", nil
}
