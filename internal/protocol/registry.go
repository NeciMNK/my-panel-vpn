// Package protocol is the single registry of inbound protocols: what serves each
// one and the facts the rest of the panel used to repeat in its own switch.
package protocol

import "github.com/mhsanaei/3x-ui/v3/internal/database/model"

// Runtime names what actually serves a protocol's inbounds on a host.
type Runtime uint8

const (
	// RuntimeXray inbounds are handlers inside the managed xray-core process.
	RuntimeXray Runtime = iota
	// RuntimeSidecar inbounds run in their own managed child process (mtg-multi, tuic-server).
	RuntimeSidecar
	// RuntimeEmbedded inbounds run inside the panel process itself (AmneziaWG).
	RuntimeEmbedded
)

// Transport is a set of L4 transports an inbound's listener binds.
type Transport uint8

const (
	TCP Transport = 1 << iota
	UDP
)

// Spec describes one inbound protocol.
type Spec struct {
	ID      model.Protocol
	Runtime Runtime
	// UsesStreamSettings: the inbound keeps streamSettings; for others the panel clears it.
	UsesStreamSettings bool
	// ListenTransport is fixed by the protocol; zero means streamSettings/settings decide.
	ListenTransport Transport
	// NodeEligible inbounds may be assigned to and pushed to a node panel.
	NodeEligible bool
	// NodeMinPanelVersion is the oldest node panel that serves this protocol; "" = any.
	NodeMinPanelVersion string
}

// ServedByXray reports whether the protocol's inbounds live inside xray-core.
func (s Spec) ServedByXray() bool { return s.Runtime == RuntimeXray }

var specs = []Spec{
	{ID: model.VMESS, UsesStreamSettings: true, NodeEligible: true},
	{ID: model.VLESS, UsesStreamSettings: true, NodeEligible: true},
	{ID: model.Trojan, UsesStreamSettings: true, NodeEligible: true},
	{ID: model.Shadowsocks, UsesStreamSettings: true, NodeEligible: true},
	{ID: model.Hysteria, UsesStreamSettings: true, ListenTransport: UDP, NodeEligible: true},
	{ID: model.WireGuard, UsesStreamSettings: true, ListenTransport: UDP, NodeEligible: true},
	{ID: model.MASQUE, UsesStreamSettings: true, NodeEligible: true, NodeMinPanelVersion: "v3.9.1"},
	{ID: model.Tunnel, UsesStreamSettings: true},
	{ID: model.HTTP},
	{ID: model.Mixed},
	{ID: model.TUN},
	{
		ID: model.MTProto, Runtime: RuntimeSidecar, ListenTransport: TCP,
		NodeEligible: true, NodeMinPanelVersion: "v3.5.0",
	},
	{
		ID: model.TUIC, Runtime: RuntimeSidecar, ListenTransport: UDP,
		NodeEligible: true, NodeMinPanelVersion: "v3.8.0",
	},
	{
		ID: model.AmneziaWG, Runtime: RuntimeEmbedded, ListenTransport: UDP,
		NodeEligible: true, NodeMinPanelVersion: "v3.7.0",
	},
}

var byID = func() map[model.Protocol]Spec {
	m := make(map[model.Protocol]Spec, len(specs))
	for _, s := range specs {
		m[s.ID] = s
	}
	return m
}()

// All returns every registered protocol in registration order.
func All() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Lookup returns the Spec for id.
func Lookup(id model.Protocol) (Spec, bool) {
	s, ok := byID[id]
	return s, ok
}

// ServedByXray reports whether id's inbounds belong in the xray-core config; an
// unregistered id is treated as Xray's, which is where it went before the registry.
func ServedByXray(id model.Protocol) bool {
	s, ok := byID[id]
	return !ok || s.ServedByXray()
}
