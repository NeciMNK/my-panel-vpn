package subedge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// CacheMaxAge is how long an edge keeps answering from a cached response.
	CacheMaxAge      = 7 * 24 * time.Hour
	maxCachedBody    = 4 << 20
	maxCacheUpdate   = 6 << 20
	headerHwid       = "X-Hwid"
	headerHwidActive = "X-Hwid-Active"
	staleWarning     = `110 - "Response is Stale"`
)

// CachedResponse is one subscription answer a master gave a client app, kept
// so an edge can still answer that app while no master does.
type CachedResponse struct {
	Host   string              `json:"host" example:"sub.example.com"`
	URI    string              `json:"uri" example:"/sub/abc123"`
	Agent  string              `json:"agent" example:"happ"`
	Device string              `json:"device" example:""`
	SubID  string              `json:"subId" example:"abc123"`
	Header map[string][]string `json:"header"`
	Body   []byte              `json:"body" example:"dmxlc3M6Ly8uLi4="`
	Stored int64               `json:"stored" example:"1762736400"`
}

// CacheUpdate is what a master sends an edge node: new responses, and every
// subscription that may still be served, so the rest is dropped.
type CacheUpdate struct {
	SubIDs    []string         `json:"subIds" example:"[\"abc123\"]"`
	Responses []CachedResponse `json:"responses"`
}

// agentFamily is the client app's product name: an app update must not miss
// the cache, while another app may need another format.
func agentFamily(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if i := strings.IndexAny(ua, "/ ;("); i >= 0 {
		ua = ua[:i]
	}
	return ua
}

func normalHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func deviceOf(hwid string) string {
	if hwid == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hwid))
	return hex.EncodeToString(sum[:])
}

func (c CachedResponse) key() string {
	return normalHost(c.Host) + "\x00" + c.URI + "\x00" + c.Agent + "\x00" + c.Device
}

// requestKeys are the cache keys that may answer r, device-bound one first:
// a response is bound to the device only when the HWID gate was on for it.
func requestKeys(r *http.Request) []string {
	base := CachedResponse{Host: r.Host, URI: r.URL.RequestURI(), Agent: agentFamily(r.UserAgent())}
	keys := []string{base.key()}
	if device := deviceOf(r.Header.Get(headerHwid)); device != "" {
		bound := base
		bound.Device = device
		keys = append([]string{bound.key()}, keys...)
	}
	return keys
}

// Recorder keeps, on a master, the latest response per cache key until every
// edge node got it.
type Recorder struct {
	mu     sync.Mutex
	active bool
	seq    uint64
	byKey  map[string]recordedResponse
}

type recordedResponse struct {
	resp CachedResponse
	seq  uint64
}

// SetActive turns recording on while this panel has edge nodes, and drops
// everything recorded when it has none.
func (r *Recorder) SetActive(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = on
	if !on {
		r.byKey = nil
	}
}

func (r *Recorder) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// Record keeps the 200 answer a client app got for subscription subID.
func (r *Recorder) Record(req *http.Request, subID string, header http.Header, body []byte) {
	if len(body) > maxCachedBody {
		return
	}
	resp := CachedResponse{
		Host:   req.Host,
		URI:    req.URL.RequestURI(),
		Agent:  agentFamily(req.UserAgent()),
		SubID:  subID,
		Header: storedHeader(header),
		Body:   bytes.Clone(body),
		Stored: time.Now().Unix(),
	}
	if strings.EqualFold(header.Get(headerHwidActive), "true") {
		resp.Device = deviceOf(req.Header.Get(headerHwid))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	if r.byKey == nil {
		r.byKey = map[string]recordedResponse{}
	}
	r.seq++
	r.byKey[resp.key()] = recordedResponse{resp: resp, seq: r.seq}
}

// Since returns the responses recorded after seq and the seq to ask with next.
func (r *Recorder) Since(seq uint64) ([]CachedResponse, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-CacheMaxAge).Unix()
	var out []CachedResponse
	for k, rec := range r.byKey {
		if rec.resp.Stored < cutoff {
			delete(r.byKey, k)
			continue
		}
		if rec.seq > seq {
			out = append(out, rec.resp)
		}
	}
	return out, r.seq
}

// SplitCacheUpdate cuts responses into updates that each fit a node's request
// body limit; there is always at least one, so the subscription list is sent.
func SplitCacheUpdate(subIDs []string, responses []CachedResponse) []CacheUpdate {
	updates := []CacheUpdate{{SubIDs: subIDs}}
	size := 0
	for _, resp := range responses {
		last := &updates[len(updates)-1]
		if size+len(resp.Body) > maxCacheUpdate && len(last.Responses) > 0 {
			updates = append(updates, CacheUpdate{SubIDs: subIDs})
			last = &updates[len(updates)-1]
			size = 0
		}
		last.Responses = append(last.Responses, resp)
		size += len(resp.Body)
	}
	return updates
}

var droppedCacheHeaders = []string{"Date", "Content-Length", "Set-Cookie", "Age", "Warning"}

func storedHeader(h http.Header) map[string][]string {
	out := h.Clone()
	for _, name := range append(droppedCacheHeaders, hopHeaders...) {
		out.Del(name)
	}
	return out
}

// responseCache is a node's cache, kept as one file per response so it
// survives a node restart while every master is down.
type responseCache struct {
	dir    func() string
	once   sync.Once
	mu     sync.Mutex
	byKey  map[string]CachedResponse
	folder string
}

func (c *responseCache) load() {
	c.once.Do(func() {
		c.byKey = map[string]CachedResponse{}
		if c.dir == nil {
			return
		}
		c.folder = c.dir()
		entries, err := os.ReadDir(c.folder)
		if err != nil {
			return
		}
		for _, e := range entries {
			raw, err := os.ReadFile(filepath.Join(c.folder, e.Name()))
			if err != nil {
				continue
			}
			var resp CachedResponse
			if json.Unmarshal(raw, &resp) == nil {
				c.byKey[resp.key()] = resp
			}
		}
	})
}

func (c *responseCache) update(u CacheUpdate) error {
	c.load()
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, resp := range u.Responses {
		k := resp.key()
		if old, ok := c.byKey[k]; ok && old.Stored > resp.Stored {
			continue
		}
		c.byKey[k] = resp
		errs = append(errs, c.write(k, resp))
	}
	keep := make(map[string]bool, len(u.SubIDs))
	for _, id := range u.SubIDs {
		keep[id] = true
	}
	cutoff := time.Now().Add(-CacheMaxAge).Unix()
	for k, resp := range c.byKey {
		if !keep[resp.SubID] || resp.Stored < cutoff {
			delete(c.byKey, k)
			errs = append(errs, c.remove(k))
		}
	}
	return errors.Join(errs...)
}

func (c *responseCache) lookup(r *http.Request, now time.Time) (CachedResponse, bool) {
	c.load()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range requestKeys(r) {
		if resp, ok := c.byKey[k]; ok && now.Unix()-resp.Stored <= int64(CacheMaxAge/time.Second) {
			return resp, true
		}
	}
	return CachedResponse{}, false
}

func fileOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".json"
}

func (c *responseCache) write(key string, resp CachedResponse) error {
	if c.folder == "" {
		return nil
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.folder, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.folder, ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(raw)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	return os.Rename(tmp.Name(), filepath.Join(c.folder, fileOf(key)))
}

func (c *responseCache) remove(key string) error {
	if c.folder == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(c.folder, fileOf(key))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func serveCached(w http.ResponseWriter, r *http.Request, resp CachedResponse, now time.Time) {
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("Age", strconv.FormatInt(max(0, now.Unix()-resp.Stored), 10))
	w.Header().Set("Warning", staleWarning)
	w.Header().Set("Content-Length", strconv.Itoa(len(resp.Body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(resp.Body)
	}
}
