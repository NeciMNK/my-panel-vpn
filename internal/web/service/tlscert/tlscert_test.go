package tlscert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/acme"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type fakeIssuer struct {
	notAfter time.Time
	err      error
	calls    *atomic.Int32
}

func (f fakeIssuer) Obtain(domains []string) ([]byte, []byte, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, nil, f.err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     f.notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

type harness struct {
	svc      *Service
	issuer   *fakeIssuer
	restarts *atomic.Int32
}

func setup(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))

	h := &harness{svc: &Service{}, restarts: &atomic.Int32{}}
	h.issuer = &fakeIssuer{notAfter: time.Now().Add(90 * 24 * time.Hour), calls: &atomic.Int32{}}
	prevIssuer, prevAsync, prevRestart := newIssuer, runAsync, restartPanel
	newIssuer = func(string, crypto.Signer) acme.Issuer { return *h.issuer }
	runAsync = func(f func()) { f() }
	restartPanel = func() error { h.restarts.Add(1); return nil }
	t.Cleanup(func() { newIssuer, runAsync, restartPanel = prevIssuer, prevAsync, prevRestart })
	if err := h.svc.SetCloudflareToken("cf-token"); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) reload(t *testing.T, id int) *model.TlsCertificate {
	t.Helper()
	cert, err := h.svc.get(id)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestNormalizeDomains(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr string
	}{
		{"wildcard with apex, cased and duplicated", []string{" *.Example.com", "example.com.", "EXAMPLE.com", ""}, []string{"*.example.com", "example.com"}, ""},
		{"wildcard not leftmost", []string{"a.*.example.com"}, nil, `invalid domain name "a.*.example.com"`},
		{"bare TLD", []string{"localhost"}, nil, `invalid domain name "localhost"`},
		{"path segment", []string{"../etc"}, nil, `invalid domain name "../etc"`},
		{"nothing", []string{" ", ""}, nil, "at least one domain is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeDomains(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// Issuing a wildcard must land in a plain folder under the cert root and be
// served by the panel; the sub server only follows when it shared the old pair.
func TestIssueWritesFilesAndAppliesToPanel(t *testing.T) {
	h := setup(t)
	ss := h.svc.settingService
	for key, val := range map[string]string{"webCertFile": "/old/cert.pem", "webKeyFile": "/old/key.pem", "subCertFile": "/old/cert.pem", "subKeyFile": "/old/key.pem"} {
		if err := database.GetDB().Create(&model.Setting{Key: key, Value: val}).Error; err != nil {
			t.Fatal(err)
		}
	}

	created, err := h.svc.Create([]string{"*.example.com", "example.com"}, true, true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cert := h.reload(t, created.Id)
	if cert.Status != model.TlsCertStatusValid || cert.LastError != "" {
		t.Fatalf("status = %q (%q), want valid", cert.Status, cert.LastError)
	}
	wantDir := filepath.Join(os.Getenv("XUI_DB_FOLDER"), "certs", "wildcard.example.com")
	if cert.CertFile != filepath.Join(wantDir, "fullchain.pem") || cert.KeyFile != filepath.Join(wantDir, "privkey.pem") {
		t.Fatalf("files = %s, %s; want under %s", cert.CertFile, cert.KeyFile, wantDir)
	}
	if want := h.issuer.notAfter.Truncate(time.Second).UnixMilli(); cert.NotAfter != want {
		t.Fatalf("notAfter = %d, want %d", cert.NotAfter, want)
	}
	if info, err := os.Stat(cert.KeyFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v (%v), want 0600", info, err)
	}

	for name, get := range map[string]func() (string, error){"web": ss.GetCertFile, "sub": ss.GetSubCertFile} {
		if got, _ := get(); got != cert.CertFile {
			t.Fatalf("%s cert = %q, want %q", name, got, cert.CertFile)
		}
	}
	if got := h.restarts.Load(); got != 1 {
		t.Fatalf("panel restarts after switching certs = %d, want 1", got)
	}

	// Renewing keeps the paths, so the listeners hot-reload and no restart runs.
	if _, err := h.svc.Renew(cert.Id); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got := h.restarts.Load(); got != 1 {
		t.Fatalf("restarts after renewal = %d, want still 1", got)
	}
}

func TestApplyLeavesSeparateSubCertAlone(t *testing.T) {
	h := setup(t)
	for key, val := range map[string]string{"webCertFile": "/old/web.pem", "webKeyFile": "/old/web.key", "subCertFile": "/old/sub.pem", "subKeyFile": "/old/sub.key"} {
		if err := database.GetDB().Create(&model.Setting{Key: key, Value: val}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.Create([]string{"panel.example.com"}, false, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.settingService.GetSubCertFile(); got != "/old/sub.pem" {
		t.Fatalf("sub cert = %q, want its own /old/sub.pem kept", got)
	}
}

// A failed renewal must leave the working files and expiry in place.
func TestFailedRenewalKeepsPreviousCertificate(t *testing.T) {
	h := setup(t)
	created, err := h.svc.Create([]string{"example.com"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	before := h.reload(t, created.Id)
	content, _ := os.ReadFile(before.CertFile)

	h.issuer.err = errors.New("cloudflare: zone not found")
	if _, err := h.svc.Renew(before.Id); err != nil {
		t.Fatal(err)
	}
	after := h.reload(t, before.Id)
	if after.Status != model.TlsCertStatusError || after.LastError != "cloudflare: zone not found" {
		t.Fatalf("status = %q, lastError = %q", after.Status, after.LastError)
	}
	if after.NotAfter != before.NotAfter || after.CertFile != before.CertFile {
		t.Fatalf("expiry/path changed on failure: %+v -> %+v", before, after)
	}
	if now, _ := os.ReadFile(before.CertFile); string(now) != string(content) {
		t.Fatal("certificate file was overwritten by a failed renewal")
	}
}

func TestRenewDue(t *testing.T) {
	h := setup(t)
	created, err := h.svc.Create([]string{"example.com"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.UnixMilli(h.reload(t, created.Id).NotAfter)
	h.issuer.calls.Store(0)

	h.svc.RenewDue(notAfter.Add(-RenewBefore - time.Hour))
	if got := h.issuer.calls.Load(); got != 0 {
		t.Fatalf("renewed %d times with more than 30 days left", got)
	}

	h.issuer.notAfter = notAfter.Add(60 * 24 * time.Hour)
	h.svc.RenewDue(notAfter.Add(-RenewBefore + time.Hour))
	if got := h.issuer.calls.Load(); got != 1 {
		t.Fatalf("renewals inside the window = %d, want 1", got)
	}
	renewed := h.reload(t, created.Id)
	if renewed.Status != model.TlsCertStatusValid || !time.UnixMilli(renewed.NotAfter).After(notAfter) {
		t.Fatalf("after renewal: status %q notAfter %v, want valid and later than %v", renewed.Status, time.UnixMilli(renewed.NotAfter), notAfter)
	}

	// A recent failure is not retried every hour.
	h.issuer.notAfter = time.Now().Add(10 * 24 * time.Hour)
	if _, err := h.svc.Renew(created.Id); err != nil {
		t.Fatal(err)
	}
	h.issuer.err = errors.New("rate limited")
	if _, err := h.svc.Renew(created.Id); err != nil {
		t.Fatal(err)
	}
	h.issuer.calls.Store(0)
	h.svc.RenewDue(time.Now())
	if got := h.issuer.calls.Load(); got != 0 {
		t.Fatalf("retried %d times right after a failure", got)
	}
	due := time.Now().Add(RetryAfterFailure + time.Hour)
	h.svc.RenewDue(due)
	if got := h.issuer.calls.Load(); got != 1 {
		t.Fatalf("retries once the backoff passed = %d, want 1", got)
	}

	if _, err := h.svc.Update(created.Id, false); err != nil {
		t.Fatal(err)
	}
	h.issuer.calls.Store(0)
	h.svc.RenewDue(due)
	if got := h.issuer.calls.Load(); got != 0 {
		t.Fatalf("renewed %d times with auto-renew off", got)
	}
}

func TestDeleteRefusesWhileInUse(t *testing.T) {
	h := setup(t)
	if err := h.svc.settingService.SetSubCertFile("/other/sub.pem"); err != nil {
		t.Fatal(err)
	}
	created, err := h.svc.Create([]string{"example.com"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	cert := h.reload(t, created.Id)
	ib := &model.Inbound{
		Remark: "vless-tls", Port: 443, Protocol: model.VLESS, Tag: "inbound-443",
		StreamSettings: `{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":"` + cert.CertFile + `"}]}}`,
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(cert.Id); err == nil || err.Error() != `certificate is still used by inbound "vless-tls"` {
		t.Fatalf("Delete with an inbound on it: %v", err)
	}

	if err := database.GetDB().Delete(ib).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.svc.settingService.SetCertFile(cert.CertFile); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(cert.Id); err == nil || err.Error() != "certificate is still used by the panel" {
		t.Fatalf("Delete while the panel serves it: %v", err)
	}

	if err := h.svc.settingService.SetCertFile(""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.settingService.SetSubCertFile(cert.CertFile); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(cert.Id); err == nil || err.Error() != "certificate is still used by the subscription server" {
		t.Fatalf("Delete while the sub server serves it: %v", err)
	}
	if err := h.svc.settingService.SetSubCertFile(""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(cert.Id); err != nil {
		t.Fatalf("Delete unused: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(cert.CertFile)); !os.IsNotExist(err) {
		t.Fatalf("certificate folder still there: %v", err)
	}
}

func TestCreateRequiresToken(t *testing.T) {
	h := setup(t)
	if err := h.svc.SetCloudflareToken("  "); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Create([]string{"example.com"}, true, false); err == nil || err.Error() != "set the Cloudflare API token first" {
		t.Fatalf("Create without token: %v", err)
	}
}

func TestInstallValidatesNameAndPair(t *testing.T) {
	h := setup(t)
	certPEM, keyPEM, err := fakeIssuer{notAfter: time.Now().Add(time.Hour), calls: &atomic.Int32{}}.Obtain([]string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, _ := fakeIssuer{notAfter: time.Now().Add(time.Hour), calls: &atomic.Int32{}}.Obtain([]string{"example.com"})

	for _, name := range []string{"../../etc", "a/b", ".hidden", ""} {
		if _, err := h.svc.Install(TlsCertInstallRequest{Name: name, CertPem: string(certPEM), KeyPem: string(keyPEM)}); err == nil || !strings.HasPrefix(err.Error(), "invalid certificate name") {
			t.Fatalf("Install(%q) = %v, want invalid name", name, err)
		}
	}
	if _, err := h.svc.Install(TlsCertInstallRequest{Name: "example.com", CertPem: string(certPEM), KeyPem: string(otherKey)}); err == nil || !strings.HasPrefix(err.Error(), "issued certificate is unusable") {
		t.Fatalf("Install with a mismatched key = %v", err)
	}
	files, err := h.svc.Install(TlsCertInstallRequest{Name: "wildcard.example.com", CertPem: string(certPEM), KeyPem: string(keyPEM)})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got, _ := os.ReadFile(files.CertFile); string(got) != string(certPEM) {
		t.Fatal("installed certificate differs from the pushed one")
	}
}

// The sub server takes an issued pair when its cert field is empty or the
// certificate covers the host subscription links are built for.
func TestIssueFillsSubscriptionCert(t *testing.T) {
	cases := []struct {
		name         string
		settings     map[string]string
		domains      []string
		wantIssued   bool
		wantRestarts int32
	}{
		{"empty field, no sub host", nil, []string{"example.com"}, true, 1},
		{"sub domain covered by wildcard replaces old pair", map[string]string{"subDomain": "sub.example.com", "subCertFile": "/old/sub.pem", "subKeyFile": "/old/sub.key"}, []string{"*.example.com"}, true, 1},
		{"sub URI host covered", map[string]string{"subURI": "https://sub.example.com:2096/sub/"}, []string{"sub.example.com"}, true, 1},
		{"sub domain not covered keeps old pair", map[string]string{"subDomain": "sub.other.org", "subCertFile": "/old/sub.pem", "subKeyFile": "/old/sub.key"}, []string{"*.example.com"}, false, 0},
		{"empty field but sub domain not covered", map[string]string{"subDomain": "a.b.example.com"}, []string{"*.example.com", "example.com"}, false, 0},
		{"other pair without sub host is kept", map[string]string{"subCertFile": "/old/sub.pem", "subKeyFile": "/old/sub.key"}, []string{"example.com"}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			for key, val := range tc.settings {
				if err := database.GetDB().Create(&model.Setting{Key: key, Value: val}).Error; err != nil {
					t.Fatal(err)
				}
			}
			before, _ := h.svc.settingService.GetSubCertFile()
			created, err := h.svc.Create(tc.domains, true, false)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			cert := h.reload(t, created.Id)
			wantCert, wantKey := before, tc.settings["subKeyFile"]
			if tc.wantIssued {
				wantCert, wantKey = cert.CertFile, cert.KeyFile
			}
			gotCert, _ := h.svc.settingService.GetSubCertFile()
			gotKey, _ := h.svc.settingService.GetSubKeyFile()
			if gotCert != wantCert || gotKey != wantKey {
				t.Fatalf("sub pair = %q, %q; want %q, %q", gotCert, gotKey, wantCert, wantKey)
			}
			if got := h.restarts.Load(); got != tc.wantRestarts {
				t.Fatalf("restarts = %d, want %d", got, tc.wantRestarts)
			}
			if _, err := h.svc.Renew(cert.Id); err != nil {
				t.Fatalf("Renew: %v", err)
			}
			if got := h.restarts.Load(); got != tc.wantRestarts {
				t.Fatalf("restarts after renewal = %d, want still %d", got, tc.wantRestarts)
			}
		})
	}
}
