package panel

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

// A fork that only ships the rolling dev-latest build has no stable release:
// GitHub answers releases/latest with 404 and the update check must not fail.
func TestGetUpdateInfoFallsBackToDevWithoutStableRelease(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/tags/dev-latest":
			_, _ = w.Write([]byte(`{"tag_name":"dev-latest","body":"commit=0123456789abcdef0123456789abcdef01234567"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := panelReleaseAPI
	panelReleaseAPI = srv.URL + "/releases/"
	t.Cleanup(func() { panelReleaseAPI = prev })

	info, err := (&PanelService{}).GetUpdateInfo()
	if err != nil {
		t.Fatalf("GetUpdateInfo() error = %v, want the dev-latest build", err)
	}
	if info.Channel != "dev" || info.LatestCommit != "01234567" || !info.UpdateAvailable {
		t.Fatalf("GetUpdateInfo() = %+v, want dev channel at commit 01234567 with an update available", info)
	}
}

// Every fork tag before 1.0 is a pre-release, so releases/latest is always 404:
// the stable check must pick the newest vX.Y.Z tag instead of falling to dev (#6).
func TestGetUpdateInfoPicksNewestPreReleaseTag(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"dev-latest","prerelease":true},{"tag_name":"v0.0.10","prerelease":true},{"tag_name":"v0.0.9","prerelease":true},{"tag_name":"0.0.1","prerelease":true}]`))
		case "/releases/tags/dev-latest":
			_, _ = w.Write([]byte(`{"tag_name":"dev-latest","body":"commit=0123456789abcdef0123456789abcdef01234567"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := panelReleaseAPI
	panelReleaseAPI = srv.URL + "/releases/"
	t.Cleanup(func() { panelReleaseAPI = prev })

	info, err := (&PanelService{}).GetUpdateInfo()
	if err != nil {
		t.Fatalf("GetUpdateInfo() error = %v", err)
	}
	if info.Channel != "stable" || info.LatestVersion != "v0.0.10" {
		t.Fatalf("GetUpdateInfo() = %+v, want stable channel at v0.0.10", info)
	}
}
