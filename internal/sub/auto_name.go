package sub

import (
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// autoNameProtocols prefixes a link's auto name; VLESS is the default protocol
// and goes unnamed, as in "Pure NL" next to "Hysteria Pure NL".
var autoNameProtocols = map[model.Protocol]string{
	model.VLESS: "", model.VMESS: "VMess", model.Trojan: "Trojan", model.Shadowsocks: "Shadowsocks",
	model.Hysteria: "Hysteria", model.TUIC: "TUIC", model.WireGuard: "WireGuard",
	model.AmneziaWG: "AmneziaWG", model.MTProto: "MTProto", model.MASQUE: "MASQUE",
	model.HTTP: "HTTP", model.Mixed: "Mixed", model.Tunnel: "Tunnel",
}

var autoNameStrategies = map[string]string{
	"leastPing": "Best Ping", "leastLoad": "Least Load", "random": "Random", "roundRobin": "Round Robin",
}

// loadCascades refreshes the enabled cascades once per request, in id order so
// an inbound entering several is named after the first, like its routing.
func (s *SubService) loadCascades() {
	s.cascades = nil
	db := database.GetDB()
	if db == nil {
		return
	}
	if err := db.Where("enable = ?", true).Order("id asc").Find(&s.cascades).Error; err != nil {
		logger.Warning("subscription: load cascades failed:", err)
	}
}

func (s *SubService) autoNameCountry(nodeID int) string {
	if n, ok := s.nodesByID[nodeID]; ok {
		return strings.ToUpper(strings.TrimSpace(n.Country))
	}
	return ""
}

// autoServerName names an inbound's links by where traffic enters and leaves:
// "🇳🇱 Pure NL", "🇷🇺 / 🇳🇱 Split RU / NL", "🇷🇺 / ⚡ Split Auto (Best Ping)".
// Without a country on every server involved it falls back to the inbound remark.
func (s *SubService) autoServerName(inbound *model.Inbound) string {
	if inbound == nil {
		return ""
	}
	entryID := 0
	if inbound.NodeID != nil {
		entryID = *inbound.NodeID
	}
	entry := s.autoNameCountry(entryID)
	if entry == "" {
		return inbound.Remark
	}
	proto, known := autoNameProtocols[inbound.Protocol]
	if !known {
		proto = strings.ToUpper(string(inbound.Protocol))
	}
	withProto := func(name string) string {
		if proto == "" {
			return name
		}
		return proto + " " + name
	}

	var cascade *model.Cascade
	route := ""
	for i := range s.cascades {
		if route = service.CascadeRoute(&s.cascades[i], inbound); route != "" {
			cascade = &s.cascades[i]
			break
		}
	}
	if cascade == nil {
		name := countryFlag(entry) + " " + withProto("Pure "+entry)
		if entry == "RU" {
			name += " (Direct Russia)"
		}
		return name
	}

	var exits []string
	for _, id := range cascade.ExitNodeIds {
		cc := s.autoNameCountry(id)
		if cc == "" {
			return inbound.Remark
		}
		if !slices.Contains(exits, cc) {
			exits = append(exits, cc)
		}
	}
	if len(exits) == 0 {
		return inbound.Remark
	}
	verb := "Split"
	if route == service.CascadeModeRelay {
		verb = "Relay"
	}
	if len(exits) > 1 {
		strategy := autoNameStrategies[cascade.Strategy]
		if strategy == "" {
			strategy = autoNameStrategies["leastPing"]
		}
		return countryFlag(entry) + " / ⚡ " + withProto(verb+" Auto ("+strategy+")")
	}
	exit := exits[0]
	if proto == "" {
		return countryFlag(entry) + " / " + countryFlag(exit) + " " + verb + " " + entry + " / " + exit
	}
	return countryFlag(entry) + " / " + countryFlag(exit) + " " + withProto(verb+" ("+exit+")")
}

// countryFlag turns a two-letter country code into its regional-indicator flag.
func countryFlag(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	var b strings.Builder
	for _, r := range cc {
		if r < 'A' || r > 'Z' {
			return ""
		}
		b.WriteRune(0x1F1E6 + r - 'A')
	}
	return b.String()
}

// clientAutoName is autoServerName for one client's link. A second link of the
// same name on another transport gets it appended, as in "Pure NL (XHTTP)".
func (s *SubService) clientAutoName(inbound *model.Inbound, client model.Client, transport string) string {
	name := s.autoServerName(inbound)
	if name == "" || transport == "" {
		return name
	}
	if s.autoNameTransports == nil {
		s.autoNameTransports = map[string]string{}
	}
	key := templateInfoKey(client) + "\x00" + name
	first, seen := s.autoNameTransports[key]
	if !seen {
		s.autoNameTransports[key] = transport
		return name
	}
	if first == transport {
		return name
	}
	return name + " (" + strings.ToUpper(transport) + ")"
}
