package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
)

func TestSubEdgeNodeConfig(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	ru := &model.Node{Id: 2, Name: "ru-1", Enable: true}
	cert := &model.TlsCertificate{
		Domains:      []string{"sub.example.com"},
		NodeInstalls: map[int]model.TlsCertNodeInstall{2: {CertFile: "/etc/x-ui/certs/sub/fullchain.pem", KeyFile: "/etc/x-ui/certs/sub/privkey.pem"}},
	}
	if err := database.GetDB().Create(cert).Error; err != nil {
		t.Fatal(err)
	}
	notPushed := &model.TlsCertificate{Domains: []string{"other.example.com"}}
	if err := database.GetDB().Create(notPushed).Error; err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		edge    model.SubEdge
		node    model.Node
		want    subedge.ListenerConfig
		wantErr string
	}{
		{
			"serves the certificate the master pushed to the node",
			model.SubEdge{Enable: true, Port: 443, CertId: cert.Id},
			*ru,
			subedge.ListenerConfig{Enable: true, Port: 443, CertFile: "/etc/x-ui/certs/sub/fullchain.pem", KeyFile: "/etc/x-ui/certs/sub/privkey.pem"},
			"",
		},
		{"no certificate serves plain HTTP", model.SubEdge{Enable: true, Port: 8080}, *ru, subedge.ListenerConfig{Enable: true, Port: 8080}, ""},
		{"a certificate not yet on the node says so", model.SubEdge{Enable: true, Port: 443, CertId: notPushed.Id}, *ru, subedge.ListenerConfig{}, "not on node ru-1 yet"},
		{"a disabled edge stops the listener", model.SubEdge{Enable: false, Port: 443, CertId: notPushed.Id}, *ru, subedge.ListenerConfig{Port: 443}, ""},
		{"a disabled node stops the listener", model.SubEdge{Enable: true, Port: 443, CertId: cert.Id}, model.Node{Id: 2, Name: "ru-1"}, subedge.ListenerConfig{Port: 443}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&SubEdgeService{}).nodeConfig(&tc.edge, &tc.node)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("nodeConfig error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("nodeConfig = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestConfigureSubEdgeNodeKeepsCertificatesInTheCertFolder(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	err := (&SettingService{}).ConfigureSubEdgeNode(subedge.ListenerConfig{Enable: true, Port: 443, CertFile: "/etc/shadow", KeyFile: "/etc/shadow"})
	if err == nil || !strings.Contains(err.Error(), "outside the certificate folder") {
		t.Fatalf("ConfigureSubEdgeNode = %v, want a refusal of files outside the certificate folder", err)
	}
	if raw, _ := (&SettingService{}).getString("subEdgeNode"); raw != "" {
		t.Fatalf("refused config was saved: %s", raw)
	}
}

func TestPushCachesResendsWhatTheNodeDidNotTake(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	useTestRuntimeManager(t)
	db := database.GetDB()
	for _, c := range []model.ClientRecord{
		{Email: "on@example.com", SubID: "abc", Enable: true},
		{Email: "off@example.com", SubID: "off", Enable: false},
	} {
		enable := c.Enable
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE clients SET enable = ? WHERE id = ?", enable, c.Id).Error; err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	fail := true
	var got []subedge.CacheUpdate
	nodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			http.Error(w, "node busy", http.StatusInternalServerError)
			return
		}
		var u subedge.CacheUpdate
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			t.Errorf("node got an unreadable update: %v", err)
		}
		got = append(got, u)
		_, _ = w.Write([]byte(`{"success":true,"msg":""}`))
	}))
	t.Cleanup(nodeSrv.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(nodeSrv.URL, "http://"))
	portNum, _ := strconv.Atoi(port)
	node := &model.Node{Name: "ru-1", Scheme: "http", Address: host, Port: portNum, BasePath: "/", ApiToken: "tok", Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "verify"}
	if err := db.Create(node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SubEdge{NodeId: node.Id, Enable: true, Port: 443}).Error; err != nil {
		t.Fatal(err)
	}

	SubEdgeRecorder().SetActive(true)
	t.Cleanup(func() { SubEdgeRecorder().SetActive(false) })
	req := httptest.NewRequest(http.MethodGet, "/sub/abc", nil)
	SubEdgeRecorder().Record(req, "abc", http.Header{}, []byte("vless://a"))

	(&SubEdgeService{}).PushCaches(context.Background())
	mu.Lock()
	fail = false
	mu.Unlock()
	(&SubEdgeService{}).PushCaches(context.Background())
	(&SubEdgeService{}).PushCaches(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("node took %d updates, want 2", len(got))
	}
	if len(got[0].Responses) != 1 || string(got[0].Responses[0].Body) != "vless://a" || strings.Join(got[0].SubIDs, ",") != "abc" {
		t.Fatalf("first update after the failure = %+v, want the response the node missed and only the enabled subscription", got[0])
	}
	if len(got[1].Responses) != 0 {
		t.Fatalf("second update resent %d responses, want none", len(got[1].Responses))
	}
}
