package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// cascadeRules returns the cascade-owned rules of cfg in order, round-tripped
// through JSON so []string and []any read the same.
func cascadeRules(t *testing.T, cfg map[string]any, id int) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(templateRouting(cfg)["rules"])
	var rules []map[string]any
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range rules {
		if tag, _ := r["ruleTag"].(string); strings.HasPrefix(tag, cascadePrefix(id)) {
			out = append(out, r)
		}
	}
	return out
}

func strs(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}

func ruleSummary(r map[string]any) string {
	var b strings.Builder
	b.WriteString(strings.TrimPrefix(r["ruleTag"].(string), "mp-cascade-4-"))
	if in := strs(r["inboundTag"]); len(in) > 0 {
		b.WriteString(" in=" + strings.Join(in, ","))
	}
	if r["domain"] != nil {
		b.WriteString(" domain")
	}
	if r["ip"] != nil {
		b.WriteString(" ip")
	}
	if r["user"] != nil {
		b.WriteString(" user")
	}
	if o, ok := r["outboundTag"].(string); ok {
		b.WriteString(" ->" + strings.TrimPrefix(o, "mp-cascade-4-"))
	}
	if o, ok := r["balancerTag"].(string); ok {
		b.WriteString(" =>" + strings.TrimPrefix(o, "mp-cascade-4-"))
	}
	return b.String()
}

func summaries(rules []map[string]any) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, ruleSummary(r))
	}
	return out
}

func TestCascadeModeRoutes(t *testing.T) {
	one := []model.CascadeLink{{NodeId: 2, Address: "de", Port: 1}}
	two := []model.CascadeLink{{NodeId: 2, Address: "de", Port: 1}, {NodeId: 5, Address: "nl", Port: 2}}
	pins := []model.CascadePin{{Email: "bob", NodeId: 5}}
	cases := []struct {
		name  string
		c     model.Cascade
		pins  []model.CascadePin
		want  []string
		local bool
	}{
		{
			name: "legacy cascade without a mode stays a pure relay",
			c:    model.Cascade{Links: one},
			want: []string{"route ->n2"},
		},
		{
			name: "relay sends everything to the exit",
			c:    model.Cascade{Mode: CascadeModeRelay, Links: one},
			want: []string{"route ->n2"},
		},
		{
			name:  "split lets the zone out of the entry, the rest goes to the exit",
			c:     model.Cascade{Mode: CascadeModeSplit, Links: one},
			want:  []string{"direct-domain domain ->direct", "direct-ip ip ->direct", "route ->n2"},
			local: true,
		},
		{
			name:  "split pins sit after the direct rules so pinned users still split",
			c:     model.Cascade{Mode: CascadeModeSplit, Links: two},
			pins:  pins,
			want:  []string{"direct-domain domain ->direct", "direct-ip ip ->direct", "pin-n5 user ->n5", "route =>route"},
			local: true,
		},
		{
			name: "both relays the picked inbounds before splitting the rest",
			c: model.Cascade{
				Mode: CascadeModeBoth, Links: two, RelayInboundTags: []string{"in-relay"},
			},
			pins: pins,
			want: []string{
				"relay-pin-n5 in=in-relay user ->n5", "relay in=in-relay =>route",
				"direct-domain domain ->direct", "direct-ip ip ->direct",
				"pin-n5 user ->n5", "route =>route",
			},
			local: true,
		},
		{
			name: "both keeps the chained-inbound filter on the split part",
			c: model.Cascade{
				Mode: CascadeModeBoth, Links: one,
				EntryInboundTags: []string{"in-split"}, RelayInboundTags: []string{"in-relay"},
			},
			want: []string{
				"relay in=in-relay ->n2",
				"direct-domain in=in-split domain ->direct", "direct-ip in=in-split ip ->direct",
				"route in=in-split ->n2",
			},
			local: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseTemplate(t)
			tc.c.Id = 4
			tc.c.Strategy = "leastPing"
			applyCascadeToTemplate(cfg, &tc.c, tc.pins, aiExitPlan{}, splitZone("RU"))
			got := summaries(cascadeRules(t, cfg, 4))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("rules =\n %v\nwant\n %v", got, tc.want)
			}
			hasDirect := false
			for _, o := range templateList(cfg, "outbounds") {
				if tagOf(o, "tag") == cascadePrefix(4)+"direct" {
					hasDirect = o.(map[string]any)["protocol"] == "freedom"
				}
			}
			if hasDirect != tc.local {
				t.Fatalf("direct freedom outbound present = %v, want %v", hasDirect, tc.local)
			}
		})
	}
}

func TestCascadeSplitAddsAdminEntriesToTheZone(t *testing.T) {
	cfg := baseTemplate(t)
	c := &model.Cascade{
		Id: 4, Mode: CascadeModeSplit, Links: []model.CascadeLink{{NodeId: 2}},
		DirectDomains: []string{"example.com"}, DirectIPs: []string{"203.0.113.0/24"},
	}
	applyCascadeToTemplate(cfg, c, nil, aiExitPlan{}, splitZone("RU"))
	rules := cascadeRules(t, cfg, 4)
	domains, ips := strs(rules[0]["domain"]), strs(rules[1]["ip"])
	for _, want := range []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru", "example.com"} {
		if !slices.Contains(domains, want) {
			t.Fatalf("direct domains %v miss %q", domains, want)
		}
	}
	if !slices.Equal(ips, []string{"geoip:ru", "203.0.113.0/24"}) {
		t.Fatalf("direct ips = %v", ips)
	}
}

func TestSplitZoneFollowsTheEntryCountry(t *testing.T) {
	cases := []struct {
		country string
		domains []string
		ips     []string
	}{
		{"", []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru"}, []string{"geoip:ru"}},
		{"de", []string{"domain:de"}, []string{"geoip:de"}},
		{"GB", []string{"domain:uk"}, []string{"geoip:gb"}},
	}
	for _, tc := range cases {
		t.Run(tc.country, func(t *testing.T) {
			z := splitZone(tc.country)
			if !slices.Equal(z.Domains, tc.domains) || !slices.Equal(z.IPs, tc.ips) {
				t.Fatalf("zone(%q) = %+v", tc.country, z)
			}
		})
	}
}

func TestCascadeSplitStripsCleanly(t *testing.T) {
	cfg := baseTemplate(t)
	before, _ := json.Marshal(cfg)
	c := &model.Cascade{
		Id: 4, Mode: CascadeModeBoth, RelayInboundTags: []string{"r"},
		Links: []model.CascadeLink{{NodeId: 2}, {NodeId: 5}},
	}
	applyCascadeToTemplate(cfg, c, []model.CascadePin{{Email: "a", NodeId: 2}}, aiExitPlan{}, splitZone("RU"))
	raw, _ := json.Marshal(cfg)
	again := map[string]any{}
	_ = json.Unmarshal(raw, &again)
	stripManaged(again, cascadePrefix(4))
	after, _ := json.Marshal(again)
	if string(after) != string(before) {
		t.Fatalf("strip left traces:\n before %s\n after  %s", before, after)
	}
}

func TestCascadeValidateMode(t *testing.T) {
	cases := []struct {
		name    string
		req     CascadeRequest
		mode    string
		wantErr string
	}{
		{name: "new cascade splits by default", req: CascadeRequest{}, mode: CascadeModeSplit},
		{name: "relay kept", req: CascadeRequest{Mode: "relay"}, mode: CascadeModeRelay},
		{name: "unknown mode", req: CascadeRequest{Mode: "mirror"}, wantErr: "unknown cascade mode mirror"},
		{name: "both needs relay inbounds", req: CascadeRequest{Mode: "both"}, wantErr: "pick the entry inbounds that only relay"},
		{
			name:    "an inbound cannot both relay and split",
			req:     CascadeRequest{Mode: "both", RelayInboundTags: []string{"a"}, EntryInboundTags: []string{"a", "b"}},
			wantErr: "inbound a is listed both for relay and for split",
		},
		{name: "bad ip", req: CascadeRequest{DirectIPs: []string{"10.0.0.300"}}, wantErr: "not an IP, CIDR or geoip: entry: 10.0.0.300"},
		{name: "bad domain", req: CascadeRequest{DirectDomains: []string{"a b.ru"}}, wantErr: "not a domain rule: a b.ru"},
		{name: "bad regexp", req: CascadeRequest{DirectDomains: []string{"regexp:("}}, wantErr: "not a domain rule: regexp:("},
		{
			name: "good entries are trimmed and empties dropped",
			req: CascadeRequest{
				DirectDomains: []string{" geosite:google ", "", "full:ya.ru", "regexp:\\.ru$"},
				DirectIPs:     []string{"1.2.3.4", "10.0.0.0/8", "geoip:kz", "2001:db8::/32", " "},
			},
			mode: CascadeModeSplit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			err := normalizeCascadeRouting(&req)
			if tc.wantErr != "" {
				if err == nil || strings.TrimSpace(err.Error()) != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if req.Mode != tc.mode {
				t.Fatalf("mode = %q, want %q", req.Mode, tc.mode)
			}
			for _, d := range append(req.DirectDomains, req.DirectIPs...) {
				if d == "" || strings.TrimSpace(d) != d {
					t.Fatalf("entry %q not normalised: %v %v", d, req.DirectDomains, req.DirectIPs)
				}
			}
		})
	}
}
