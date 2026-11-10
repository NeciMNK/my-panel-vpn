package job

import (
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// A standby master must not run node sync or traffic resets a second time.
func TestLeaderOnlySkipsOnAStandbyMaster(t *testing.T) {
	ran := 0
	j := LeaderOnly(cron.FuncJob(func() { ran++ }))
	t.Setenv("XUI_MASTER_PRIORITY", "2")
	service.InitClusterRole()
	j.Run()
	t.Setenv("XUI_MASTER_PRIORITY", "")
	service.InitClusterRole()
	j.Run()
	if ran != 1 {
		t.Fatalf("job ran %d times, want once (only as a single master)", ran)
	}
}
