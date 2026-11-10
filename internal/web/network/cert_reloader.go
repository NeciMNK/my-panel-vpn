package network

import (
	"crypto/tls"
	"os"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// CertReloader re-reads a certificate pair whenever either file's mtime
// changes, so a renewed certificate is served without restarting the listener.
type CertReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// NewCertReloader loads the pair once and fails when it is not a valid pair.
func NewCertReloader(certFile, keyFile string) (*CertReloader, error) {
	r := &CertReloader{certFile: certFile, keyFile: keyFile}
	certMod, keyMod, err := r.modTimes()
	if err != nil {
		return nil, err
	}
	if err := r.load(certMod, keyMod); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CertReloader) modTimes() (time.Time, time.Time, error) {
	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return certInfo.ModTime(), keyInfo.ModTime(), nil
}

func (r *CertReloader) load(certMod, keyMod time.Time) error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.cert, r.certMod, r.keyMod = &cert, certMod, keyMod
	return nil
}

// GetCertificate is a tls.Config.GetCertificate callback. A pair that fails to
// load (e.g. caught between the cert and key writes) keeps the previous one.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	certMod, keyMod, err := r.modTimes()
	if err == nil && (!certMod.Equal(r.certMod) || !keyMod.Equal(r.keyMod)) {
		if lerr := r.load(certMod, keyMod); lerr != nil {
			logger.Warning("Keeping the previous TLS certificate, reload failed:", lerr)
		} else {
			logger.Info("Reloaded TLS certificate from", r.certFile)
		}
	}
	return r.cert, nil
}
