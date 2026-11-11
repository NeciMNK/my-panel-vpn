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

// Country codes whose top-level domain is not the code itself.
var countryTLD = map[string]string{"gb": "uk"}

// splitZone builds the zone of the entry's country (Russia when unset): its
// TLDs and geoip, plus the curated geosite list where geosite.dat has one.
func splitZone(country string) cascadeZone {
	cc := strings.ToLower(strings.TrimSpace(country))
	if cc == "" {
		cc = "ru"
	}
	if cc == "ru" {
		return cascadeZone{
			Domains: []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru"},
			IPs:     []string{"geoip:ru"},
		}
	}
	tld := cc
	if t, ok := countryTLD[cc]; ok {
		tld = t
	}
	return cascadeZone{Domains: []string{"domain:" + tld}, IPs: []string{"geoip:" + cc}}
}

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
