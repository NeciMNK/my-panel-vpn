package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
)

// planNode is a node panel that keeps the DNS plans the master sends it.
func planNode(t *testing.T, name, country string) (*model.Node, *[]subedge.DNSPlan) {
	t.Helper()
	var mu sync.Mutex
	plans := &[]subedge.DNSPlan{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/subEdges/node/dns") {
			var plan subedge.DNSPlan
			_ = json.NewDecoder(r.Body).Decode(&plan)
			mu.Lock()
			*plans = append(*plans, plan)
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"success":true,"msg":""}`))
	}))
	t.Cleanup(srv.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	portNum, _ := strconv.Atoi(port)
	node := &model.Node{Name: name, Country: country, Scheme: "http", Address: host, Port: portNum, BasePath: "/", ApiToken: "tok", Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "verify"}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.SubEdge{NodeId: node.Id, Enable: true, Port: 443}).Error; err != nil {
		t.Fatal(err)
	}
	return node, plans
}

func TestSwitchDNSOrdersRussianEdgesFirstAndTellsEachWhoComesBefore(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	useTestRuntimeManager(t)
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"Invalid API Token"}]}`))
	}))
	t.Cleanup(cf.Close)
	cloudflareAPI = cf.URL
	t.Cleanup(func() { cloudflareAPI = "" })

	svc := &SubEdgeService{}
	if err := svc.SaveDNS(SubEdgeDNSRequest{Domain: "Sub.Example.com.", Token: "tok", MasterIPs: []string{"198.51.100.1"}}); err != nil {
		t.Fatal(err)
	}
	_, foreignPlans := planNode(t, "de-1", "DE")
	_, ruPlans := planNode(t, "ru-1", "RU")

	svc.SwitchDNS(context.Background())

	if len(*ruPlans) != 1 || len(*foreignPlans) != 1 {
		t.Fatalf("nodes got %d and %d plans, want one each", len(*ruPlans), len(*foreignPlans))
	}
	ru, foreign := (*ruPlans)[0], (*foreignPlans)[0]
	wantTiers := [][]string{{"198.51.100.1"}, {"127.0.0.1"}, {"127.0.0.1"}}
	if ru.Domain != "sub.example.com" || !slices.EqualFunc(ru.Tiers, wantTiers, slices.Equal) {
		t.Fatalf("RU plan = %+v, want domain sub.example.com and tiers %v", ru, wantTiers)
	}
	if len(ru.EdgesBefore) != 0 || !slices.Equal(foreign.EdgesBefore, []string{"127.0.0.1"}) {
		t.Fatalf("edges before: RU %v, foreign %v; want none for RU and the RU edge for the foreign one", ru.EdgesBefore, foreign.EdgesBefore)
	}
	view, err := svc.DNSView()
	if err != nil || !strings.Contains(view.Status.Error, "Invalid API Token") || !view.TokenSet {
		t.Fatalf("view = %+v, %v; want the token set and Cloudflare's error in the status", view, err)
	}
}

func TestSaveDNSRefusesNonIPv4MasterAddresses(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	err := (&SubEdgeService{}).SaveDNS(SubEdgeDNSRequest{Domain: "sub.example.com", MasterIPs: []string{"panel.example.com"}})
	if err == nil || !strings.Contains(err.Error(), "not an IPv4 address") {
		t.Fatalf("SaveDNS = %v, want an IPv4 refusal", err)
	}
}

func TestDNSPlanUsesTheTLSCloudflareTokenWhenNoOwnIsSaved(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SubEdgeService{}
	if err := (&SettingService{}).SetTlsCloudflareToken("tls-token"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDNS(SubEdgeDNSRequest{Domain: "sub.example.com"}); err != nil {
		t.Fatal(err)
	}
	plan, _, err := svc.dnsPlan(context.Background())
	if err != nil || plan.Token != "tls-token" || !plan.Enabled() {
		t.Fatalf("plan = %+v, %v; want the TLS token and switching on", plan, err)
	}
	if view, _ := svc.DNSView(); !view.TokenFromTLS || view.TokenSet {
		t.Fatalf("view = %+v, want the TLS token shown as the one in use", view)
	}
	if err := svc.SaveDNS(SubEdgeDNSRequest{Domain: "sub.example.com", Token: "dns-token"}); err != nil {
		t.Fatal(err)
	}
	if plan, _, _ := svc.dnsPlan(context.Background()); plan.Token != "dns-token" {
		t.Fatalf("plan token = %q, want the separate DNS token first", plan.Token)
	}
}
