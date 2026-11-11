package speedtest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	ookla "github.com/showwin/speedtest-go/speedtest"
)

// Ookla measures against the nearest-by-latency Speedtest.net server.
type Ookla struct {
	Doer    *http.Client
	Capture time.Duration
}

const (
	ooklaCapture = 10 * time.Second
	// minStreams keeps a 1-CPU VPS from measuring a gigabit link over one TCP stream.
	minStreams = 4
)

func (o *Ookla) Name() string { return ProviderOokla }

func (o *Ookla) Measure(ctx context.Context) (Result, error) {
	var opts []ookla.Option
	if o.Doer != nil {
		opts = append(opts, ookla.WithDoer(o.Doer))
	}
	client := ookla.New(opts...)
	capture := o.Capture
	if capture <= 0 {
		capture = ooklaCapture
	}
	client.SetCaptureTime(capture)
	client.SetNThread(max(runtime.NumCPU(), minStreams))

	servers, err := client.FetchServerListContext(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("server list: %w", err)
	}
	picked, err := servers.FindServer(nil)
	if err != nil {
		return Result{}, err
	}
	s := picked[0]
	if s.Latency <= 0 {
		return Result{}, errors.New("no server answered")
	}
	if err := s.PingTestContext(ctx, nil); err != nil {
		return Result{}, fmt.Errorf("ping %s: %w", s.Host, err)
	}
	if err := s.DownloadTestContext(ctx); err != nil {
		return Result{}, fmt.Errorf("download from %s: %w", s.Host, err)
	}
	if err := s.UploadTestContext(ctx); err != nil {
		return Result{}, fmt.Errorf("upload to %s: %w", s.Host, err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{
		Server:       strings.Trim(s.Sponsor+", "+s.Name, ", "),
		PingMs:       roundTenth(float64(s.Latency) / float64(time.Millisecond)),
		DownloadMbps: roundTenth(max(s.DLSpeed.Mbps(), 0)),
		UploadMbps:   roundTenth(max(s.ULSpeed.Mbps(), 0)),
	}, nil
}
