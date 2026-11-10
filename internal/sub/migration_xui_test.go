package sub

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// writeLegacyXUIDB builds a 3x-ui 2.x x-ui.db (clients only in settings JSON)
// where one subscription holds a VLESS and a Trojan client under two emails.
func writeLegacyXUIDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old-x-ui.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE inbounds (id integer primary key autoincrement, user_id integer, up integer, down integer, total integer,
		 remark text, enable numeric, expiry_time integer, listen text, port integer, protocol text, settings text,
		 stream_settings text, tag text unique, sniffing text)`,
		`CREATE TABLE client_traffics (id integer primary key autoincrement, inbound_id integer, enable numeric, email text unique,
		 up integer, down integer, expiry_time integer, total integer, reset integer default 0)`,
		`CREATE TABLE settings (id integer primary key autoincrement, key text, value text)`,
		`INSERT INTO inbounds VALUES (1, 1, 0, 0, 0, 'reality', 1, 0, '', 443, 'vless',
		 '{"clients":[{"id":"0e2f4a6c-8e1a-4c3e-9f5b-7d9e1f3a5c7e","flow":"xtls-rprx-vision","email":"ann-vless","limitIp":0,"totalGB":"53687091200","expiryTime":1801396800000,"enable":true,"tgId":"777","subId":"annSub123"},
		              {"id":"1a3c5e7a-9b1d-4f3a-8c5e-7a9b1d3f5a7c","flow":"xtls-rprx-vision","email":"carl","enable":false,"subId":"carlSub456"}],"decryption":"none","fallbacks":[]}',
		 '{"network":"tcp","security":"reality","realitySettings":{"show":false,"xver":0,"dest":"www.google.com:443","serverNames":["www.google.com"],"privateKey":"` + rwRealityPrivate + `","shortIds":["ab12"],"settings":{"publicKey":"` + rwRealityPublic + `","fingerprint":"firefox","serverName":"","spiderX":"/"}},"tcpSettings":{"header":{"type":"none"}}}',
		 'inbound-443', '{"enabled":true,"destOverride":["http","tls"]}')`,
		`INSERT INTO inbounds VALUES (2, 1, 0, 0, 0, 'trojan', 1, 0, '', 2083, 'trojan',
		 '{"clients":[{"password":"annTrojanPass","email":"ann-trojan","enable":true,"subId":"annSub123"}],"fallbacks":[]}',
		 '{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}', 'inbound-2083', '{"enabled":false}')`,
		`INSERT INTO client_traffics (inbound_id, enable, email, up, down, expiry_time, total) VALUES (1, 1, 'ann-vless', 100, 900, 0, 0), (2, 1, 'ann-trojan', 10, 90, 0, 0)`,
		`INSERT INTO settings (key, value) VALUES ('subPath', '/mysub/'), ('subDomain', 'vpn.example.org'), ('subPort', '2096')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return path
}

// Merging an old 3x-ui into this panel must keep each subscription id with
// every link it served, and keep keys, limits and usage.
func TestXUIImport_KeepsSubscriptionsAndKeys(t *testing.T) {
	seedSubDB(t)
	src := writeLegacyXUIDB(t)
	svc := &service.MigrationService{}

	preview, err := svc.PreviewXUI(src, nil)
	if err != nil {
		t.Fatalf("PreviewXUI: %v", err)
	}
	if len(preview.Servers) != 1 || preview.Servers[0].Suggested != service.MigrationTargetLocal {
		t.Fatalf("servers = %+v, want the 3x-ui server suggested here", preview.Servers)
	}
	if preview.Clients.Total != 2 {
		t.Fatalf("clients = %d, want ann (two emails, one subscription) and carl", preview.Clients.Total)
	}
	if preview.Sub.ExampleURL != "http://vpn.example.org:2096/mysub/annSub123" {
		t.Errorf("example sub url = %q", preview.Sub.ExampleURL)
	}

	res, err := svc.ApplyXUI(src, nil, true, 1)
	if err != nil {
		t.Fatalf("ApplyXUI: %v", err)
	}
	if res.InboundsCreated != 2 || res.Clients.Created != 2 || len(res.Clients.Skipped) != 0 {
		t.Fatalf("result = %+v", res)
	}

	links, _, _, _, err := NewSubService("").GetSubs("annSub123", "vpn.example.org")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	got := map[string]string{}
	for _, l := range links {
		u, _ := url.Parse(l)
		got[u.Scheme] = u.User.Username() + "@" + u.Port() + " pbk=" + u.Query().Get("pbk") + " fp=" + u.Query().Get("fp")
	}
	if len(links) != 2 ||
		got["vless"] != "0e2f4a6c-8e1a-4c3e-9f5b-7d9e1f3a5c7e@443 pbk="+rwRealityPublic+" fp=firefox" ||
		got["trojan"] != "annTrojanPass@2083 pbk= fp=" {
		t.Fatalf("ann's links = %v", links)
	}

	var stat xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", "ann-vless").First(&stat).Error; err != nil {
		t.Fatalf("ann traffic: %v", err)
	}
	if stat.Up != 110 || stat.Down != 990 || stat.Total != 53687091200 || stat.ExpiryTime != 1801396800000 {
		t.Errorf("ann traffic = %+v, want both emails' usage and her limits", stat)
	}
	if p, _ := (&service.SettingService{}).GetSubPath(); p != "/mysub/" {
		t.Errorf("subPath = %q, want the old panel's /mysub/", p)
	}
}
