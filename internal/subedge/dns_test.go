package subedge

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloudflare keeps the A records of one zone the way the Cloudflare API does.
type fakeCloudflare struct {
	mu      sync.Mutex
	zone    string
	records map[string]cfRecord
	next    int
}

func newFakeCloudflare(t *testing.T, zone string, existing ...cfRecord) (*fakeCloudflare, *Cloudflare) {
	t.Helper()
	f := &fakeCloudflare{zone: zone, records: map[string]cfRecord{}}
	for _, r := range existing {
		f.add(r)
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, &Cloudflare{Token: "tok", BaseURL: srv.URL}
}

func (f *fakeCloudflare) add(r cfRecord) {
	f.next++
	r.ID = strconv.Itoa(f.next)
	f.records[r.ID] = r
}

func (f *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}
	switch {
	case r.Header.Get("Authorization") != "Bearer tok":
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]string{{"message": "Invalid API Token"}}})
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		if r.URL.Query().Get("name") == f.zone {
			reply([]map[string]string{{"id": "z1"}})
		} else {
			reply([]map[string]string{})
		}
	case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
		var out []cfRecord
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") && rec.Type == r.URL.Query().Get("type") {
				out = append(out, rec)
			}
		}
		reply(out)
	case r.Method == http.MethodPost && r.URL.Path == "/zones/z1/dns_records":
		var rec cfRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		f.add(rec)
		reply(rec)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/z1/dns_records/"):
		delete(f.records, strings.TrimPrefix(r.URL.Path, "/zones/z1/dns_records/"))
		reply(map[string]string{})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCloudflare) addresses(managed bool) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.records {
		if (r.Comment == dnsRecordComment) == managed {
			out = append(out, r.Content)
		}
	}
	slices.Sort(out)
	return out
}

func managedRecord(ip string) cfRecord {
	return cfRecord{Type: "A", Name: "sub.example.com", Content: ip, TTL: dnsRecordTTL, Comment: dnsRecordComment}
}

func TestDNSFollowsTheBestLiveTier(t *testing.T) {
	const master, ru, foreign = "198.51.100.1", "203.0.113.7", "192.0.2.9"
	cf, client := newFakeCloudflare(t, "example.com", managedRecord(master), cfRecord{Type: "A", Name: "sub.example.com", Content: "192.0.2.200", TTL: 1})
	plan := DNSPlan{Domain: "sub.example.com", Token: "tok", Tiers: [][]string{{master}, {ru}, {foreign}}}
	sw := &Switcher{}
	start := time.Unix(1_800_000_000, 0)

	steps := []struct {
		name  string
		after time.Duration
		up    []string
		want  []string
	}{
		{"masters up keep their record", 0, []string{master, ru, foreign}, []string{master}},
		{"masters down fall to Russia at once", time.Minute, []string{ru, foreign}, []string{ru}},
		{"Russia down falls to the foreign tier", 2 * time.Minute, []string{foreign}, []string{foreign}},
		{"Russia back is not trusted yet", 3 * time.Minute, []string{ru, foreign}, []string{foreign}},
		{"Russia stable for the settle time takes over", 5 * time.Minute, []string{ru, foreign}, []string{ru}},
		{"nothing up leaves DNS as it is", 6 * time.Minute, nil, []string{ru}},
		{"masters back after the outage still wait", 7 * time.Minute, []string{master, ru}, []string{ru}},
		{"the published tier dying takes the best live one, settled or not", 8 * time.Minute, []string{master, foreign}, []string{master}},
	}
	for _, step := range steps {
		live := func(_ context.Context, ips []string) []string {
			var out []string
			for _, ip := range ips {
				if slices.Contains(step.up, ip) {
					out = append(out, ip)
				}
			}
			return out
		}
		status := sw.Step(context.Background(), client, plan, live, start.Add(step.after))
		if got := cf.addresses(true); !slices.Equal(got, step.want) {
			t.Fatalf("%s: DNS = %v (status %+v), want %v", step.name, got, status, step.want)
		}
		if got := cf.addresses(false); !slices.Equal(got, []string{"192.0.2.200"}) {
			t.Fatalf("%s: the admin's own record changed to %v", step.name, got)
		}
	}
}

func TestPublishWritesUnproxiedShortLivedRecords(t *testing.T) {
	cf, client := newFakeCloudflare(t, "example.com")
	if err := client.Publish(context.Background(), "sub.example.com", []string{"203.0.113.7"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range cf.records {
		if r.Proxied || r.TTL != dnsRecordTTL || r.Comment != dnsRecordComment || r.Name != "sub.example.com" {
			t.Fatalf("record = %+v, want an unproxied %ds record marked as the panel's", r, dnsRecordTTL)
		}
	}
}

func TestCloudflareErrorsReachTheStatus(t *testing.T) {
	_, client := newFakeCloudflare(t, "example.com")
	client.Token = "wrong"
	status := (&Switcher{}).Step(context.Background(), client, DNSPlan{Domain: "sub.example.com", Tiers: [][]string{{"198.51.100.1"}}}, func(context.Context, []string) []string { return nil }, time.Now())
	if !strings.Contains(status.Error, "Invalid API Token") {
		t.Fatalf("status error = %q, want Cloudflare's message", status.Error)
	}
}

func TestProberNeedsTheSubscriptionCertificate(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	cases := []struct {
		name   string
		domain string
		port   int
		want   bool
	}{
		{"the edge's certificate covers the domain", "example.com", port, true},
		{"a certificate for another name", "sub.other.org", port, false},
		{"nothing listens", "example.com", closedPort(t), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Prober{Domain: tc.domain, Port: tc.port, Roots: roots}
			if got := p.Up(context.Background(), "127.0.0.1"); got != tc.want {
				t.Fatalf("Up = %v, want %v", got, tc.want)
			}
		})
	}
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestEdgeNodeSwitchesDNSOnlyWhenNoMasterOrEarlierEdgeCan(t *testing.T) {
	const master, ru1, ru2 = "198.51.100.1", "203.0.113.7", "203.0.113.8"
	plan := DNSPlan{Domain: "sub.example.com", Token: "tok", Tiers: [][]string{{master}, {ru1, ru2}, {}}, EdgesBefore: []string{ru1}}
	cases := []struct {
		name   string
		tunnel bool
		up     []string
		acts   bool
	}{
		{"a master's tunnel is up", true, []string{ru2}, false},
		{"a master answers on its address", false, []string{master, ru2}, false},
		{"an edge ordered before this one is up", false, []string{ru1, ru2}, false},
		{"no master and no earlier edge", false, []string{ru2}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cf, client := newFakeCloudflare(t, "example.com", managedRecord(master))
			node := &Node{}
			if tc.tunnel {
				connectMaster(t, node, "m1", 1, http.NotFoundHandler())
			}
			live := func(_ context.Context, ips []string) []string {
				var out []string
				for _, ip := range ips {
					if slices.Contains(tc.up, ip) {
						out = append(out, ip)
					}
				}
				return out
			}
			_, acted := node.StepDNS(context.Background(), client, plan, live, time.Now())
			want := []string{master}
			if tc.acts {
				want = []string{ru2}
			}
			if got := cf.addresses(true); acted != tc.acts || !slices.Equal(got, want) {
				t.Fatalf("acted %v, DNS %v; want acted %v, DNS %v", acted, got, tc.acts, want)
			}
		})
	}
}
