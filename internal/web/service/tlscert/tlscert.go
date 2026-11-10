// Package tlscert issues and renews panel-managed TLS certificates (ACME DNS-01
// over Cloudflare), applies them to the panel and copies them to every node.
package tlscert

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/acme"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
)

const (
	certFileName = "fullchain.pem"
	keyFileName  = "privkey.pem"
	// Let's Encrypt certificates live 90 days; renewing at 30 left leaves
	// several daily retries before expiry.
	RenewBefore = 30 * 24 * time.Hour
	maxDomains  = 100
	// A failed renewal waits this long before the hourly job retries it, well
	// inside Let's Encrypt's failed-validation limit.
	RetryAfterFailure = 6 * time.Hour
)

var (
	hostnameRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	dirNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,252}$`)

	// issueMu serializes issuance: one ACME account, and renewals must not
	// race a manual issue of the same row.
	issueMu sync.Mutex
)

// newIssuer, runAsync and restartPanel are swapped by tests for a local CA, a
// synchronous run and a counter.
var (
	newIssuer = func(token string, accountKey crypto.Signer) acme.Issuer {
		return &acme.CloudflareIssuer{APIToken: token, AccountKey: accountKey}
	}
	runAsync     = func(f func()) { go f() }
	restartPanel = func() error { return (&panel.PanelService{}).RestartPanel(3 * time.Second) }
)

// TlsCertFiles is where a certificate pair was written.
type TlsCertFiles struct {
	CertFile string `json:"certFile" example:"/etc/x-ui/certs/example.com/fullchain.pem"`
	KeyFile  string `json:"keyFile" example:"/etc/x-ui/certs/example.com/privkey.pem"`
}

// TlsCertInstallRequest is the body a master sends a node to store a pair.
type TlsCertInstallRequest struct {
	Name    string `json:"name" example:"wildcard.example.com"`
	CertPem string `json:"certPem" example:"-----BEGIN CERTIFICATE-----\n..."`
	KeyPem  string `json:"keyPem" example:"-----BEGIN EC PRIVATE KEY-----\n..."`
}

// TlsCertSettings reports whether issuance can run, never the token itself.
type TlsCertSettings struct {
	CloudflareTokenSet bool `json:"cloudflareTokenSet" example:"true"`
}

type Service struct {
	settingService service.SettingService
	nodeService    service.NodeService
}

// NormalizeDomains lower-cases, trims and de-duplicates names and rejects
// anything Let's Encrypt would refuse to put in a certificate.
func NormalizeDomains(raw []string) ([]string, error) {
	var out []string
	for _, d := range raw {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if len(d) > 253 || !hostnameRe.MatchString(d) {
			return nil, fmt.Errorf("invalid domain name %q", d)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one domain is required")
	}
	if len(out) > maxDomains {
		return nil, fmt.Errorf("too many domains (max %d)", maxDomains)
	}
	return out, nil
}

// dirName names a certificate's folder after its first domain; "*." becomes
// "wildcard." so the name stays a plain path segment.
func dirName(domains []string) string {
	if rest, ok := strings.CutPrefix(domains[0], "*."); ok {
		return "wildcard." + rest
	}
	return domains[0]
}

func (s *Service) Settings() (*TlsCertSettings, error) {
	token, err := s.settingService.GetTlsCloudflareToken()
	if err != nil {
		return nil, err
	}
	return &TlsCertSettings{CloudflareTokenSet: token != ""}, nil
}

func (s *Service) SetCloudflareToken(token string) error {
	return s.settingService.SetTlsCloudflareToken(strings.TrimSpace(token))
}

func (s *Service) List() ([]model.TlsCertificate, error) {
	var certs []model.TlsCertificate
	err := database.GetDB().Order("id asc").Find(&certs).Error
	return certs, err
}

func (s *Service) get(id int) (*model.TlsCertificate, error) {
	var cert model.TlsCertificate
	if err := database.GetDB().First(&cert, id).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, fmt.Errorf("certificate %d not found", id)
		}
		return nil, err
	}
	return &cert, nil
}

// Create stores a pending certificate and issues it in the background; the
// DNS-01 propagation wait outlasts an HTTP request.
func (s *Service) Create(domains []string, autoRenew, applyToPanel bool) (*model.TlsCertificate, error) {
	domains, err := NormalizeDomains(domains)
	if err != nil {
		return nil, err
	}
	if set, err := s.Settings(); err != nil {
		return nil, err
	} else if !set.CloudflareTokenSet {
		return nil, errors.New("set the Cloudflare API token first")
	}
	existing, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, c := range existing {
		if dirName(c.Domains) == dirName(domains) {
			return nil, fmt.Errorf("a certificate for %s already exists", domains[0])
		}
	}
	cert := &model.TlsCertificate{
		Domains:      domains,
		Status:       model.TlsCertStatusPending,
		AutoRenew:    autoRenew,
		ApplyToPanel: applyToPanel,
	}
	if err := database.GetDB().Create(cert).Error; err != nil {
		return nil, err
	}
	id := cert.Id
	runAsync(func() { s.issue(id) })
	return cert, nil
}

// Renew re-issues a certificate now, in the background.
func (s *Service) Renew(id int) (*model.TlsCertificate, error) {
	cert, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if cert.Status == model.TlsCertStatusPending {
		return nil, errors.New("certificate is already being issued")
	}
	if err := database.GetDB().Model(cert).Update("status", model.TlsCertStatusPending).Error; err != nil {
		return nil, err
	}
	cert.Status = model.TlsCertStatusPending
	runAsync(func() { s.issue(id) })
	return cert, nil
}

func (s *Service) Update(id int, autoRenew bool) (*model.TlsCertificate, error) {
	cert, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if err := database.GetDB().Model(cert).Update("auto_renew", autoRenew).Error; err != nil {
		return nil, err
	}
	cert.AutoRenew = autoRenew
	return cert, nil
}

// Delete removes the record and its files, refusing while the panel or an
// inbound still points at them: either would fail to start on its next load.
func (s *Service) Delete(id int) error {
	cert, err := s.get(id)
	if err != nil {
		return err
	}
	if cert.Status == model.TlsCertStatusPending {
		return errors.New("certificate is being issued; delete it once that finishes")
	}
	if cert.CertFile != "" {
		if user, err := s.usedBy(cert.CertFile); err != nil {
			return err
		} else if user != "" {
			return fmt.Errorf("certificate is still used by %s", user)
		}
	}
	if err := database.GetDB().Delete(&model.TlsCertificate{}, id).Error; err != nil {
		return err
	}
	if cert.CertFile != "" {
		dir := filepath.Dir(cert.CertFile)
		if filepath.Dir(dir) == filepath.Clean(config.GetCertFolderPath()) {
			if err := os.RemoveAll(dir); err != nil {
				logger.Warning("Could not remove certificate folder", dir, ":", err)
			}
		}
	}
	return nil
}

func (s *Service) usedBy(certFile string) (string, error) {
	for _, setting := range []struct {
		user string
		get  func() (string, error)
	}{{"the panel", s.settingService.GetCertFile}, {"the subscription server", s.settingService.GetSubCertFile}} {
		v, err := setting.get()
		if err != nil {
			return "", err
		}
		if v == certFile {
			return setting.user, nil
		}
	}
	var remarks []string
	pattern := "%" + certFile + "%"
	err := database.GetDB().Model(&model.Inbound{}).
		Where("stream_settings LIKE ? OR settings LIKE ?", pattern, pattern).
		Pluck("remark", &remarks).Error
	if err != nil {
		return "", err
	}
	if len(remarks) > 0 {
		return fmt.Sprintf("inbound %q", remarks[0]), nil
	}
	return "", nil
}

func (s *Service) accountKey() (crypto.Signer, error) {
	stored, err := s.settingService.GetTlsAcmeAccountKey()
	if err != nil {
		return nil, err
	}
	if stored != "" {
		return acme.ParseAccountKey(stored)
	}
	key, keyPEM, err := acme.NewAccountKey()
	if err != nil {
		return nil, err
	}
	if err := s.settingService.SetTlsAcmeAccountKey(keyPEM); err != nil {
		return nil, err
	}
	return key, nil
}

// issue obtains the certificate, writes it, applies it and pushes it to nodes.
// A failure keeps the previous files and records the error on the row.
func (s *Service) issue(id int) {
	issueMu.Lock()
	defer issueMu.Unlock()

	cert, err := s.get(id)
	if err != nil {
		logger.Warning("TLS certificate", id, "vanished before issuance:", err)
		return
	}
	certPEM, keyPEM, notAfter, err := s.obtain(cert.Domains)
	if err != nil {
		logger.Warning("TLS certificate for", strings.Join(cert.Domains, ","), "was not issued:", err)
		database.GetDB().Model(cert).Updates(map[string]any{
			"status": model.TlsCertStatusError, "last_error": err.Error(),
		})
		return
	}
	files, err := WriteCertPair(dirName(cert.Domains), certPEM, keyPEM)
	if err != nil {
		database.GetDB().Model(cert).Updates(map[string]any{
			"status": model.TlsCertStatusError, "last_error": err.Error(),
		})
		return
	}
	cert.Status, cert.LastError = model.TlsCertStatusValid, ""
	cert.CertFile, cert.KeyFile, cert.NotAfter = files.CertFile, files.KeyFile, notAfter
	if err := database.GetDB().Model(cert).Updates(map[string]any{
		"status": cert.Status, "last_error": "", "cert_file": cert.CertFile,
		"key_file": cert.KeyFile, "not_after": cert.NotAfter,
	}).Error; err != nil {
		logger.Error("TLS certificate issued but not recorded:", err)
		return
	}
	logger.Info("Issued TLS certificate for", strings.Join(cert.Domains, ","))

	restart := false
	if cert.ApplyToPanel {
		changed, err := s.applyToPanel(cert)
		if err != nil {
			logger.Warning("TLS certificate not applied to the panel:", err)
		}
		restart = changed
	}
	if changed, err := s.applyToSub(cert); err != nil {
		logger.Warning("TLS certificate not applied to the subscription server:", err)
	} else if changed {
		restart = true
	}
	if restart {
		if err := restartPanel(); err != nil {
			logger.Warning("Panel restart after a TLS certificate switch failed:", err)
		}
	}
	s.pushToNodes(cert, certPEM, keyPEM)
}

func (s *Service) obtain(domains []string) ([]byte, []byte, int64, error) {
	token, err := s.settingService.GetTlsCloudflareToken()
	if err != nil {
		return nil, nil, 0, err
	}
	if token == "" {
		return nil, nil, 0, errors.New("cloudflare API token is not set")
	}
	key, err := s.accountKey()
	if err != nil {
		return nil, nil, 0, err
	}
	certPEM, keyPEM, err := newIssuer(token, key).Obtain(domains)
	if err != nil {
		return nil, nil, 0, err
	}
	leaf, err := parseLeaf(certPEM, keyPEM)
	if err != nil {
		return nil, nil, 0, err
	}
	return certPEM, keyPEM, leaf.NotAfter.UnixMilli(), nil
}

func parseLeaf(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("issued certificate is unusable: %w", err)
	}
	return x509.ParseCertificate(pair.Certificate[0])
}

// applyToPanel also moves the sub server when it shared the panel's old pair.
// Unchanged paths report false: listeners reload renewed files themselves.
func (s *Service) applyToPanel(cert *model.TlsCertificate) (bool, error) {
	oldCert, err := s.settingService.GetCertFile()
	if err != nil {
		return false, err
	}
	oldKey, err := s.settingService.GetKeyFile()
	if err != nil {
		return false, err
	}
	if oldCert == cert.CertFile && oldKey == cert.KeyFile {
		return false, nil
	}
	subCert, subKey, err := s.subPair()
	if err != nil {
		return false, err
	}
	if err := s.settingService.SetCertFile(cert.CertFile); err != nil {
		return false, err
	}
	if err := s.settingService.SetKeyFile(cert.KeyFile); err != nil {
		return false, err
	}
	if oldCert != "" && subCert == oldCert && subKey == oldKey {
		if err := s.setSubPair(cert); err != nil {
			return false, err
		}
	}
	logger.Info("Panel TLS certificate switched to", cert.CertFile)
	return true, nil
}

// applyToSub fills the subscription pair when it is empty or the certificate
// covers the subscription host; a non-covering cert would break client TLS.
func (s *Service) applyToSub(cert *model.TlsCertificate) (bool, error) {
	subCert, subKey, err := s.subPair()
	if err != nil {
		return false, err
	}
	if subCert == cert.CertFile && subKey == cert.KeyFile {
		return false, nil
	}
	host, err := s.subHost()
	if err != nil {
		return false, err
	}
	if host != "" {
		if !covers(cert.Domains, host) {
			return false, nil
		}
	} else if subCert != "" || subKey != "" {
		return false, nil
	}
	if err := s.setSubPair(cert); err != nil {
		return false, err
	}
	logger.Info("Subscription TLS certificate switched to", cert.CertFile)
	return true, nil
}

// subHost is the host subscription links point at: subDomain, else subURI's.
func (s *Service) subHost() (string, error) {
	domain, err := s.settingService.GetSubDomain()
	if err != nil {
		return "", err
	}
	if domain = strings.TrimSpace(domain); domain != "" {
		return strings.ToLower(domain), nil
	}
	uri, err := s.settingService.GetSubURI()
	if err != nil {
		return "", err
	}
	if u, err := url.Parse(strings.TrimSpace(uri)); err == nil {
		return strings.ToLower(u.Hostname()), nil
	}
	return "", nil
}

// covers matches host against the certificate names; "*." spans one label.
func covers(domains []string, host string) bool {
	host = strings.TrimSuffix(host, ".")
	for _, d := range domains {
		if d == host {
			return true
		}
		if rest, ok := strings.CutPrefix(d, "*."); ok {
			if label, parent, found := strings.Cut(host, "."); found && label != "" && parent == rest {
				return true
			}
		}
	}
	return false
}

func (s *Service) subPair() (string, string, error) {
	certFile, err := s.settingService.GetSubCertFile()
	if err != nil {
		return "", "", err
	}
	keyFile, err := s.settingService.GetSubKeyFile()
	return certFile, keyFile, err
}

func (s *Service) setSubPair(cert *model.TlsCertificate) error {
	if err := s.settingService.SetSubCertFile(cert.CertFile); err != nil {
		return err
	}
	return s.settingService.SetSubKeyFile(cert.KeyFile)
}

// pushToNodes copies the pair to every enabled node and records each outcome.
func (s *Service) pushToNodes(cert *model.TlsCertificate, certPEM, keyPEM []byte) {
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		logger.Warning("TLS certificate not pushed to nodes:", err)
		return
	}
	mgr := runtime.GetManager()
	installs := map[int]model.TlsCertNodeInstall{}
	for _, n := range nodes {
		if !n.Enable {
			continue
		}
		install := model.TlsCertNodeInstall{}
		if prev, ok := cert.NodeInstalls[n.Id]; ok {
			install = prev
		}
		if err := s.installOnNode(mgr, n, cert, certPEM, keyPEM, &install); err != nil {
			install.Error = err.Error()
			logger.Warning("TLS certificate not installed on node", n.Name, ":", err)
		}
		installs[n.Id] = install
	}
	cert.NodeInstalls = installs
	if err := database.GetDB().Model(cert).Select("NodeInstalls").Updates(&model.TlsCertificate{NodeInstalls: installs}).Error; err != nil {
		logger.Warning("TLS certificate node install state not saved:", err)
	}
}

func (s *Service) installOnNode(mgr *runtime.Manager, n *model.Node, cert *model.TlsCertificate, certPEM, keyPEM []byte, install *model.TlsCertNodeInstall) error {
	if mgr == nil {
		return errors.New("runtime manager unavailable")
	}
	remote, err := mgr.RemoteFor(n)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	certFile, keyFile, err := remote.InstallTlsCert(ctx, dirName(cert.Domains), certPEM, keyPEM)
	if err != nil {
		return err
	}
	*install = model.TlsCertNodeInstall{CertFile: certFile, KeyFile: keyFile, NotAfter: cert.NotAfter}
	return nil
}

// RenewDue renews certificates within RenewBefore of expiry and re-pushes
// current ones to nodes that missed them (offline, or added later).
func (s *Service) RenewDue(now time.Time) {
	certs, err := s.List()
	if err != nil {
		logger.Warning("TLS certificate renewal check failed:", err)
		return
	}
	for i := range certs {
		cert := &certs[i]
		if cert.Status == model.TlsCertStatusPending || cert.NotAfter == 0 {
			continue
		}
		failedRecently := cert.Status == model.TlsCertStatusError && now.Sub(time.UnixMilli(cert.UpdatedAt)) < RetryAfterFailure
		if cert.AutoRenew && time.UnixMilli(cert.NotAfter).Sub(now) < RenewBefore && !failedRecently {
			database.GetDB().Model(cert).Update("status", model.TlsCertStatusPending)
			s.issue(cert.Id)
			continue
		}
		if s.nodesBehind(cert) {
			certPEM, keyPEM, err := readPair(cert)
			if err != nil {
				logger.Warning("TLS certificate", cert.CertFile, "cannot be re-pushed:", err)
				continue
			}
			issueMu.Lock()
			s.pushToNodes(cert, certPEM, keyPEM)
			issueMu.Unlock()
		}
	}
}

func (s *Service) nodesBehind(cert *model.TlsCertificate) bool {
	nodes, err := s.nodeService.GetAll()
	if err != nil {
		return false
	}
	for _, n := range nodes {
		if n.Enable && cert.NodeInstalls[n.Id].NotAfter != cert.NotAfter {
			return true
		}
	}
	return false
}

func readPair(cert *model.TlsCertificate) ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(cert.CertFile)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(cert.KeyFile)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// Install is the node side of a push: validate and store the pair locally.
func (s *Service) Install(req TlsCertInstallRequest) (*TlsCertFiles, error) {
	if !dirNameRe.MatchString(req.Name) || strings.Contains(req.Name, "..") {
		return nil, fmt.Errorf("invalid certificate name %q", req.Name)
	}
	if _, err := parseLeaf([]byte(req.CertPem), []byte(req.KeyPem)); err != nil {
		return nil, err
	}
	return WriteCertPair(req.Name, []byte(req.CertPem), []byte(req.KeyPem))
}

// WriteCertPair writes the pair under the certificate folder. Each file is
// renamed into place so a reader never sees a half-written file.
func WriteCertPair(name string, certPEM, keyPEM []byte) (*TlsCertFiles, error) {
	dir := filepath.Join(config.GetCertFolderPath(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	files := &TlsCertFiles{CertFile: filepath.Join(dir, certFileName), KeyFile: filepath.Join(dir, keyFileName)}
	if err := writeAtomic(files.KeyFile, keyPEM); err != nil {
		return nil, err
	}
	if err := writeAtomic(files.CertFile, certPEM); err != nil {
		return nil, err
	}
	return files, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
