package sub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// edgeAfterMasterAnswered is an edge node holding what this master's router
// recorded, with no master reachable.
func edgeAfterMasterAnswered(t *testing.T, subID string) *subedge.Node {
	t.Helper()
	responses, _ := service.SubEdgeRecorder().Since(0)
	node := &subedge.Node{}
	if err := node.UpdateCache(subedge.CacheUpdate{SubIDs: []string{subID}, Responses: responses}); err != nil {
		t.Fatal(err)
	}
	return node
}

func edgeAnswer(node *subedge.Node, path, userAgent, hwid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "sub.example.com"
	req.Header.Set("User-Agent", userAgent)
	if hwid != "" {
		req.Header.Set("X-HWID", hwid)
	}
	rec := httptest.NewRecorder()
	node.ServeHTTP(rec, req)
	return rec
}

func TestEdgeCacheServesWhatTheMasterAnswered(t *testing.T) {
	t.Run("each client app gets its own format", func(t *testing.T) {
		recordForEdges(t)
		router, subID := initHwidSubRouter(t, 0)
		clash := requestSubAs(t, router, "/sub/"+subID, "clash-verge/v2.0", "")
		raw := requestSubAs(t, router, "/sub/"+subID, "v2rayNG/1.9", "")
		requestSub(t, router, http.MethodGet, "/sub/"+subID+"?html=1", "", "")
		requestSub(t, router, http.MethodGet, "/sub/unknown", "", "")
		if clash.Code != http.StatusOK || raw.Code != http.StatusOK || clash.Body.String() == raw.Body.String() {
			t.Fatalf("master answered %d and %d with the same body; the test needs two formats", clash.Code, raw.Code)
		}

		node := edgeAfterMasterAnswered(t, subID)
		if got := edgeAnswer(node, "/sub/"+subID, "clash-verge/v2.1", ""); got.Code != http.StatusOK || got.Body.String() != clash.Body.String() {
			t.Fatalf("edge gave Clash %d %q, want the master's Clash body", got.Code, got.Body.String())
		}
		if got := edgeAnswer(node, "/sub/"+subID, "v2rayNG/1.9.1", ""); got.Code != http.StatusOK || got.Body.String() != raw.Body.String() {
			t.Fatalf("edge gave v2rayNG %d %q, want the master's raw body", got.Code, got.Body.String())
		}
		for _, path := range []string{"/sub/" + subID + "?html=1", "/sub/unknown"} {
			if got := edgeAnswer(node, path, "", ""); got.Code != http.StatusServiceUnavailable {
				t.Fatalf("edge answered %s with %d; an HTML page or a 404 must not be cached", path, got.Code)
			}
		}
	})

	t.Run("under the HWID gate only the device that fetched", func(t *testing.T) {
		recordForEdges(t)
		router, subID := initHwidSubRouter(t, 1)
		if rec := requestSubAs(t, router, "/sub/"+subID, "Happ/3.0", "phone-one"); rec.Code != http.StatusOK {
			t.Fatalf("master answered %d", rec.Code)
		}
		if rec := requestSubAs(t, router, "/sub/"+subID, "Happ/3.0", "phone-two"); rec.Code != http.StatusNotFound {
			t.Fatalf("master answered the device over the limit with %d, want 404", rec.Code)
		}

		node := edgeAfterMasterAnswered(t, subID)
		if got := edgeAnswer(node, "/sub/"+subID, "Happ/3.0", "phone-one"); got.Code != http.StatusOK {
			t.Fatalf("edge answered the device that fetched with %d, want 200", got.Code)
		}
		if got := edgeAnswer(node, "/sub/"+subID, "Happ/3.0", "phone-two"); got.Code != http.StatusServiceUnavailable {
			t.Fatalf("edge answered another device with %d, want 503", got.Code)
		}
	})
}

func recordForEdges(t *testing.T) {
	t.Helper()
	service.SubEdgeRecorder().SetActive(true)
	t.Cleanup(func() { service.SubEdgeRecorder().SetActive(false) })
}

func requestSubAs(t *testing.T, router http.Handler, path, userAgent, hwid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "sub.example.com"
	req.Header.Set("User-Agent", userAgent)
	if hwid != "" {
		req.Header.Set("X-HWID", hwid)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestTeeWriterKeepsStringWrites(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	w := &teeWriter{ResponseWriter: c.Writer}
	_, _ = io.WriteString(w, "vless://a\n")
	_, _ = w.Write([]byte("vless://b\n"))
	if got := w.body.String(); got != "vless://a\nvless://b\n" {
		t.Fatalf("captured %q, want both writes", got)
	}
}
