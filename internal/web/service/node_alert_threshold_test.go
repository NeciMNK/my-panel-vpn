package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A node's own CPU/RAM alert thresholds survive create and edit, and a value
// outside 0..100 is refused rather than silently never alerting.
func TestNodeAlertThresholdsAreSavedAndChecked(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	s := &NodeService{}
	n := &model.Node{Name: "hot", Address: "5.5.5.5", Port: 2053, ApiToken: "tok", AllowPrivateAddress: true, AlertCpu: 90}
	if err := s.Create(n); err != nil {
		t.Fatal(err)
	}
	edit := *n
	edit.AlertCpu, edit.AlertMemory = 70, 85
	if err := s.Update(n.Id, &edit); err != nil {
		t.Fatal(err)
	}
	var got model.Node
	if err := database.GetDB().First(&got, n.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.AlertCpu != 70 || got.AlertMemory != 85 {
		t.Fatalf("saved thresholds = %d/%d, want 70/85", got.AlertCpu, got.AlertMemory)
	}
	edit.AlertMemory = 120
	if err := s.Update(n.Id, &edit); err == nil {
		t.Fatal("a 120% RAM threshold was accepted")
	}
}
