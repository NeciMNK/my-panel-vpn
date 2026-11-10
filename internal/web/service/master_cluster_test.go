package service

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestElectLeader(t *testing.T) {
	const now = int64(10_000)
	settled := now - masterSettleSeconds
	up := func(name string, priority int, upSince int64) model.MasterMember {
		return model.MasterMember{Name: name, Priority: priority, UpSince: upSince, LastSeen: now}
	}
	cases := []struct {
		name    string
		members []model.MasterMember
		want    string
	}{
		{"the primary leads", []model.MasterMember{up("b", 2, settled), up("a", 1, settled)}, "a"},
		{
			"a primary that stopped beating loses the lead",
			[]model.MasterMember{{Name: "a", Priority: 1, UpSince: settled, LastSeen: now - masterAliveSeconds - 1}, up("b", 2, settled)},
			"b",
		},
		{"a primary back for less than the settle time waits", []model.MasterMember{up("a", 1, now-5), up("b", 2, settled)}, "b"},
		{"on a cold start the primary leads at once", []model.MasterMember{up("a", 1, now), up("b", 2, now)}, "a"},
		{"no master alive means no leader", []model.MasterMember{{Name: "a", Priority: 1, LastSeen: now - 60}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := electLeader(tc.members, now); got != tc.want {
				t.Fatalf("leader = %q, want %q", got, tc.want)
			}
		})
	}
}

// A master claims the lead only on its second winning beat and steps down
// on the first beat that sees a better settled master.
func TestBeatClaimsAfterTwoBeatsAndStepsDownAtOnce(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Setenv("XUI_MASTER_NAME", "b")
	t.Setenv("XUI_MASTER_PRIORITY", "2")
	InitClusterRole()
	t.Cleanup(func() { clusterLeader.Store(true) })
	s := &MasterClusterService{}
	// b ran long ago: coming back must restart its settle time.
	if err := database.GetDB().Create(&model.MasterMember{Name: "b", Priority: 2, UpSince: 1, LastSeen: 1}).Error; err != nil {
		t.Fatal(err)
	}

	if leader, flipped, err := s.Beat(); err != nil || leader != "b" || flipped || IsClusterLeader() {
		t.Fatalf("first beat: leader %q flipped %v leads %v err %v", leader, flipped, IsClusterLeader(), err)
	}
	var self model.MasterMember
	database.GetDB().Where("name = ?", "b").First(&self)
	if self.UpSince != self.LastSeen {
		t.Fatalf("returning master kept up_since %d, want %d", self.UpSince, self.LastSeen)
	}
	if _, flipped, err := s.Beat(); err != nil || !flipped || !IsClusterLeader() {
		t.Fatalf("second beat must claim the lead: flipped %v leads %v err %v", flipped, IsClusterLeader(), err)
	}
	if leads, err := ReadClusterRole(time.Now().Unix()); err != nil || !leads {
		t.Fatalf("role file: leads %v err %v", leads, err)
	}

	now, err := dbNow(database.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	primary := model.MasterMember{Name: "a", Priority: 1, UpSince: now - masterSettleSeconds, LastSeen: now}
	if err := database.GetDB().Create(&primary).Error; err != nil {
		t.Fatal(err)
	}
	if leader, flipped, err := s.Beat(); err != nil || leader != "a" || !flipped || IsClusterLeader() {
		t.Fatalf("back primary: leader %q flipped %v leads %v err %v", leader, flipped, IsClusterLeader(), err)
	}
}

func TestReadClusterRoleRefusesAStaleFile(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	if err := os.WriteFile(config.GetClusterRolePath(), []byte("leader 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if leads, err := ReadClusterRole(1000 + 3*MasterBeatSeconds); err != nil || !leads {
		t.Fatalf("fresh file: leads %v err %v", leads, err)
	}
	want := "the panel has not written its role for " + strconv.Itoa(3*MasterBeatSeconds+1) + "s"
	if leads, err := ReadClusterRole(1000 + 3*MasterBeatSeconds + 1); leads || err == nil || err.Error() != want {
		t.Fatalf("stale file: leads %v err %v", leads, err)
	}
}
