package job

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/websocket"
)

const (
	nodeHeartbeatConcurrency    = 32
	nodeHeartbeatRequestTimeout = 4 * time.Second
	nodeWitnessTimeout          = 6 * time.Second
	nodeWitnessRecheck          = 30 * time.Second
	// Past this many same-direction transitions in one tick, one summary event goes out:
	// per-node events overflow the notifier queues and every chat's rate limit.
	nodeTransitionBurst      = 5
	nodeTransitionBurstNames = 10
)

type NodeHeartbeatJob struct {
	nodeService    service.NodeService
	settingService service.SettingService
	running        sync.Mutex
	health         map[int]nodeHealth
	witnessed      map[int]witnessCheck
	// witnessRecheck spaces the asks about a node the master keeps failing to reach.
	witnessRecheck time.Duration
}

type nodeHealth struct {
	failStreak int
	notified   bool
	// announcedIsolated: the last node.down said other nodes still reached it.
	announcedIsolated bool
}

type witnessCheck struct {
	at     time.Time
	seenBy []string
}

type nodeProbe struct {
	node  *model.Node
	patch service.HeartbeatPatch
}

func NewNodeHeartbeatJob() *NodeHeartbeatJob {
	return &NodeHeartbeatJob{witnessRecheck: nodeWitnessRecheck}
}

func (j *NodeHeartbeatJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()

	nodes, err := j.nodeService.GetAll()
	if err != nil {
		logger.Warning("node heartbeat: load nodes failed:", err)
		return
	}
	j.nodeService.RetainEnabledNodeDescendants(nodes)
	if len(nodes) == 0 {
		return
	}

	var enabled []*model.Node
	for _, n := range nodes {
		if n.Enable {
			enabled = append(enabled, n)
		}
	}
	probes := make([]nodeProbe, len(enabled))
	fanOutNodes(enabled, "node-heartbeat:", func(i int, n *model.Node) {
		probes[i] = nodeProbe{node: n, patch: j.probeOne(n)}
	})
	j.resolveUnreachable(probes)
	fanOutNodes(enabled, "node-heartbeat-save:", func(i int, n *model.Node) {
		j.record(probes[i])
	})
	publishNodeTransitions(j.healthTransitions(probes))

	if !websocket.HasClients() {
		return
	}
	updated, err := j.nodeService.GetNodeTreeView()
	if err != nil {
		logger.Warning("node heartbeat: load nodes for broadcast failed:", err)
		return
	}
	websocket.BroadcastNodes(updated)
}

func fanOutNodes(nodes []*model.Node, label string, fn func(i int, n *model.Node)) {
	sem := make(chan struct{}, nodeHeartbeatConcurrency)
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		common.GoRecover(label+n.Name, func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i, n)
		})
	}
	wg.Wait()
}

func (j *NodeHeartbeatJob) probeOne(n *model.Node) service.HeartbeatPatch {
	ctx, cancel := context.WithTimeout(context.Background(), nodeHeartbeatRequestTimeout)
	defer cancel()
	patch, err := j.nodeService.Probe(ctx, n)
	if err != nil {
		patch.Status = model.NodeStatusOffline
	} else {
		patch.Status = model.NodeStatusOnline
	}
	return patch
}

// resolveUnreachable asks the nodes the master still reaches about each node it
// lost: a node any of them reaches is isolated, not dead, and stays offered to users.
func (j *NodeHeartbeatJob) resolveUnreachable(probes []nodeProbe) {
	var reachable []*model.Node
	for _, p := range probes {
		if p.patch.Status == model.NodeStatusOnline {
			reachable = append(reachable, p.node)
		}
	}
	prev := j.witnessed
	j.witnessed = make(map[int]witnessCheck)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range probes {
		p := &probes[i]
		if p.patch.Status != model.NodeStatusOffline || len(reachable) == 0 {
			continue
		}
		if c, ok := prev[p.node.Id]; ok && time.Since(c.at) < j.witnessRecheck {
			j.witnessed[p.node.Id] = c
			applyWitnesses(p, c.seenBy)
			continue
		}
		wg.Add(1)
		common.GoRecover("node-witness:"+p.node.Name, func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), nodeWitnessTimeout)
			defer cancel()
			seenBy := j.nodeService.AskWitnesses(ctx, p.node, service.PickWitnesses(reachable))
			mu.Lock()
			j.witnessed[p.node.Id] = witnessCheck{at: time.Now(), seenBy: seenBy}
			mu.Unlock()
			applyWitnesses(p, seenBy)
		})
	}
	wg.Wait()
}

func applyWitnesses(p *nodeProbe, seenBy []string) {
	if len(seenBy) == 0 {
		return
	}
	p.patch.Status = model.NodeStatusIsolated
	p.patch.SeenBy = strings.Join(seenBy, ", ")
}

func (j *NodeHeartbeatJob) record(p nodeProbe) {
	n := p.node
	if updErr := j.nodeService.UpdateHeartbeat(n.Id, p.patch); updErr != nil {
		logger.Warning("node heartbeat: update node", n.Id, "failed:", updErr)
	}
	// Learn the nodes this node manages so the panel can surface them as
	// transitive sub-nodes (#4983). Drop them when the master cannot reach it.
	if p.patch.Status == model.NodeStatusOnline {
		dctx, dcancel := context.WithTimeout(context.Background(), nodeHeartbeatRequestTimeout)
		j.nodeService.RefreshDescendants(dctx, n)
		dcancel()
	} else {
		j.nodeService.ClearDescendants(n.Id)
	}
}

func (j *NodeHeartbeatJob) healthTransitions(probes []nodeProbe) []eventbus.Event {
	threshold := 1
	if v, err := j.settingService.GetNodeDownThreshold(); err == nil && v > 0 {
		threshold = v
	}
	prev := j.health
	j.health = make(map[int]nodeHealth, len(probes))
	var events []eventbus.Event
	for _, p := range probes {
		h, seen := prev[p.node.Id]
		if !seen {
			h.notified = p.node.DownNotified
			h.announcedIsolated = p.node.Status == model.NodeStatusIsolated
		}
		if p.patch.Status == model.NodeStatusOnline {
			if h.notified {
				events = append(events, nodeHealthEvent(eventbus.EventNodeUp, p.node, p.patch))
				j.saveDownNotified(p.node.Id, false)
			}
			h = nodeHealth{}
		} else {
			isolated := p.patch.Status == model.NodeStatusIsolated
			h.failStreak++
			// An isolated node that then dies for everyone (or back) is news again.
			if h.failStreak >= threshold && (!h.notified || h.announcedIsolated != isolated) {
				events = append(events, nodeHealthEvent(eventbus.EventNodeDown, p.node, p.patch))
				if !h.notified {
					j.saveDownNotified(p.node.Id, true)
				}
				h.notified, h.announcedIsolated = true, isolated
			}
		}
		j.health[p.node.Id] = h
	}
	return events
}

func (j *NodeHeartbeatJob) saveDownNotified(id int, notified bool) {
	if err := j.nodeService.SetDownNotified(id, notified); err != nil {
		logger.Warning("node heartbeat: save down-notified flag for node", id, "failed:", err)
	}
}

func nodeHealthEvent(eventType eventbus.EventType, n *model.Node, patch service.HeartbeatPatch) eventbus.Event {
	source := n.Name
	if source == "" {
		source = "node-" + strconv.Itoa(n.Id)
	}
	return eventbus.Event{
		Type:   eventType,
		Source: source,
		Data: &eventbus.NodeHealthData{
			NodeId:    n.Id,
			LatencyMs: patch.LatencyMs,
			CpuPct:    patch.CpuPct,
			MemPct:    patch.MemPct,
			XrayState: patch.XrayState,
			XrayError: patch.XrayError,
			SeenBy:    seenByNames(patch.SeenBy),
		},
	}
}

func seenByNames(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ", ")
}

// publishNodeTransitions sends one tick's transitions, folding a same-direction burst
// (a master-side blip flips every node at once) into one event naming the nodes.
func publishNodeTransitions(events []eventbus.Event) {
	if EventBus == nil {
		return
	}
	namesByType := make(map[eventbus.EventType][]string)
	for _, e := range events {
		namesByType[e.Type] = append(namesByType[e.Type], e.Source)
	}
	for _, e := range events {
		if len(namesByType[e.Type]) <= nodeTransitionBurst {
			EventBus.Publish(e)
		}
	}
	for _, eventType := range []eventbus.EventType{eventbus.EventNodeDown, eventbus.EventNodeUp} {
		names := namesByType[eventType]
		if len(names) <= nodeTransitionBurst {
			continue
		}
		sort.Strings(names)
		source := strings.Join(names[:min(len(names), nodeTransitionBurstNames)], ", ")
		if extra := len(names) - nodeTransitionBurstNames; extra > 0 {
			source += fmt.Sprintf(" (+%d)", extra)
		}
		EventBus.Publish(eventbus.Event{Type: eventType, Source: source})
	}
}
