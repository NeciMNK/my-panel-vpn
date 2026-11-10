package service

import (
	"errors"
	"fmt"
	"os"
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
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
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
		if self.Id == 0 || now-self.LastSeen > masterAliveSeconds {
			self.UpSince = now
		}
		self.Name, self.Priority, self.Url, self.LastSeen = name, priority, config.GetMasterURL(), now
		if err := tx.Save(&self).Error; err != nil {
			return err
		}
		return tx.Find(&members).Error
	})
	if err != nil {
		return "", err
	}
	return electLeader(members, now), nil
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
		})
	}
	return out, nil
}

// Forget drops a master that is down for good from the list.
func (s *MasterClusterService) Forget(name string) error {
	db := database.GetDB()
	now, err := dbNow(db)
	if err != nil {
		return err
	}
	m := model.MasterMember{}
	if err := db.Where("name = ?", name).First(&m).Error; err != nil {
		return common.NewError("no master named ", name)
	}
	if now-m.LastSeen <= masterAliveSeconds {
		return common.NewError("master ", name, " is still running; stop it first")
	}
	return db.Delete(&m).Error
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
