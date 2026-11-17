package subedge

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dnsRecordComment = "managed-by: my-panel-vpn"
	dnsRecordTTL     = 60
	// PromoteAfter is how long a better tier must stay up before DNS returns to it.
	PromoteAfter = 2 * time.Minute
	probeTimeout = 5 * time.Second
	// MasterSilence is how long after a master's last plan an edge node may
	// switch DNS itself; masters send the plan every 30 seconds.
	MasterSilence = 90 * time.Second
)

var cloudflareTimeout = 20 * time.Second

// cloudflareTransport opens a connection per call: a pooled one the network
// dropped without a reset hung every later call until the client timeout.
var cloudflareTransport = func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	return t
}()

// Tier names, best first: masters, then edge nodes in Russia, then the rest.
const (
	TierMasters = iota
	TierRussia
	TierForeign
	tierCount
)

// DNSPlan is what decides the subscription domain's A records: the addresses
// of each tier, best first. EdgesBefore is set only for an edge node.
type DNSPlan struct {
	Domain      string     `json:"domain" example:"sub.example.com"`
	Token       string     `json:"token" example:"cf-dns-token"`
	Tiers       [][]string `json:"tiers" example:"[[\"198.51.100.1\"],[\"203.0.113.7\"],[]]"`
	EdgesBefore []string   `json:"edgesBefore" example:"[\"203.0.113.7\"]"`
}

// Enabled reports whether the plan asks for DNS switching at all.
func (p DNSPlan) Enabled() bool { return p.Domain != "" && p.Token != "" }

// DNSStatus is the outcome of the last switching step.
type DNSStatus struct {
	Tier      int      `json:"tier" example:"0"`
	Published []string `json:"published" example:"[\"198.51.100.1\"]"`
	Unmanaged []string `json:"unmanaged" example:"[]"`
	Error     string   `json:"error" example:""`
	At        int64    `json:"at" example:"1762736400"`
}

// Cloudflare edits the A records of one name through the Cloudflare API,
// touching only the records it created itself.
type Cloudflare struct {
	Token   string
	BaseURL string
	Client  *http.Client
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (c *Cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, &payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: cloudflareTimeout, Transport: cloudflareTransport}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Success bool                       `json:"success"`
		Errors  []struct{ Message string } `json:"errors"`
		Result  json.RawMessage            `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("cloudflare %s %s: HTTP %d: %w", method, path, resp.StatusCode, err)
	}
	if !envelope.Success {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("cloudflare %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

// zoneID finds the zone holding domain, trying its parents from the longest.
func (c *Cloudflare) zoneID(ctx context.Context, domain string) (string, error) {
	labels := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for i := 0; i+1 < len(labels); i++ {
		var zones []struct{ ID string }
		if err := c.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(strings.Join(labels[i:], ".")), nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("no Cloudflare zone this token can see holds %s", domain)
}

func (c *Cloudflare) records(ctx context.Context, zone, domain string) ([]cfRecord, error) {
	var recs []cfRecord
	err := c.call(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?per_page=100&type=A&name="+url.QueryEscape(domain), nil, &recs)
	return recs, err
}

// Published returns the addresses of the records the panel manages and of
// those it found but leaves alone.
func (c *Cloudflare) Published(ctx context.Context, domain string) (managed, unmanaged []string, err error) {
	zone, err := c.zoneID(ctx, domain)
	if err != nil {
		return nil, nil, err
	}
	recs, err := c.records(ctx, zone, domain)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range recs {
		if r.Comment == dnsRecordComment {
			managed = append(managed, r.Content)
		} else {
			unmanaged = append(unmanaged, r.Content)
		}
	}
	slices.Sort(managed)
	slices.Sort(unmanaged)
	return managed, unmanaged, nil
}

// Publish makes the managed A records of domain exactly ips, adding the new
// ones before removing the old so the name never resolves to nothing.
func (c *Cloudflare) Publish(ctx context.Context, domain string, ips []string) error {
	zone, err := c.zoneID(ctx, domain)
	if err != nil {
		return err
	}
	recs, err := c.records(ctx, zone, domain)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	var stale []cfRecord
	for _, r := range recs {
		if r.Comment != dnsRecordComment {
			continue
		}
		if slices.Contains(ips, r.Content) && !have[r.Content] {
			have[r.Content] = true
			continue
		}
		stale = append(stale, r)
	}
	for _, ip := range ips {
		if have[ip] {
			continue
		}
		rec := cfRecord{Type: "A", Name: domain, Content: ip, TTL: dnsRecordTTL, Comment: dnsRecordComment}
		if err := c.call(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", rec, nil); err != nil {
			return err
		}
	}
	for _, r := range stale {
		if err := c.call(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+r.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Prober checks that an address serves the subscription domain over TLS.
type Prober struct {
	Domain string
	Port   int
	Roots  *x509.CertPool
}

func (p Prober) Up(ctx context.Context, ip string) bool {
	port := p.Port
	if port == 0 {
		port = 443
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: probeTimeout},
		Config:    &tls.Config{ServerName: p.Domain, RootCAs: p.Roots, MinVersion: tls.VersionTLS12},
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Live returns the addresses of ips that are up, probing them in parallel.
func (p Prober) Live(ctx context.Context, ips []string) []string {
	up := make([]bool, len(ips))
	var wg sync.WaitGroup
	for i, ip := range ips {
		wg.Go(func() { up[i] = p.Up(ctx, ip) })
	}
	wg.Wait()
	var live []string
	for i, ip := range ips {
		if up[i] {
			live = append(live, ip)
		}
	}
	slices.Sort(live)
	return live
}

// Switcher picks the tier to publish: a worse tier at once when the published
// one is down, a better one only after it stayed up for PromoteAfter.
type Switcher struct {
	upSince [tierCount]time.Time
}

// Choose returns the tier and its live addresses to publish, or ok false when
// no tier is up and DNS should stay as it is.
func (s *Switcher) Choose(now time.Time, tiers, live [][]string, published []string) (tier int, ips []string, ok bool) {
	current := tierOf(tiers, published)
	for t := range tierCount {
		if t >= len(live) || len(live[t]) == 0 {
			s.upSince[t] = time.Time{}
		} else if s.upSince[t].IsZero() {
			s.upSince[t] = now
		}
	}
	currentUp := current >= 0 && current < len(live) && len(live[current]) > 0
	for t := range min(len(live), tierCount) {
		if len(live[t]) == 0 {
			continue
		}
		if !currentUp || t >= current || now.Sub(s.upSince[t]) >= PromoteAfter {
			return t, live[t], true
		}
	}
	return -1, nil, false
}

// tierOf is the tier the published addresses belong to, or -1.
func tierOf(tiers [][]string, published []string) int {
	for _, ip := range published {
		for t, addrs := range tiers {
			if slices.Contains(addrs, ip) {
				return t
			}
		}
	}
	return -1
}

// Step probes every tier and publishes the chosen one when it differs from DNS.
func (s *Switcher) Step(ctx context.Context, cf *Cloudflare, plan DNSPlan, live func(context.Context, []string) []string, now time.Time) DNSStatus {
	status := DNSStatus{Tier: -1, At: now.Unix()}
	managed, unmanaged, err := cf.Published(ctx, plan.Domain)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Published, status.Unmanaged = managed, unmanaged
	up := make([][]string, len(plan.Tiers))
	for t, ips := range plan.Tiers {
		up[t] = live(ctx, ips)
	}
	tier, ips, ok := s.Choose(now, plan.Tiers, up, managed)
	if !ok {
		status.Tier = tierOf(plan.Tiers, managed)
		status.Error = "no tier answers; DNS left as it is"
		return status
	}
	status.Tier = tier
	if !slices.Equal(ips, managed) {
		if err := cf.Publish(ctx, plan.Domain, ips); err != nil {
			status.Error = err.Error()
			return status
		}
		status.Published = ips
	}
	return status
}

// HeardFromMaster records that a master, which switches DNS itself, sent this
// node its plan at now.
func (n *Node) HeardFromMaster(now time.Time) {
	n.dnsMu.Lock()
	defer n.dnsMu.Unlock()
	n.masterHeard = now
}

// StepDNS switches DNS from this edge node, but only while no master reaches
// it or sent a plan lately, no master address answers and no earlier edge is up.
func (n *Node) StepDNS(ctx context.Context, cf *Cloudflare, plan DNSPlan, live func(context.Context, []string) []string, now time.Time) (DNSStatus, bool) {
	n.dnsMu.Lock()
	heard := n.masterHeard
	n.dnsMu.Unlock()
	if !heard.IsZero() && now.Sub(heard) < MasterSilence {
		return DNSStatus{}, false
	}
	if len(n.Tunnels()) > 0 {
		return DNSStatus{}, false
	}
	if len(plan.Tiers) > TierMasters && len(live(ctx, plan.Tiers[TierMasters])) > 0 {
		return DNSStatus{}, false
	}
	if len(live(ctx, plan.EdgesBefore)) > 0 {
		return DNSStatus{}, false
	}
	n.dnsMu.Lock()
	defer n.dnsMu.Unlock()
	return n.dns.Step(ctx, cf, plan, live, now), true
}
