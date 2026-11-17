package service

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// Cascade modes: split lets the entry country's sites out of the entry itself,
// relay sends everything to the exit, both relays some inbounds and splits the rest.
const (
	CascadeModeSplit = "split"
	CascadeModeRelay = "relay"
	CascadeModeBoth  = "both"
)

var cascadeModes = []string{CascadeModeSplit, CascadeModeRelay, CascadeModeBoth}

func cascadeDirectTag(id int) string { return cascadePrefix(id) + "direct" }

// cascadeMode reads a stored cascade; rows saved before modes existed relayed everything.
func cascadeMode(c *model.Cascade) string {
	if c.Mode == "" {
		return CascadeModeRelay
	}
	return c.Mode
}

// cascadeZone is the routing template a split cascade lets out of its entry.
type cascadeZone struct {
	Domains []string
	IPs     []string
}

// splitZone is the entry country's server IPs (Russia when unset) plus its curated
// geosite list; no TLDs, since a .ru site may be hosted abroad (bug 23).
func splitZone(country string) cascadeZone {
	cc := strings.ToLower(strings.TrimSpace(country))
	if cc == "" {
		cc = "ru"
	}
	zone := cascadeZone{IPs: []string{"geoip:" + cc}}
	if cc == "ru" {
		zone.Domains = []string{"geosite:category-ru"}
	}
	return zone
}

// splitDomainStrategy makes the entry resolve a sniffed domain when it meets an
// IP rule; under AsIs or IPIfNonMatch the catch-all wins before geoip is tried.
const splitDomainStrategy = "IPOnDemand"

func nodeCountry(nodeID int) string {
	if nodeID == 0 {
		return ""
	}
	n, err := (&NodeService{}).GetById(nodeID)
	if err != nil {
		return ""
	}
	return n.Country
}

var plainDomain = regexp.MustCompile(`^[\p{L}\p{N}.*_-]+$`)

// validDomainRule accepts what an Xray routing "domain" entry can hold.
func validDomainRule(d string) bool {
	prefix, rest, found := strings.Cut(d, ":")
	if !found {
		return plainDomain.MatchString(d)
	}
	switch prefix {
	case "regexp":
		_, err := regexp.Compile(rest)
		return rest != "" && err == nil
	case "domain", "full", "keyword", "dotless", "geosite", "ext":
		return rest != "" && !strings.ContainsAny(rest, " \t")
	}
	return false
}

func validIPRule(ip string) bool {
	if prefix, rest, ok := strings.Cut(ip, ":"); ok && (prefix == "geoip" || prefix == "ext") {
		return rest != "" && !strings.ContainsAny(rest, " \t")
	}
	if net.ParseIP(ip) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(ip)
	return err == nil
}

func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

// normalizeCascadeRouting defaults a new cascade to split and rejects entries
// that would make Xray refuse the entry's whole config.
func normalizeCascadeRouting(req *CascadeRequest) error {
	if req.Mode == "" {
		req.Mode = CascadeModeSplit
	}
	if !slices.Contains(cascadeModes, req.Mode) {
		return common.NewError("unknown cascade mode " + req.Mode)
	}
	req.DirectDomains = cleanList(req.DirectDomains)
	req.DirectIPs = cleanList(req.DirectIPs)
	req.EntryInboundTags = cleanList(req.EntryInboundTags)
	req.RelayInboundTags = cleanList(req.RelayInboundTags)
	for _, d := range req.DirectDomains {
		if !validDomainRule(d) {
			return common.NewError("not a domain rule: " + d)
		}
	}
	for _, ip := range req.DirectIPs {
		if !validIPRule(ip) {
			return common.NewError("not an IP, CIDR or geoip: entry: " + ip)
		}
	}
	if req.Mode != CascadeModeBoth {
		req.RelayInboundTags = nil
		return nil
	}
	if len(req.RelayInboundTags) == 0 {
		return common.NewError("pick the entry inbounds that only relay")
	}
	for _, tag := range req.RelayInboundTags {
		if slices.Contains(req.EntryInboundTags, tag) {
			return common.NewError(fmt.Sprintf("inbound %s is listed both for relay and for split", tag))
		}
	}
	return nil
}

// suggestedHost is a bare ASCII or punycode host with a dot, unlike the looser
// plainDomain an admin may write.
var suggestedHost = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
