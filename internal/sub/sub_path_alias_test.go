package sub

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Renaming subPath broke every link already handed out (#21); an old path kept
// as an alias must keep serving the same raw subscription.
func TestSubPathAliasServesOldLinks(t *testing.T) {
	initSubDB(t)
	subID := "alias-sub"
	seedClashQuotaSub(t, subID, time.Now().Add(24*time.Hour).UnixMilli())

	oldDistFS := distFS
	distFS = testDistFS
	t.Cleanup(func() { distFS = oldDistFS })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Aliases equal to a configured path would make gin panic on a duplicate route.
	NewSUBController(router.Group("/"),
		WithSUBPath("/new/"),
		WithSUBPathAliases([]string{"/old/", "/new/", "/clash/"}),
		WithSUBClashEnabled(true),
		WithSUBEncryption(false),
	)

	fresh := fetchClashSub(t, router, "/new/"+subID+"?view=raw")
	if fresh.Code != http.StatusOK {
		t.Fatalf("new path status = %d, want 200", fresh.Code)
	}
	old := fetchClashSub(t, router, "/old/"+subID+"?view=raw")
	if old.Code != http.StatusOK || old.Body.String() != fresh.Body.String() {
		t.Fatalf("old path = %d %q, want the new path's 200 %q", old.Code, old.Body.String(), fresh.Body.String())
	}
	if got := fetchClashSub(t, router, "/gone/"+subID).Code; got != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", got)
	}
}
