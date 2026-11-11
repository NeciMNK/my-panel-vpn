// Package speedtest measures a server's own internet link: Speedtest by Ookla
// first, Yandex Internetometer when Ookla is unreachable (e.g. from Russia).
package speedtest

import (
	"context"
	"errors"
	"fmt"
)

const (
	ProviderOokla  = "speedtest"
	ProviderYandex = "yandex"
)

// Result is one finished measurement of the server's link.
type Result struct {
	Provider     string  `json:"provider" example:"speedtest"`
	Server       string  `json:"server" example:"Rostelecom, Moscow"`
	PingMs       float64 `json:"pingMs" example:"4.2"`
	DownloadMbps float64 `json:"downloadMbps" example:"930.5"`
	UploadMbps   float64 `json:"uploadMbps" example:"910.1"`
}

// Measurer is one speed-test service.
type Measurer interface {
	Name() string
	Measure(ctx context.Context) (Result, error)
}

// Default is the order a server tries the services in.
func Default() []Measurer { return []Measurer{&Ookla{}, &Yandex{}} }

// Measure returns the first service's complete result, falling through to the
// next one when a service fails or measures nothing in either direction.
func Measure(ctx context.Context, measurers ...Measurer) (Result, error) {
	var errs []error
	for _, m := range measurers {
		r, err := m.Measure(ctx)
		if err == nil && (r.DownloadMbps <= 0 || r.UploadMbps <= 0) {
			err = errors.New("measured no download or no upload")
		}
		if err == nil {
			r.Provider = m.Name()
			return r, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return Result{}, errors.New("no speed-test service configured")
	}
	return Result{}, errors.Join(errs...)
}
