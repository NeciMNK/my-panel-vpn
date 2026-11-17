package job

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func setGlobalMonthlyReset(t *testing.T, enable bool, day int) {
	t.Helper()
	var s service.SettingService
	if err := s.SetTrafficResetAllMonthly(enable, day); err != nil {
		t.Fatalf("set global monthly reset: %v", err)
	}
}

func TestGlobalMonthlyTrafficReset(t *testing.T) {
	today := time.Now().In(time.UTC).Day()
	otherDay := today%28 + 1

	t.Run("resets clients without a cycle of their own on the global day", func(t *testing.T) {
		initResetJobDB(t)
		setGlobalMonthlyReset(t, true, today)
		seedClientOnCycle(t, 42001, seededClient{email: "plain@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true})

		NewPeriodicTrafficResetJob("monthly", time.UTC).Run()

		if row := trafficFor(t, "plain@example.com"); row.Up != 0 || row.Down != 0 {
			t.Fatalf("client following the global rule was not reset: up=%d down=%d", row.Up, row.Down)
		}
	})

	t.Run("a client's own cycle wins over the global rule", func(t *testing.T) {
		initResetJobDB(t)
		setGlobalMonthlyReset(t, true, today)
		seedClientOnCycle(t, 42002, seededClient{email: "weekly@example.com", cycle: "weekly", day: 1, recordEnable: true, quotaEnable: true})
		seedClientOnCycle(t, 42003, seededClient{email: "ownday@example.com", cycle: "monthly", day: otherDay, recordEnable: true, quotaEnable: true})
		seedClientOnCycle(t, 42004, seededClient{email: "inweekly@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true, inboundCycle: "weekly"})
		seedClientOnCycle(t, 42005, seededClient{email: "renews@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true})
		if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "renews@example.com").
			Update("reset_day", otherDay).Error; err != nil {
			t.Fatalf("set renewal day: %v", err)
		}

		NewPeriodicTrafficResetJob("monthly", time.UTC).Run()

		for _, email := range []string{"weekly@example.com", "ownday@example.com", "inweekly@example.com", "renews@example.com"} {
			if row := trafficFor(t, email); row.Up != 500 || row.Down != 700 {
				t.Fatalf("%s has its own cycle but the global rule reset it: up=%d down=%d", email, row.Up, row.Down)
			}
		}
	})

	t.Run("waits for the global day", func(t *testing.T) {
		initResetJobDB(t)
		setGlobalMonthlyReset(t, true, otherDay)
		seedClientOnCycle(t, 42006, seededClient{email: "early@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true})

		NewPeriodicTrafficResetJob("monthly", time.UTC).Run()

		if row := trafficFor(t, "early@example.com"); row.Up != 500 || row.Down != 700 {
			t.Fatalf("reset before the global day: up=%d down=%d", row.Up, row.Down)
		}
	})

	t.Run("switched off leaves everyone alone", func(t *testing.T) {
		initResetJobDB(t)
		setGlobalMonthlyReset(t, false, today)
		seedClientOnCycle(t, 42007, seededClient{email: "off@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true})

		NewPeriodicTrafficResetJob("monthly", time.UTC).Run()

		if row := trafficFor(t, "off@example.com"); row.Up != 500 || row.Down != 700 {
			t.Fatalf("reset with the global rule switched off: up=%d down=%d", row.Up, row.Down)
		}
	})

	t.Run("only the monthly run applies the global rule", func(t *testing.T) {
		initResetJobDB(t)
		setGlobalMonthlyReset(t, true, today)
		seedClientOnCycle(t, 42008, seededClient{email: "daily@example.com", cycle: "never", day: 1, recordEnable: true, quotaEnable: true})

		NewPeriodicTrafficResetJob("daily", time.UTC).Run()

		if row := trafficFor(t, "daily@example.com"); row.Up != 500 || row.Down != 700 {
			t.Fatalf("daily run applied the monthly global rule: up=%d down=%d", row.Up, row.Down)
		}
	})
}
