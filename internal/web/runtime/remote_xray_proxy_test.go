package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The node path is passed with a leading slash; the error named "xray//homeTunnel".
func TestProxyXrayErrorNamesTheNodePathOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()
	r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)
	_, err := r.ProxyXray(context.Background(), http.MethodPost, "/homeTunnel", "", "application/json", []byte("{}"))
	if err == nil || !strings.HasPrefix(err.Error(), "POST xray/homeTunnel: ") {
		t.Fatalf("err = %v", err)
	}
}
