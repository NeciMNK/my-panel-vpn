// Package shaper caps the speed of single client IPs with Linux tc: an HTB
// class per IP for downloads and an ingress policer per IP for uploads.
package shaper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Limit caps every connection from one client IP at Mbps (0 = no cap) and,
// when the plan knows the link speed, keeps MinMbps of it for that IP.
type Limit struct {
	IP      string `json:"ip"`
	Mbps    int    `json:"mbps"`
	MinMbps int    `json:"minMbps,omitempty"`
}

// Plan is the full set of limits one server must enforce; LinkMbps is the
// server's link speed, which guarantees are carved out of.
type Plan struct {
	Limits   []Limit `json:"limits"`
	LinkMbps int     `json:"linkMbps,omitempty"`
}

// Status is what a server reports after applying a plan.
type Status struct {
	Interface string `json:"interface" example:"eth0"`
	Limited   int    `json:"limited" example:"3"`
}

// Normalize validates IPs and rates, merges the users of one IP (strictest cap,
// biggest guarantee) and sorts, so equal plans render identical commands.
func (p *Plan) Normalize() error {
	if p.LinkMbps < 0 {
		return fmt.Errorf("link speed cannot be negative")
	}
	by := map[string]Limit{}
	for _, l := range p.Limits {
		a, err := netip.ParseAddr(strings.TrimSpace(l.IP))
		if err != nil {
			return fmt.Errorf("limit: %q is not an IP address", l.IP)
		}
		if l.Mbps < 0 || l.MinMbps < 0 || l.Mbps == 0 && l.MinMbps == 0 {
			return fmt.Errorf("limit for %s: speed must be above 0 Mbit/s", a)
		}
		ip := a.Unmap().String()
		cur, ok := by[ip]
		if !ok {
			by[ip] = Limit{IP: ip, Mbps: l.Mbps, MinMbps: l.MinMbps}
			continue
		}
		if l.Mbps > 0 && (cur.Mbps == 0 || l.Mbps < cur.Mbps) {
			cur.Mbps = l.Mbps
		}
		cur.MinMbps = max(cur.MinMbps, l.MinMbps)
		by[ip] = cur
	}
	p.Limits = p.Limits[:0]
	for _, l := range by {
		if p.LinkMbps == 0 {
			l.MinMbps = 0
		}
		if l.Mbps > 0 && l.MinMbps > l.Mbps {
			l.MinMbps = l.Mbps
		}
		if l.Mbps > 0 || l.MinMbps > 0 {
			p.Limits = append(p.Limits, l)
		}
	}
	sort.Slice(p.Limits, func(i, j int) bool { return p.Limits[i].IP < p.Limits[j].IP })
	return nil
}

func (p Plan) guarantees() bool {
	for _, l := range p.Limits {
		if l.MinMbps > 0 {
			return true
		}
	}
	return false
}

// Commands renders the tc invocations that turn dev's shaping into exactly the
// plan, starting from a clean root and ingress qdisc.
func Commands(dev string, p Plan) [][]string {
	cmds := [][]string{
		{"qdisc", "del", "dev", dev, "root"},
		{"qdisc", "del", "dev", dev, "ingress"},
	}
	if len(p.Limits) == 0 {
		return cmds
	}
	// Guaranteed classes only win the link from the rest when they share one
	// parent bounded at the link speed; plain caps need no tree.
	parent, ceilOf := "1:", func(l Limit) int { return l.Mbps }
	cmds = append(cmds, []string{"qdisc", "add", "dev", dev, "root", "handle", "1:", "htb", "default", "ffff"})
	if p.guarantees() {
		parent = "1:fffe"
		link := fmt.Sprintf("%dmbit", p.LinkMbps)
		rest := p.LinkMbps
		for _, l := range p.Limits {
			rest -= rateOf(l)
		}
		cmds = append(cmds,
			[]string{"class", "add", "dev", dev, "parent", "1:", "classid", parent, "htb", "rate", link, "ceil", link},
			[]string{"class", "add", "dev", dev, "parent", parent, "classid", "1:ffff", "htb", "rate", fmt.Sprintf("%dmbit", max(rest, 1)), "ceil", link},
		)
		ceilOf = func(l Limit) int {
			if l.Mbps > 0 {
				return l.Mbps
			}
			return p.LinkMbps
		}
	} else {
		cmds = append(cmds, []string{"class", "add", "dev", dev, "parent", "1:", "classid", "1:ffff", "htb", "rate", "100gbit"})
	}
	cmds = append(cmds, []string{"qdisc", "add", "dev", dev, "handle", "ffff:", "ingress"})
	for i, l := range p.Limits {
		class := fmt.Sprintf("1:%x", i+1)
		proto, match, bits := "ip", "ip", "32"
		if strings.Contains(l.IP, ":") {
			proto, match, bits = "ipv6", "ip6", "128"
		}
		cidr := l.IP + "/" + bits
		cmds = append(cmds,
			[]string{"class", "add", "dev", dev, "parent", parent, "classid", class, "htb", "rate", fmt.Sprintf("%dmbit", rateOf(l)), "ceil", fmt.Sprintf("%dmbit", ceilOf(l))},
			[]string{"filter", "add", "dev", dev, "parent", "1:", "protocol", proto, "prio", "1", "u32", "match", match, "dst", cidr, "flowid", class},
		)
		if l.Mbps > 0 {
			cmds = append(cmds, []string{
				"filter", "add", "dev", dev, "parent", "ffff:", "protocol", proto, "prio", "1", "u32", "match", match, "src", cidr,
				"police", "rate", fmt.Sprintf("%dmbit", l.Mbps), "burst", burstFor(l.Mbps), "drop", "flowid", ":1",
			})
		}
	}
	return cmds
}

// rateOf is what HTB promises the IP: its guarantee, or its cap when it has none.
func rateOf(l Limit) int {
	if l.MinMbps > 0 {
		return l.MinMbps
	}
	return l.Mbps
}

// burstFor sizes the policer bucket to ~10ms of traffic, never below 64k, so
// TCP keeps its window instead of collapsing on every burst.
func burstFor(mbps int) string {
	kb := mbps * 1000 / 8 / 100
	if kb < 64 {
		kb = 64
	}
	return fmt.Sprintf("%dk", kb)
}

// commandTimeout bounds one tc or ip call, so a wedged netlink never hangs the push.
const commandTimeout = 10 * time.Second

var (
	mu        sync.Mutex
	stateFile = "/run/x-ui-shaper.state"
	tc        = func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "tc", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("tc %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	defaultDev = defaultInterface
)

func defaultInterface() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "-o", "route", "show", "default").Output()
	if err != nil {
		return "", fmt.Errorf("find the default interface: %w", err)
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", errors.New("no default route")
}

func fingerprint(dev string, p Plan) string {
	sum := sha256.New()
	for _, c := range Commands(dev, p) {
		sum.Write([]byte(strings.Join(c, " ") + "\n"))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Owner says who pushed a plan: a node's own panel must not lift the caps its
// master pushed just because the node itself has none configured.
type Owner string

const (
	OwnerLocal  Owner = "local"
	OwnerMaster Owner = "master"
)

// Apply enforces the plan on the default interface. An unchanged plan is a
// no-op, so a periodic push never resets live shaping.
func Apply(p Plan, owner Owner) (Status, error) {
	if err := p.Normalize(); err != nil {
		return Status{}, err
	}
	mu.Lock()
	defer mu.Unlock()
	dev, err := defaultDev()
	if err != nil {
		return Status{}, err
	}
	st := Status{Interface: dev, Limited: len(p.Limits)}
	want := string(owner) + "\n" + fingerprint(dev, p)
	raw, _ := os.ReadFile(stateFile)
	prev := string(raw)
	if prev == want {
		return st, nil
	}
	if len(p.Limits) == 0 && (prev == "" || !strings.HasPrefix(prev, string(owner)+"\n")) {
		return st, nil
	}
	for i, c := range Commands(dev, p) {
		err := tc(c...)
		if err != nil && i >= 2 {
			return Status{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
		return Status{}, err
	}
	return st, os.WriteFile(stateFile, []byte(want), 0o600)
}
