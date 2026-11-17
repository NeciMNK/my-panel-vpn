package service

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const grpcRealityStream = `{
  "network": "grpc",
  "security": "reality",
  "realitySettings": {"show": false, "target": "www.example.com:443", "serverNames": ["www.example.com"],
    "privateKey": "aGVsbG8taGVsbG8taGVsbG8taGVsbG8taGVsbG8taGU", "shortIds": ["ab12"],
    "settings": {"publicKey": "pub", "fingerprint": "chrome"}},
  "grpcSettings": {"serviceName": "mysvc", "authority": "cdn.example.com", "multiMode": true}
}`

// Xray 26.10 deprecates the gRPC transport; the copy must keep port-independent
// identity (clients, REALITY keys) while swapping only the transport.
func TestCopyGrpcInboundAsXhttp(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	clients := []model.Client{
		{Email: "alice@x", ID: "11111111-1111-1111-1111-111111111111", SubID: "sa", Enable: true},
		{Email: "bob@x", ID: "22222222-2222-2222-2222-222222222222", SubID: "sb", Enable: true},
	}
	src := mkInbound(t, 21001, model.VLESS, clientsSettings(t, clients))
	src.StreamSettings = grpcRealityStream
	src.Remark = "NL gRPC"
	if err := database.GetDB().Save(src).Error; err != nil {
		t.Fatalf("save source stream: %v", err)
	}
	if err := clientSvc.SyncInbound(nil, src.Id, clients); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}

	copied, _, err := inboundSvc.CopyGrpcInboundAsXhttp(src.Id, 21002)
	if err != nil {
		t.Fatalf("CopyGrpcInboundAsXhttp: %v", err)
	}
	if copied.Port != 21002 || !copied.Enable || copied.Remark != "NL gRPC (XHTTP)" {
		t.Fatalf("copy = port %d enable %v remark %q", copied.Port, copied.Enable, copied.Remark)
	}

	got, err := inboundSvc.GetInbound(copied.Id)
	if err != nil {
		t.Fatalf("GetInbound(copy): %v", err)
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(got.StreamSettings), &stream); err != nil {
		t.Fatalf("copy stream: %v", err)
	}
	if stream["network"] != "xhttp" || stream["security"] != "reality" {
		t.Fatalf("copy network/security = %v/%v", stream["network"], stream["security"])
	}
	if _, left := stream["grpcSettings"]; left {
		t.Fatal("copy still carries grpcSettings")
	}
	xh, _ := stream["xhttpSettings"].(map[string]any)
	if xh["path"] != "/mysvc" || xh["host"] != "cdn.example.com" || xh["mode"] != "auto" {
		t.Fatalf("xhttpSettings = %v", xh)
	}
	rs, _ := stream["realitySettings"].(map[string]any)
	if rs["privateKey"] != "aGVsbG8taGVsbG8taGVsbG8taGVsbG8taGVsbG8taGU" {
		t.Fatalf("REALITY key not kept: %v", rs["privateKey"])
	}

	linked, err := clientSvc.ListForInbound(nil, copied.Id)
	if err != nil {
		t.Fatalf("ListForInbound(copy): %v", err)
	}
	ids := make([]string, 0, len(linked))
	for _, c := range linked {
		ids = append(ids, c.Email+"="+c.ID)
	}
	sort.Strings(ids)
	want := []string{"alice@x=" + clients[0].ID, "bob@x=" + clients[1].ID}
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("copy clients = %v, want %v", ids, want)
	}

	still, err := inboundSvc.GetInbound(src.Id)
	if err != nil {
		t.Fatalf("GetInbound(source): %v", err)
	}
	if still.StreamSettings != grpcRealityStream || !still.Enable {
		t.Fatal("source inbound changed; it must keep serving gRPC clients until removed")
	}
}

func TestCopyGrpcInboundAsXhttp_RefusesNonGrpc(t *testing.T) {
	setupBulkDB(t)
	src := mkInbound(t, 21011, model.VLESS, `{"clients":[]}`)
	src.StreamSettings = `{"network":"tcp","security":"none"}`
	if err := database.GetDB().Save(src).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	_, _, err := (&InboundService{}).CopyGrpcInboundAsXhttp(src.Id, 21012)
	if err == nil || err.Error() != ErrInboundNotGrpc.Error() {
		t.Fatalf("err = %v, want %v", err, ErrInboundNotGrpc)
	}
}

func TestGrpcStreamToXhttp_Path(t *testing.T) {
	cases := []struct{ serviceName, want string }{
		{"mysvc", "/mysvc"},
		{"/already", "/already"},
		{"", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.serviceName, func(t *testing.T) {
			in := `{"network":"grpc","grpcSettings":{"serviceName":"` + tc.serviceName + `"}}`
			out, err := grpcStreamToXhttp(in)
			if err != nil {
				t.Fatalf("grpcStreamToXhttp: %v", err)
			}
			var stream map[string]any
			if err := json.Unmarshal([]byte(out), &stream); err != nil {
				t.Fatal(err)
			}
			if p := stream["xhttpSettings"].(map[string]any)["path"]; p != tc.want {
				t.Fatalf("path = %v, want %s", p, tc.want)
			}
		})
	}
}
