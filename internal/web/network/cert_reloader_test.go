package network

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func selfSignedPEM(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writePair(t *testing.T, certFile, keyFile string, certPEM, keyPEM []byte, mod time.Time) {
	t.Helper()
	for path, data := range map[string][]byte{certFile: certPEM, keyFile: keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func servedCN(t *testing.T, r *CertReloader) string {
	t.Helper()
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

// A renewed pair on disk must be served without restarting the listener.
func TestCertReloaderServesRenewedPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	start := time.Now().Add(-time.Hour)
	oldCert, oldKey := selfSignedPEM(t, "old")
	writePair(t, certFile, keyFile, oldCert, oldKey, start)

	r, err := NewCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	if got := servedCN(t, r); got != "old" {
		t.Fatalf("served %q before renewal, want old", got)
	}

	newCert, newKey := selfSignedPEM(t, "new")
	writePair(t, certFile, keyFile, newCert, newKey, start.Add(time.Minute))
	if got := servedCN(t, r); got != "new" {
		t.Fatalf("served %q after renewal, want new", got)
	}
}

// A cert written before its key (mismatched pair) must not knock the listener
// over; the previous pair stays until both files agree again.
func TestCertReloaderKeepsPreviousPairOnMismatch(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	start := time.Now().Add(-time.Hour)
	oldCert, oldKey := selfSignedPEM(t, "old")
	writePair(t, certFile, keyFile, oldCert, oldKey, start)
	r, err := NewCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	newCert, newKey := selfSignedPEM(t, "new")
	writePair(t, certFile, keyFile, newCert, oldKey, start.Add(time.Minute))
	if got := servedCN(t, r); got != "old" {
		t.Fatalf("served %q with a mismatched pair, want old", got)
	}

	writePair(t, certFile, keyFile, newCert, newKey, start.Add(2*time.Minute))
	if got := servedCN(t, r); got != "new" {
		t.Fatalf("served %q once the pair matched, want new", got)
	}
}
