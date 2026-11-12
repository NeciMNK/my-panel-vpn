package outbound

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/testpg"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Each HA master adds its own Xray's outbound deltas to the shared rows; a
// read-then-save add lets one master overwrite the other's bytes.
func TestAddTrafficTwoMastersKeepsEveryDeltaPostgres(t *testing.T) {
	if os.Getenv("XUI_DB_TYPE") != "postgres" || os.Getenv("XUI_DB_DSN") == "" {
		t.Skip("set XUI_DB_TYPE=postgres and XUI_DB_DSN")
	}
	cleanup, err := testpg.IsolatePackage("outbound_two_masters")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if err := database.InitDB(""); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	const tagCount, rounds = 8, 60
	var forward []*xray.Traffic
	for i := range tagCount {
		forward = append(forward, &xray.Traffic{IsOutbound: true, Tag: fmt.Sprintf("out-%d", i), Up: 1, Down: 2})
	}
	backward := slices.Clone(forward)
	slices.Reverse(backward)

	svc := &OutboundService{}
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, report := range [][]*xray.Traffic{forward, backward} {
		wg.Go(func() {
			for range rounds {
				if err, _ := svc.AddTraffic(report, nil); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a master lost an outbound tick: %v", err)
	}

	var rows []model.OutboundTraffics
	if err := database.GetDB().Order("tag").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != tagCount {
		t.Fatalf("got %d outbound rows, want %d", len(rows), tagCount)
	}
	for _, row := range rows {
		if row.Up != 2*rounds || row.Down != 4*rounds || row.Total != 6*rounds {
			t.Errorf("%s: up=%d down=%d total=%d, want %d/%d/%d", row.Tag, row.Up, row.Down, row.Total, 2*rounds, 4*rounds, 6*rounds)
		}
	}
}
