package service

import (
	"testing"
	"time"
)

// A node alert needs the load over the WHOLE window: a node seen for 30 s must
// not count as "busy for two minutes", and a stale series must not alert at all.
func TestNodeLoadSpan(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	window := 2 * time.Minute
	feed := func(id int, from, to time.Duration, v func(i int) float64) {
		t.Cleanup(func() { nodeMetrics.drop(nodeMetricKey(id, "cpu")) })
		for i, at := 0, from; at <= to; i, at = i+1, at+5*time.Second {
			nodeMetrics.append(nodeMetricKey(id, "cpu"), now.Add(-at), v(i))
		}
	}
	feed(9001, 0, 3*time.Minute, func(i int) float64 { return float64(90 + i%3) })
	feed(9002, 0, 30*time.Second, func(int) float64 { return 95 })
	feed(9003, 90*time.Second, 5*time.Minute, func(int) float64 { return 95 })
	feed(9004, 0, 3*time.Minute, func(i int) float64 {
		if i == 7 {
			return 40
		}
		return 95
	})

	tests := []struct {
		name      string
		id        int
		low, high float64
		ok        bool
	}{
		{"covered window", 9001, 90, 92, true},
		{"node seen too briefly", 9002, 0, 0, false},
		{"samples stopped coming", 9003, 0, 0, false},
		{"one dip inside the window", 9004, 40, 95, true},
		{"no series", 9005, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			low, high, ok := (&NodeService{}).NodeLoadSpan(tc.id, "cpu", window, now)
			if ok != tc.ok || low != tc.low || high != tc.high {
				t.Fatalf("NodeLoadSpan = (%v, %v, %v), want (%v, %v, %v)", low, high, ok, tc.low, tc.high, tc.ok)
			}
		})
	}
}
