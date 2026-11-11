package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMeasurer struct {
	name string
	res  Result
	err  error
}

func (f *fakeMeasurer) Name() string { return f.name }

func (f *fakeMeasurer) Measure(context.Context) (Result, error) { return f.res, f.err }

func TestMeasureFallsThroughToTheNextService(t *testing.T) {
	yandex := &fakeMeasurer{name: ProviderYandex, res: Result{Server: "ya", DownloadMbps: 90, UploadMbps: 80}}
	cases := []struct {
		name  string
		ookla *fakeMeasurer
		want  Result
	}{
		{
			"ookla works", &fakeMeasurer{name: ProviderOokla, res: Result{Server: "ok", DownloadMbps: 500, UploadMbps: 400}},
			Result{Provider: ProviderOokla, Server: "ok", DownloadMbps: 500, UploadMbps: 400},
		},
		{
			"ookla blocked", &fakeMeasurer{name: ProviderOokla, err: errors.New("dial tcp: blocked")},
			Result{Provider: ProviderYandex, Server: "ya", DownloadMbps: 90, UploadMbps: 80},
		},
		{
			"ookla measured no upload", &fakeMeasurer{name: ProviderOokla, res: Result{Server: "ok", DownloadMbps: 500}},
			Result{Provider: ProviderYandex, Server: "ya", DownloadMbps: 90, UploadMbps: 80},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Measure(context.Background(), c.ookla, yandex)
			if err != nil || got != c.want {
				t.Fatalf("Measure = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestMeasureNamesEveryFailedService(t *testing.T) {
	_, err := Measure(context.Background(),
		&fakeMeasurer{name: ProviderOokla, err: errors.New("blocked")},
		&fakeMeasurer{name: ProviderYandex, err: errors.New("HTTP 403")})
	if err == nil || err.Error() != "speedtest: blocked\nyandex: HTTP 403" {
		t.Fatalf("err = %v", err)
	}
}

// throttled serves an endless body at about bytesPerTick every 10ms per request.
func throttled(w http.ResponseWriter, r *http.Request, bytesPerTick int) {
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, bytesPerTick)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
		if _, err := w.Write(buf); err != nil {
			return
		}
		w.(http.Flusher).Flush()
	}
}

// streamCeilingMbps is what `streams` throttled transfers can reach at most.
func streamCeilingMbps(bytesPerTick int) float64 {
	return float64(streams*bytesPerTick*100) * 8 / 1e6
}

func TestYandexMeasuresThroughItsProbes(t *testing.T) {
	const tick = 32 << 10
	var uploaded atomic.Int64
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/internet/api/v0/get-probes", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"mid":"1","latency":{"probes":[{"url":"%[1]s/dead","timeout":1000},{"url":"%[1]s/ping","timeout":1000}]},
			"download":{"probes":[{"url":"%[1]s/100kb.bin"},{"url":"%[1]s/50mb.bin"}]},
			"upload":{"probes":[{"url":"%[1]s/upload","size":52428800}]}}`, srv.URL)
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/dead", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("/100kb.bin", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("/50mb.bin", func(w http.ResponseWriter, r *http.Request) { throttled(w, r, tick) })
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, tick)
		for {
			n, err := r.Body.Read(buf)
			uploaded.Add(int64(n))
			if err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	y := &Yandex{ProbesURL: srv.URL + "/internet/api/v0/get-probes", Doer: srv.Client(), Phase: 1500 * time.Millisecond, Warmup: 200 * time.Millisecond}
	got, err := y.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ceiling := streamCeilingMbps(tick)
	for dir, mbps := range map[string]float64{"download": got.DownloadMbps, "upload": got.UploadMbps} {
		if mbps < ceiling/2 || mbps > ceiling*1.3 {
			t.Fatalf("%s = %v Mbps, want near the throttled %v", dir, mbps, ceiling)
		}
	}
	if uploaded.Load() == 0 {
		t.Fatal("the upload probe received nothing")
	}
	if host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://")); got.Server != host {
		t.Fatalf("server = %q, want %q", got.Server, host)
	}
}

func TestYandexRejectsAProbeListWithoutUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"latency":{"probes":[]},"download":{"probes":[{"url":"http://x/50mb.bin"}]},"upload":{"probes":[]}}`)
	}))
	defer srv.Close()
	_, err := (&Yandex{ProbesURL: srv.URL, Doer: srv.Client()}).Measure(context.Background())
	if err == nil || err.Error() != "probe list has no download or upload target" {
		t.Fatalf("err = %v", err)
	}
}

// hostRewriter sends every request for a live host to srv and fails the rest.
type hostRewriter struct {
	srv  *url.URL
	live map[string]bool
}

func (h *hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "www.speedtest.net" && !h.live[r.URL.Host] {
		return nil, errors.New("connection refused")
	}
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = h.srv.Scheme, h.srv.Host
	return http.DefaultTransport.RoundTrip(r)
}

func ooklaServer(t *testing.T, tick int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/js/servers", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"url":"http://dead.example:8080/speedtest/upload.php","name":"Dead","sponsor":"Gone","id":"1","host":"dead.example:8080","distance":1},
			{"url":"http://live.example:8080/speedtest/upload.php","name":"Moscow","sponsor":"Rostelecom","id":"2","host":"live.example:8080","distance":5}]`)
	})
	mux.HandleFunc("/speedtest/latency.txt", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "test=test") })
	mux.HandleFunc("/speedtest/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.Copy(io.Discard, r.Body)
			return
		}
		throttled(w, r, tick)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestOoklaUsesTheServerThatAnswers(t *testing.T) {
	srv := ooklaServer(t, 32<<10)
	u, _ := url.Parse(srv.URL)
	o := &Ookla{Doer: &http.Client{Transport: &hostRewriter{srv: u, live: map[string]bool{"live.example:8080": true}}}, Capture: time.Second}
	got, err := o.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Server != "Rostelecom, Moscow" || got.DownloadMbps <= 0 || got.UploadMbps <= 0 {
		t.Fatalf("got %+v, want Rostelecom, Moscow with both directions measured", got)
	}
}

func TestOoklaFailsWhenNoServerAnswers(t *testing.T) {
	srv := ooklaServer(t, 32<<10)
	u, _ := url.Parse(srv.URL)
	o := &Ookla{Doer: &http.Client{Transport: &hostRewriter{srv: u}}, Capture: time.Second}
	if _, err := o.Measure(context.Background()); err == nil || err.Error() != "no server answered" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerJoinsTheRunningMeasurement(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	r := NewRunner(func(context.Context) (Result, error) {
		calls.Add(1)
		<-release
		return Result{Provider: ProviderOokla, DownloadMbps: 100, UploadMbps: 50}, nil
	})
	first := r.Start()
	second := r.Start()
	if !first.Running || second.Run != first.Run {
		t.Fatalf("second start = %+v, want to join run %d", second, first.Run)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for r.State().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	done := r.State()
	if done.Running || done.Result == nil || done.Result.DownloadMbps != 100 || calls.Load() != 1 {
		t.Fatalf("finished state = %+v after %d measurements", done, calls.Load())
	}
	if next := r.Start(); next.Run != first.Run+1 {
		t.Fatalf("next run = %d, want %d", next.Run, first.Run+1)
	}
}
