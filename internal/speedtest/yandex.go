package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Yandex measures with Yandex Internetometer's probes (yandex.ru/internet), the
// protocol github.com/Master290/internetometer-cli (MIT) documents.
type Yandex struct {
	ProbesURL string
	Doer      *http.Client
	Phase     time.Duration
	Warmup    time.Duration
}

const (
	yandexProbesURL = "https://yandex.ru/internet/api/v0/get-probes"
	yandexUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	yandexPhase     = 10 * time.Second
	yandexWarmup    = time.Second
	yandexPings     = 5
	// yandexUploadChunk is one POST body; Internetometer's own probes are 50 MB.
	yandexUploadChunk = 8 << 20
)

type yandexProbe struct {
	URL  string `json:"url"`
	Size int    `json:"size"`
}

type yandexProbes struct {
	Latency  struct{ Probes []yandexProbe } `json:"latency"`
	Download struct{ Probes []yandexProbe } `json:"download"`
	Upload   struct{ Probes []yandexProbe } `json:"upload"`
}

func (y *Yandex) Name() string { return ProviderYandex }

func (y *Yandex) doer() *http.Client {
	if y.Doer != nil {
		return y.Doer
	}
	return http.DefaultClient
}

func (y *Yandex) Measure(ctx context.Context) (Result, error) {
	probes, err := y.fetchProbes(ctx)
	if err != nil {
		return Result{}, err
	}
	down := largestProbe(probes.Download.Probes)
	if down == "" || len(probes.Upload.Probes) == 0 {
		return Result{}, errors.New("probe list has no download or upload target")
	}
	up := probes.Upload.Probes[0].URL
	ping, err := y.ping(ctx, probes.Latency.Probes)
	if err != nil {
		return Result{}, err
	}
	phase, warmup := y.Phase, y.Warmup
	if phase <= 0 {
		phase, warmup = yandexPhase, yandexWarmup
	}
	downMbps, err := measureStreams(ctx, phase, warmup, func(ctx context.Context, n *atomic.Int64) error {
		return y.download(ctx, down, n)
	})
	if err != nil {
		return Result{}, fmt.Errorf("download: %w", err)
	}
	upMbps, err := measureStreams(ctx, phase, warmup, func(ctx context.Context, n *atomic.Int64) error {
		return y.upload(ctx, up, n)
	})
	if err != nil {
		return Result{}, fmt.Errorf("upload: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	host := down
	if u, err := url.Parse(down); err == nil {
		host = u.Hostname()
	}
	return Result{Server: host, PingMs: ping, DownloadMbps: downMbps, UploadMbps: upMbps}, nil
}

func (y *Yandex) request(ctx context.Context, method, target string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", yandexUA)
	req.Header.Set("Referer", "https://yandex.ru/internet")
	req.Header.Set("Origin", "https://yandex.ru")
	return y.doer().Do(req)
}

func (y *Yandex) fetchProbes(ctx context.Context) (*yandexProbes, error) {
	target := y.ProbesURL
	if target == "" {
		target = yandexProbesURL
	}
	resp, err := y.request(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("probe list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe list: HTTP %d", resp.StatusCode)
	}
	out := &yandexProbes{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return nil, fmt.Errorf("probe list: %w", err)
	}
	return out, nil
}

// largestProbe prefers the 50 MB file, then anything but the 100 KB one.
func largestProbe(probes []yandexProbe) string {
	pick := ""
	for _, p := range probes {
		switch {
		case p.URL == "":
		case strings.Contains(p.URL, "50mb"):
			return p.URL
		case pick == "" || !strings.Contains(p.URL, "100kb"):
			pick = p.URL
		}
	}
	return pick
}

// ping takes the fastest latency probe, then the best of a few round trips to it.
func (y *Yandex) ping(ctx context.Context, probes []yandexProbe) (float64, error) {
	best, bestURL := time.Duration(math.MaxInt64), ""
	for _, p := range probes {
		if d, ok := y.roundTrip(ctx, p.URL); ok && d < best {
			best, bestURL = d, p.URL
		}
	}
	if bestURL == "" {
		return 0, errors.New("no latency probe answered")
	}
	for range yandexPings {
		if d, ok := y.roundTrip(ctx, bestURL); ok && d < best {
			best = d
		}
	}
	return roundTenth(float64(best) / float64(time.Millisecond)), nil
}

func (y *Yandex) roundTrip(ctx context.Context, target string) (time.Duration, bool) {
	if target == "" {
		return 0, false
	}
	start := time.Now()
	resp, err := y.request(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return time.Since(start), resp.StatusCode == http.StatusOK
}

func (y *Yandex) download(ctx context.Context, target string, n *atomic.Int64) error {
	resp, err := y.request(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, &countingReader{r: resp.Body, n: n})
	return err
}

func (y *Yandex) upload(ctx context.Context, target string, n *atomic.Int64) error {
	body := &countingReader{r: io.LimitReader(zeroReader{}, yandexUploadChunk), n: n}
	resp, err := y.request(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// streams is how many parallel transfers one direction runs.
const streams = minStreams

// measureStreams runs transfer in parallel loops for phase and returns the
// Mbit/s moved after warmup, which skips TCP slow start.
func measureStreams(ctx context.Context, phase, warmup time.Duration, transfer func(context.Context, *atomic.Int64) error) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, phase)
	defer cancel()
	var moved atomic.Int64
	var lastErr atomic.Value
	var wg sync.WaitGroup
	for range streams {
		wg.Go(func() {
			for ctx.Err() == nil {
				if err := transfer(ctx, &moved); err != nil && ctx.Err() == nil {
					lastErr.Store(err)
					time.Sleep(200 * time.Millisecond)
				}
			}
		})
	}
	start := time.Now()
	var atWarm int64
	select {
	case <-time.After(warmup):
		atWarm = moved.Load()
	case <-ctx.Done():
	}
	warmedAt := time.Now()
	<-ctx.Done()
	end := moved.Load()
	elapsed := time.Since(warmedAt)
	wg.Wait()
	if end-atWarm <= 0 || elapsed <= 0 {
		if err, ok := lastErr.Load().(error); ok {
			return 0, err
		}
		if parent := context.Cause(ctx); parent != nil && !errors.Is(parent, context.DeadlineExceeded) {
			return 0, parent
		}
		return 0, fmt.Errorf("nothing transferred in %s", time.Since(start).Round(time.Second))
	}
	return roundTenth(float64(end-atWarm) * 8 / 1e6 / elapsed.Seconds()), nil
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	return k, err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func roundTenth(v float64) float64 { return math.Round(v*10) / 10 }
