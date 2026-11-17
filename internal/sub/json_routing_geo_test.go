package sub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xraygeodata "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func writeStandardGeoFiles(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	t.Setenv("XRAY_LOCATION_ASSET", "")
	t.Setenv("xray.location.asset", "")
	sites, err := proto.Marshal(&xraygeodata.GeoSiteList{Entry: []*xraygeodata.GeoSite{
		{Code: "RU", Domain: []*xraygeodata.Domain{{Type: xraygeodata.Domain_Domain, Value: "ru"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ips, err := proto.Marshal(&xraygeodata.GeoIPList{Entry: []*xraygeodata.GeoIP{
		{Code: "RU", Cidr: []*xraygeodata.CIDR{{Ip: []byte{5, 0, 0, 0}, Prefix: 8}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"geosite.dat": sites, "geoip.dat": ips} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A profile written for custom geo files (RoscomVPN's whitelist, geoip:direct)
// broke the JSON config of every non-Happ client, whose standard files lack
// those categories (#16); such tokens are dropped from the baked config only.
func TestJsonRoutingDropsCategoriesMissingFromStandardGeoFiles(t *testing.T) {
	writeStandardGeoFiles(t)
	profile := map[string]any{
		"DirectSites": []string{"geosite:ru", "geosite:whitelist", "domain:example.ru"},
		"DirectIp":    []string{"geoip:ru", "geoip:direct", "10.0.0.0/8"},
	}
	for _, tc := range []struct {
		name     string
		geoURLs  bool
		wantGone []string
	}{
		{"custom geo files", true, []string{"geosite:whitelist", "geoip:direct"}},
		{"standard geo files", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.geoURLs {
				profile["Geositeurl"] = "https://example.com/geosite.dat"
				profile["Geoipurl"] = "https://example.com/geoip.dat"
			} else {
				delete(profile, "Geositeurl")
				delete(profile, "Geoipurl")
			}
			raw, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			svc := NewSubJsonService("", "", "", string(raw), NewSubService(""))
			baked, err := json.Marshal(svc.bakedTemplate())
			if err != nil {
				t.Fatal(err)
			}
			text := string(baked)
			for _, kept := range []string{"geosite:ru", "domain:example.ru", "geoip:ru", "10.0.0.0/8"} {
				if !strings.Contains(text, `"`+kept+`"`) {
					t.Fatalf("baked config lost %s: %s", kept, text)
				}
			}
			for _, token := range []string{"geosite:whitelist", "geoip:direct"} {
				gone := strings.Contains(tc.name, "custom")
				if got := !strings.Contains(text, `"`+token+`"`); got != gone {
					t.Fatalf("%s dropped = %v, want %v: %s", token, got, gone, text)
				}
			}
		})
	}
}
