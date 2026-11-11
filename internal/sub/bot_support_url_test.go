package sub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Every client reads Support-Url, so an unset support link must still lead to
// the panel's bot instead of leaving the client without a contact button.
func TestSupportURLFallsBackToPanelBot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDistFS := distFS
	distFS = testDistFS
	t.Cleanup(func() { distFS = oldDistFS })
	for _, tc := range []struct {
		name       string
		botEnabled bool
		username   string
		override   string
		want       string
	}{
		{name: "bot link", botEnabled: true, username: "mnk_vpn_bot", want: "https://t.me/mnk_vpn_bot"},
		{name: "override wins", botEnabled: true, username: "mnk_vpn_bot", override: "https://t.me/mnk_support", want: "https://t.me/mnk_support"},
		{name: "bot disabled", username: "mnk_vpn_bot"},
		{name: "username unknown", botEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initSubDB(t)
			seedInfoEndpointSub(t, "bot-sub", "bot@example.com")
			settings := service.SettingService{}
			if err := settings.SetTgbotEnabled(tc.botEnabled); err != nil {
				t.Fatalf("enable bot: %v", err)
			}
			if err := settings.SetTgBotUsername(tc.username); err != nil {
				t.Fatalf("store bot username: %v", err)
			}
			router := gin.New()
			NewSUBController(router.Group("/"), WithSUBSupportURL(tc.override))
			for _, path := range []string{"/sub/bot-sub", "/sub/bot-sub?html=1"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Host = "sub.example.com"
				req.Header.Set("User-Agent", "v2rayNG/1.8.5")
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK {
					t.Fatalf("%s: status = %d, body=%s", path, resp.Code, resp.Body.String())
				}
				if path == "/sub/bot-sub" {
					if got := resp.Header().Get("Support-Url"); got != tc.want {
						t.Fatalf("Support-Url = %q, want %q", got, tc.want)
					}
					continue
				}
				// The page's support button reads subSupportUrl from the embedded data.
				hasLink := strings.Contains(resp.Body.String(), "t.me/")
				if hasLink != (tc.want != "") || (tc.want != "" && !strings.Contains(resp.Body.String(), tc.want)) {
					t.Fatalf("page support link: want %q, page has t.me link = %v", tc.want, hasLink)
				}
			}
		})
	}
}
