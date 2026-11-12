package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
)

// A refused save must say nothing was saved; it used to read "AI domains saved (reason)".
func TestAIDomainsSaveRefusalSaysNothingWasSaved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewAIDomainsController(router.Group("/aiDomains"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/aiDomains/save", strings.NewReader(`{"domains":"openai.com, regexp:("}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	var res struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if want := "pages.aiDomains.notSaved (AI domains: not a domain rule: regexp:()"; res.Success || res.Msg != want {
		t.Fatalf("response = %+v, want failure %q", res, want)
	}
}
