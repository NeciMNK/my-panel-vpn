package service

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

const (
	peerProbeMaxPorts    = 4
	peerProbeDialTimeout = 3 * time.Second
)

var (
	errPeerProbeAddress = errors.New("peer probe: address is required")
	errPeerProbePorts   = errors.New("peer probe: 1 to 4 ports in 1-65535 are required")
)

// ProbePeer checks from this panel whether another node accepts TCP on any of
// the ports. It only connects and hangs up: nothing is sent to the peer.
func ProbePeer(ctx context.Context, req entity.PeerProbeRequest) (entity.PeerProbeResult, error) {
	if req.Address == "" || len(req.Address) > 253 {
		return entity.PeerProbeResult{}, errPeerProbeAddress
	}
	if len(req.Ports) == 0 || len(req.Ports) > peerProbeMaxPorts {
		return entity.PeerProbeResult{}, errPeerProbePorts
	}
	for _, p := range req.Ports {
		if p < 1 || p > 65535 {
			return entity.PeerProbeResult{}, errPeerProbePorts
		}
	}

	ctx, cancel := context.WithTimeout(ctx, peerProbeDialTimeout)
	defer cancel()
	type dial struct {
		port    int
		latency time.Duration
		err     error
	}
	results := make(chan dial, len(req.Ports))
	for _, port := range req.Ports {
		go func() {
			start := time.Now()
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(req.Address, strconv.Itoa(port)))
			if err == nil {
				_ = conn.Close()
			}
			results <- dial{port: port, latency: time.Since(start), err: err}
		}()
	}
	var lastErr error
	for range req.Ports {
		r := <-results
		if r.err == nil {
			return entity.PeerProbeResult{Reachable: true, Port: r.port, LatencyMs: int(r.latency.Milliseconds())}, nil
		}
		lastErr = r.err
	}
	return entity.PeerProbeResult{Error: lastErr.Error()}, nil
}

const (
	maxNodeWitnesses       = 3
	maxWitnessInboundPorts = peerProbeMaxPorts - 1
)

// PickWitnesses chooses the nodes asked about a lost node, one per country
// first, so a single country's outage does not decide the verdict.
func PickWitnesses(candidates []*model.Node) []*model.Node {
	sorted := slices.Clone(candidates)
	slices.SortFunc(sorted, func(a, b *model.Node) int { return a.Id - b.Id })
	picked := make([]*model.Node, 0, maxNodeWitnesses)
	seenCountry := map[string]bool{}
	for _, n := range sorted {
		if len(picked) < maxNodeWitnesses && !seenCountry[n.Country] {
			seenCountry[n.Country] = true
			picked = append(picked, n)
		}
	}
	for _, n := range sorted {
		if len(picked) < maxNodeWitnesses && !slices.Contains(picked, n) {
			picked = append(picked, n)
		}
	}
	return picked
}

// AskWitnesses returns the names of the witnesses that still reach target. A
// witness that cannot answer (old build, unreachable) counts as not seeing it.
func (s *NodeService) AskWitnesses(ctx context.Context, target *model.Node, witnesses []*model.Node) []string {
	mgr := runtime.GetManager()
	if mgr == nil || len(witnesses) == 0 {
		return nil
	}
	req := entity.PeerProbeRequest{Address: target.Address, Ports: witnessPorts(target)}
	seen := make([]bool, len(witnesses))
	var wg sync.WaitGroup
	for i, w := range witnesses {
		wg.Add(1)
		common.GoRecover("node-witness:"+w.Name, func() {
			defer wg.Done()
			remote, err := mgr.RemoteFor(w)
			if err != nil {
				return
			}
			res, err := remote.ProbePeer(ctx, req)
			if err != nil {
				logger.Debug("node witness", w.Name, "could not check", target.Name, ":", err)
				return
			}
			seen[i] = res.Reachable
		})
	}
	wg.Wait()
	var names []string
	for i, w := range witnesses {
		if seen[i] {
			names = append(names, w.Name)
		}
	}
	return names
}

// witnessPorts is the node's panel port plus the first ports its users connect to.
func witnessPorts(n *model.Node) []int {
	ports := []int{n.Port}
	var inboundPorts []int
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("node_id = ? AND enable = ?", n.Id, true).
		Order("id ASC").Pluck("port", &inboundPorts).Error; err != nil {
		logger.Warning("node witness: list inbound ports of", n.Name, ":", err)
	}
	for _, p := range inboundPorts {
		if len(ports) > maxWitnessInboundPorts {
			break
		}
		if p > 0 && !slices.Contains(ports, p) {
			ports = append(ports, p)
		}
	}
	return ports
}
