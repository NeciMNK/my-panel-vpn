package service

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
)

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// A node answers for another node by actually connecting to it: the first port
// that accepts wins, and refused ports alone mean the peer is unreachable.
func TestProbePeerConnectsToThePeer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	open := l.Addr().(*net.TCPAddr).Port
	closed := closedPort(t)

	cases := []struct {
		name      string
		ports     []int
		reachable bool
		port      int
	}{
		{"open port behind a closed one", []int{closed, open}, true, open},
		{"only closed ports", []int{closed}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProbePeer(context.Background(), entity.PeerProbeRequest{Address: "127.0.0.1", Ports: tc.ports})
			if err != nil {
				t.Fatalf("ProbePeer: %v", err)
			}
			if got.Reachable != tc.reachable || got.Port != tc.port {
				t.Fatalf("ProbePeer = %+v, want reachable=%v port=%d", got, tc.reachable, tc.port)
			}
			if !got.Reachable && got.Error == "" {
				t.Fatal("unreachable peer came back without the dial error")
			}
		})
	}
}

func TestProbePeerRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name string
		req  entity.PeerProbeRequest
		want error
	}{
		{"no address", entity.PeerProbeRequest{Ports: []int{443}}, errPeerProbeAddress},
		{"no ports", entity.PeerProbeRequest{Address: "de.example.com"}, errPeerProbePorts},
		{"port out of range", entity.PeerProbeRequest{Address: "de.example.com", Ports: []int{70000}}, errPeerProbePorts},
		{"too many ports", entity.PeerProbeRequest{Address: "de.example.com", Ports: []int{1, 2, 3, 4, 5}}, errPeerProbePorts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ProbePeer(context.Background(), tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("ProbePeer error = %v, want %v", err, tc.want)
			}
		})
	}
}
