package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
)

// A refused change must reach the admin as the translated reason naming the
// master to fix, not as the service's internal error text.
func TestMasterChangeRefusalIsTranslated(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Setenv("XUI_MASTER_NAME", "a")
	t.Setenv("XUI_MASTER_PRIORITY", "1")
	now := time.Now().Unix()
	for _, m := range []model.MasterMember{
		{Name: "a", Priority: 1, UpSince: now - 3600, LastSeen: now},
		{Name: "b", Priority: 2, UpSince: 1, LastSeen: 1},
	} {
		if err := database.GetDB().Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, params ...string) string {
			return strings.Join(append([]string{key}, params...), " ")
		})
		c.Next()
	})
	NewMasterClusterController(router.Group("/panel/api/masters"))

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"names":["b","a"]}`)
	req := httptest.NewRequest(http.MethodPost, "/panel/api/masters/reorder", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	var got struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	want := "pages.nodes.masters.toasts.refused (pages.nodes.masters.refusals.offline Name==b)"
	if got.Success || got.Msg != want {
		t.Fatalf("response = %+v, want refusal %q", got, want)
	}
}
