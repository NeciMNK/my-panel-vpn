package service

import (
	"path/filepath"
	"strings"
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
