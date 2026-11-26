package sub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// An entryless auto-select only exists as a JSON document, so Happ must get
// JSON from the plain link without the admin turning JSON on; others keep raw.
func TestEntrylessAutoSelectReachesHappWithoutJsonSettings(t *testing.T) {
	seedSubDB(t)
	db := database.GetDB()
	nl := &model.Node{Name: "nl", Address: "10.0.0.3", Port: 1, Enable: true, Country: "NL", Status: "online"}
	if err := db.Create(nl).Error; err != nil {
		t.Fatal(err)
	}
	ib := seedSubInbound(t, "s1", "nl-in", 4950, 1, `{"network":"tcp","security":"none"}`)
	db.Model(ib).Update("node_id", nl.Id)
	gin.SetMode(gin.TestMode)

	fetch := func(userAgent string) string {
		t.Helper()
		router := gin.New()
		NewSUBController(router.Group("/"), WithSUBEncryption(false))
		req := httptest.NewRequest(http.MethodGet, "http://sub.example.com/sub/s1", nil)
		req.Header.Set("User-Agent", userAgent)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		return resp.Body.String()
	}

	if body := fetch("Happ/1.2.0"); strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Fatalf("Happ got JSON with no entryless auto-select: %s", body)
	}
	seedSubBalancer(t, &model.SubBalancer{Remark: "Auto", Strategy: "leastPing", SortOrder: 1, Enabled: true, ExitForeign: true})

	body := fetch("Happ/1.2.0")
	docs := parseSubJsonDocs(t, body)
	auto := findDocByRemarks(docs, "Auto")
	if auto == nil {
		t.Fatalf("Happ list lacks the auto-select: %s", body)
	}
	routing := mustMarshal(t, auto["routing"])
	if !strings.Contains(routing, `"geosite:category-ru"`) || !strings.Contains(routing, `"geoip:ru"`) {
		t.Fatalf("auto-select lacks the cascade split (Russian sites direct): %s", routing)
	}
	if body := fetch("v2rayNG/1.9"); strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Fatalf("a non-Happ client was switched to JSON: %s", body)
	}
}
