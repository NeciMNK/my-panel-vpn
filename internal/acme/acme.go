// Package acme obtains Let's Encrypt certificates over DNS-01 solved via the
// Cloudflare API, so wildcards work and no inbound port has to be free.
package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

// Issuer obtains a certificate chain and its private key for the given names.
type Issuer interface {
	Obtain(domains []string) (certPEM, keyPEM []byte, err error)
}

// CloudflareIssuer issues from Let's Encrypt production with an account bound
// to AccountKey; the account is looked up by key and registered when missing.
type CloudflareIssuer struct {
	APIToken   string
	AccountKey crypto.Signer
}

type account struct {
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (a *account) GetEmail() string                        { return "" }
func (a *account) GetRegistration() *registration.Resource { return a.registration }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.key }

func (i *CloudflareIssuer) Obtain(domains []string) ([]byte, []byte, error) {
	if strings.TrimSpace(i.APIToken) == "" {
		return nil, nil, errors.New("cloudflare API token is not set")
	}
	user := &account{key: i.AccountKey}
	client, err := lego.NewClient(lego.NewConfig(user))
	if err != nil {
		return nil, nil, fmt.Errorf("acme client: %w", err)
	}

	cfg := cloudflare.NewDefaultConfig()
	cfg.AuthToken = i.APIToken
	provider, err := cloudflare.NewDNSProviderConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return nil, nil, fmt.Errorf("dns-01 provider: %w", err)
	}

	reg, err := client.Registration.ResolveAccountByKey()
	if err != nil {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, nil, fmt.Errorf("acme account registration: %w", err)
		}
	}
	user.registration = reg

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, Bundle: true})
	if err != nil {
		return nil, nil, err
	}
	return res.Certificate, res.PrivateKey, nil
}

// NewAccountKey returns a fresh ACME account key and its PEM encoding.
func NewAccountKey() (crypto.Signer, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, "", err
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

// ParseAccountKey decodes a key produced by NewAccountKey.
func ParseAccountKey(keyPEM string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("acme account key: no PEM block")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}
