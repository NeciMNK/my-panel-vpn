// Package shaper caps the speed of single client IPs with Linux tc: an HTB
// class per IP for downloads and an ingress policer per IP for uploads.
package shaper

import (
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
)

// Limit caps every connection from one client IP.
type Limit struct {
	IP   string `json:"ip"`
	Mbps int    `json:"mbps"`
}

// Plan is the full set of limits one server must enforce.
type Plan struct {
	Limits []Limit `json:"limits"`
}

// Status is what a server reports after applying a plan.
type Status struct {
	Interface string `json:"interface" example:"eth0"`
	Limited   int    `json:"limited" example:"3"`
}

// Normalize validates IPs and rates, keeps the strictest limit per IP and
// sorts, so equal plans render identical commands.
func (p *Plan) Normalize() error {
	by := map[string]int{}
	for _, l := range p.Limits {
		a, err := netip.ParseAddr(strings.TrimSpace(l.IP))
		if err != nil {
			return fmt.Errorf("limit: %q is not an IP address", l.IP)
		}
		if l.Mbps <= 0 {
			return fmt.Errorf("limit for %s: speed must be above 0 Mbit/s", a)
		}
		ip := a.Unmap().String()
		if cur, ok := by[ip]; !ok || l.Mbps < cur {
			by[ip] = l.Mbps
		}
	}
	p.Limits = p.Limits[:0]
	for ip, mbps := range by {
		p.Limits = append(p.Limits, Limit{IP: ip, Mbps: mbps})
	}
	sort.Slice(p.Limits, func(i, j int) bool { return p.Limits[i].IP < p.Limits[j].IP })
	return nil
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
	cmds = append(cmds,
		[]string{"qdisc", "add", "dev", dev, "root", "handle", "1:", "htb", "default", "ffff"},
		[]string{"class", "add", "dev", dev, "parent", "1:", "classid", "1:ffff", "htb", "rate", "100gbit"},
		[]string{"qdisc", "add", "dev", dev, "handle", "ffff:", "ingress"},
	)
	for i, l := range p.Limits {
		class := fmt.Sprintf("1:%x", i+1)
		rate := fmt.Sprintf("%dmbit", l.Mbps)
		proto, match, bits := "ip", "ip", "32"
		if strings.Contains(l.IP, ":") {
			proto, match, bits = "ipv6", "ip6", "128"
		}
		cidr := l.IP + "/" + bits
		cmds = append(cmds,
			[]string{"class", "add", "dev", dev, "parent", "1:", "classid", class, "htb", "rate", rate, "ceil", rate},
			[]string{"filter", "add", "dev", dev, "parent", "1:", "protocol", proto, "prio", "1", "u32", "match", match, "dst", cidr, "flowid", class},
			[]string{
				"filter", "add", "dev", dev, "parent", "ffff:", "protocol", proto, "prio", "1", "u32", "match", match, "src", cidr,
				"police", "rate", rate, "burst", burstFor(l.Mbps), "drop", "flowid", ":1",
			},
		)
	}
	return cmds
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

var (
	mu        sync.Mutex
	stateFile = "/run/x-ui-shaper.state"
	tc        = func(args ...string) error {
		out, err := exec.Command("tc", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("tc %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	defaultDev = defaultInterface
)

func defaultInterface() (string, error) {
	out, err := exec.Command("ip", "-o", "route", "show", "default").Output()
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
