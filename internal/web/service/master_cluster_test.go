package service

import (
	"errors"
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

// clusterOf seeds a cluster seen from master self, which leads; every row
// alive and settled unless listed in down.
func clusterOf(t *testing.T, self string, names []string, down ...string) *MasterClusterService {
	t.Helper()
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Setenv("XUI_MASTER_NAME", self)
	t.Setenv("XUI_MASTER_PRIORITY", "1")
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", "postgres://xui:secret@10.0.0.1:5432,10.0.0.2:5432/xui")
	clusterLeader.Store(true)
	t.Cleanup(func() { clusterLeader.Store(true) })
	now, err := dbNow(database.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		m := model.MasterMember{Name: name, Priority: i + 1, UpSince: now - 3600, LastSeen: now}
		for _, d := range down {
			if d == name {
				m.LastSeen = now - masterAliveSeconds - 1
			}
		}
		if err := database.GetDB().Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &MasterClusterService{}
}

func priorities(t *testing.T) map[string]int {
	t.Helper()
	var members []model.MasterMember
	if err := database.GetDB().Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, m := range members {
		out[m.Name] = m.Priority
	}
	return out
}

func wantRefusal(t *testing.T, err error, reason, master string) {
	t.Helper()
	var refusal *MasterChangeError
	if !errors.As(err, &refusal) || refusal.Reason != reason || refusal.Master != master {
		t.Fatalf("err = %v, want refusal %s for %q", err, reason, master)
	}
}

func TestMasterChangesNeedEveryMasterOnline(t *testing.T) {
	s := clusterOf(t, "a", []string{"a", "b", "c"}, "c")
	wantRefusal(t, s.Reorder([]string{"b", "a", "c"}), MasterChangeOffline, "c")
	_, err := s.Add("d", "https://d:2053")
	wantRefusal(t, err, MasterChangeOffline, "c")
	if got := priorities(t); got["b"] != 2 || len(got) != 3 {
		t.Fatalf("a refused change still wrote: %v", got)
	}
}

func TestForgetNeedsEveryOtherMasterOnline(t *testing.T) {
	s := clusterOf(t, "a", []string{"a", "b", "c"}, "b", "c")
	wantRefusal(t, s.Forget("b"), MasterChangeOffline, "c")
	database.GetDB().Where("name = ?", "c").Delete(&model.MasterMember{})
	if err := s.Forget("b"); err != nil {
		t.Fatal(err)
	}
	if got := priorities(t); len(got) != 1 {
		t.Fatalf("masters left = %v, want only a", got)
	}
}

func TestMasterChangesOnlyOnThePrimary(t *testing.T) {
	s := clusterOf(t, "b", []string{"a", "b"})
	wantRefusal(t, s.Reorder([]string{"b", "a"}), MasterChangeNotPrimary, "a")
	clusterLeader.Store(false)
	t.Setenv("XUI_MASTER_NAME", "a")
	wantRefusal(t, s.Reorder([]string{"b", "a"}), MasterChangeNotPrimary, "a")
}

func TestReorderHandsTheLeadToTheNewPrimary(t *testing.T) {
	s := clusterOf(t, "a", []string{"a", "b", "c"})
	wantRefusal(t, s.Forget("b"), MasterChangeRunning, "b")
	wantRefusal(t, s.Reorder([]string{"b", "a"}), MasterChangeBadOrder, "")
	wantRefusal(t, s.Reorder([]string{"b", "a", "a"}), MasterChangeBadOrder, "")
	if err := s.Reorder([]string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := priorities(t); got["c"] != 1 || got["a"] != 2 || got["b"] != 3 {
		t.Fatalf("priorities = %v", got)
	}
	s.elected = 2
	if leader, flipped, err := s.Beat(); err != nil || leader != "c" || !flipped || IsClusterLeader() {
		t.Fatalf("old primary after reorder: leader %q flipped %v leads %v err %v", leader, flipped, IsClusterLeader(), err)
	}
	if got := priorities(t); got["a"] != 2 {
		t.Fatalf("the beat put back the env priority: %v", got)
	}
}

func TestAddedMasterWaitsAndKeepsItsPriorityWhenItJoins(t *testing.T) {
	s := clusterOf(t, "a", []string{"a", "b"})
	_, err := s.Add("b", "")
	wantRefusal(t, err, MasterChangeNameTaken, "b")
	setup, err := s.Add(" c ", "https://c:2053")
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := "XUI_DB_TYPE=postgres\nXUI_DB_DSN=postgres://xui:secret@10.0.0.1:5432,10.0.0.2:5432/xui\n" +
		"XUI_MASTER_PRIORITY=3\nXUI_MASTER_NAME=c\nXUI_MASTER_URL=https://c:2053\n"
	if setup.Env != wantEnv {
		t.Fatalf("env =\n%s\nwant\n%s", setup.Env, wantEnv)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if last := list[2]; last.Name != "c" || !last.Pending || last.Alive {
		t.Fatalf("added master listed as %+v, want pending", last)
	}
	wantRefusal(t, s.Reorder([]string{"b", "a", "c"}), MasterChangeOffline, "c")

	t.Setenv("XUI_MASTER_URL", "")
	if _, err := s.beat("c", 1); err != nil {
		t.Fatal(err)
	}
	var c model.MasterMember
	database.GetDB().Where("name = ?", "c").First(&c)
	if c.Priority != 3 || c.Url != "https://c:2053" {
		t.Fatalf("joined master: priority %d url %q, want 3 and the added url", c.Priority, c.Url)
	}
}

// A master the list forgot must not come back as primary from its old env.
func TestUnknownMasterWithATakenPriorityJoinsLast(t *testing.T) {
	s := clusterOf(t, "a", []string{"a", "b"})
	if _, err := s.beat("old", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.beat("new", 7); err != nil {
		t.Fatal(err)
	}
	if got := priorities(t); got["old"] != 3 || got["new"] != 7 {
		t.Fatalf("priorities = %v, want old last (3) and new keeping its free 7", got)
	}
}
