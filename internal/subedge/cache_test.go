package subedge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func cachedHappResponse(age time.Duration) CachedResponse {
	return CachedResponse{
		Host:   "sub.example.com",
		URI:    "/sub/abc",
		Agent:  "happ",
		SubID:  "abc",
		Header: map[string][]string{"Profile-Title": {"My VPN"}},
		Body:   []byte("vless://cached"),
		Stored: time.Now().Add(-age).Unix(),
	}
}

func edgeRequest(t *testing.T, node *Node, path string, header http.Header) *http.Response {
	t.Helper()
	edge := httptest.NewTLSServer(node)
	t.Cleanup(edge.Close)
	req, err := http.NewRequest(http.MethodGet, edge.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "sub.example.com"
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := edge.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestEdgeAnswersFromCacheWhenNoMasterDoes(t *testing.T) {
	old := masterAnswerTimeout
	masterAnswerTimeout = 200 * time.Millisecond
	t.Cleanup(func() { masterAnswerTimeout = old })

	masters := map[string]func(t *testing.T, node *Node){
		"no master tunnel": func(*testing.T, *Node) {},
		"the master answers 502": func(t *testing.T, node *Node) {
			connectMaster(t, node, "m1", 1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			}))
		},
		"the master hangs": func(t *testing.T, node *Node) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			connectMaster(t, node, "m1", 1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				<-release
			}))
		},
	}
	for name, setup := range masters {
		t.Run(name, func(t *testing.T) {
			node := &Node{}
			if err := node.UpdateCache(CacheUpdate{SubIDs: []string{"abc"}, Responses: []CachedResponse{cachedHappResponse(time.Hour)}}); err != nil {
				t.Fatal(err)
			}
			setup(t, node)

			resp := edgeRequest(t, node, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}})

			body, _ := io.ReadAll(resp.Body)
			age, _ := strconv.Atoi(resp.Header.Get("Age"))
			if resp.StatusCode != http.StatusOK || string(body) != "vless://cached" || resp.Header.Get("Profile-Title") != "My VPN" {
				t.Fatalf("edge answered %d %q %v, want the cached Happ response", resp.StatusCode, body, resp.Header)
			}
			if age < 3600 || age > 3660 || resp.Header.Get("Warning") != staleWarning {
				t.Fatalf("Age %q, Warning %q; want about 3600 and %q", resp.Header.Get("Age"), resp.Header.Get("Warning"), staleWarning)
			}
		})
	}
}

func TestEdgeCacheAnswersOnlyTheSameClient(t *testing.T) {
	bound := cachedHappResponse(time.Hour)
	bound.Device = deviceOf("phone-1")
	cases := []struct {
		name   string
		stored CachedResponse
		subIDs []string
		path   string
		header http.Header
		want   int
	}{
		{"another app", cachedHappResponse(time.Hour), []string{"abc"}, "/sub/abc", http.Header{"User-Agent": {"clash-verge/v2"}}, http.StatusServiceUnavailable},
		{"another query", cachedHappResponse(time.Hour), []string{"abc"}, "/sub/abc?format=info", http.Header{"User-Agent": {"Happ/3.1.0"}}, http.StatusServiceUnavailable},
		{"older than the cache keeps", cachedHappResponse(CacheMaxAge + time.Hour), []string{"abc"}, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}}, http.StatusServiceUnavailable},
		{"a subscription the master no longer serves", cachedHappResponse(time.Hour), []string{"other"}, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}}, http.StatusServiceUnavailable},
		{"another device under the HWID gate", bound, []string{"abc"}, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}, "X-Hwid": {"phone-2"}}, http.StatusServiceUnavailable},
		{"the same device under the HWID gate", bound, []string{"abc"}, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}, "X-Hwid": {"phone-1"}}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &Node{}
			if err := node.UpdateCache(CacheUpdate{SubIDs: tc.subIDs, Responses: []CachedResponse{tc.stored}}); err != nil {
				t.Fatal(err)
			}
			if resp := edgeRequest(t, node, tc.path, tc.header); resp.StatusCode != tc.want {
				t.Fatalf("edge answered %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestEdgeCacheSurvivesANodeRestart(t *testing.T) {
	dir := t.TempDir()
	first := &Node{CacheDir: func() string { return dir }}
	expired := cachedHappResponse(CacheMaxAge + time.Hour)
	expired.Agent = "v2rayng"
	if err := first.UpdateCache(CacheUpdate{SubIDs: []string{"abc"}, Responses: []CachedResponse{cachedHappResponse(time.Hour), expired}}); err != nil {
		t.Fatal(err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 1 {
		t.Fatalf("cache folder holds %d files, want only the unexpired response", len(files))
	}

	restarted := &Node{CacheDir: func() string { return dir }}
	resp := edgeRequest(t, restarted, "/sub/abc", http.Header{"User-Agent": {"Happ/3.1.0"}})
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusOK || string(body) != "vless://cached" {
		t.Fatalf("restarted edge answered %d %q, want the cached response", resp.StatusCode, body)
	}
}

func TestRecorderHandsEachResponseOverOnce(t *testing.T) {
	rec := &Recorder{}
	get := func(ua string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/sub/abc", nil)
		r.Host = "sub.example.com:443"
		r.Header.Set("User-Agent", ua)
		return r
	}
	rec.Record(get("Clash/1.0"), "abc", http.Header{}, []byte("ignored while inactive"))
	rec.SetActive(true)
	rec.Record(get("Happ/1.0"), "abc", http.Header{}, []byte("first"))
	rec.Record(get("Happ/1.1"), "abc", http.Header{}, []byte("second"))

	got, next := rec.Since(0)
	if len(got) != 1 || string(got[0].Body) != "second" {
		t.Fatalf("Since(0) = %+v, want only the latest Happ response", got)
	}
	if again, _ := rec.Since(next); len(again) != 0 {
		t.Fatalf("Since(next) = %+v, want nothing new", again)
	}
	rec.Record(get("v2rayNG/1.9"), "abc", http.Header{}, []byte("third"))
	if got, _ := rec.Since(next); len(got) != 1 || string(got[0].Body) != "third" {
		t.Fatalf("Since(next) after a new app = %+v, want its response", got)
	}
}

func TestSplitCacheUpdateKeepsEachUpdateUnderTheBodyLimit(t *testing.T) {
	big := make([]byte, maxCacheUpdate/2+1)
	responses := []CachedResponse{{Body: big}, {Body: big}, {Body: big}}
	updates := SplitCacheUpdate([]string{"abc"}, responses)
	if len(updates) != 3 {
		t.Fatalf("got %d updates, want 3", len(updates))
	}
	for i, u := range updates {
		if len(u.Responses) != 1 || len(u.SubIDs) != 1 {
			t.Fatalf("update %d = %d responses, subIds %v", i, len(u.Responses), u.SubIDs)
		}
	}
	if none := SplitCacheUpdate([]string{"abc"}, nil); len(none) != 1 || len(none[0].SubIDs) != 1 {
		t.Fatalf("without responses got %+v, want one update carrying the subscriptions", none)
	}
}

func TestCachedResponseExpiresWhileStored(t *testing.T) {
	cache := &responseCache{}
	if err := cache.update(CacheUpdate{SubIDs: []string{"abc"}, Responses: []CachedResponse{cachedHappResponse(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/sub/abc", nil)
	r.Host = "sub.example.com"
	r.Header.Set("User-Agent", "Happ/3.1.0")
	if _, ok := cache.lookup(r, time.Now()); !ok {
		t.Fatal("a fresh response missed the cache")
	}
	if _, ok := cache.lookup(r, time.Now().Add(CacheMaxAge)); ok {
		t.Fatal("a response older than CacheMaxAge was still served")
	}
}
