package autocert

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// legoClient abstracts the lego operations used by Manager for testability.
type legoClient interface {
	Register(opts registration.RegisterOptions) (*registration.Resource, error)
	Obtain(req certificate.ObtainRequest) (*certificate.Resource, error)
	Renew(cert certificate.Resource, bundle, mustStaple bool, preferredChain string) (*certificate.Resource, error)
}

type legoClientAdapter struct {
	client *lego.Client
}

func (c *legoClientAdapter) Register(opts registration.RegisterOptions) (*registration.Resource, error) {
	return c.client.Registration.Register(opts)
}

func (c *legoClientAdapter) Obtain(req certificate.ObtainRequest) (*certificate.Resource, error) {
	return c.client.Certificate.Obtain(req)
}

func (c *legoClientAdapter) Renew(cert certificate.Resource, bundle, mustStaple bool, preferredChain string) (*certificate.Resource, error) {
	return c.client.Certificate.Renew(cert, bundle, mustStaple, preferredChain)
}

// Manager orchestrates ACME certificate provisioning and renewal.
type Manager struct {
	config       *Config
	store        Store
	httpProvider *HTTP01Provider
	client       legoClient
}

// NewManager returns a new ACME certificate manager.
func NewManager(config *Config, store Store) *Manager {
	if config.RenewBefore == 0 {
		config.RenewBefore = DefaultRenewBefore
	}
	return &Manager{
		config:       config,
		store:        store,
		httpProvider: NewHTTP01Provider(),
	}
}

// HTTP01Handler serves HTTP-01 challenge responses. Mount it at
// /.well-known/acme-challenge/ on port 80 before TLS termination.
func (m *Manager) HTTP01Handler() http.Handler {
	return m.httpProvider
}

// Obtain requests a new certificate from the ACME server for the given
// domains and stores it.
func (m *Manager) Obtain(domains []string) (*CertificateData, error) {
	if len(domains) == 0 {
		return nil, errors.New("autocert: no domains specified")
	}

	account, err := m.loadOrCreateAccount()
	if err != nil {
		return nil, err
	}
	if err := m.initClient(account); err != nil {
		return nil, err
	}

	if account.Registration == nil {
		reg, err := m.client.Register(registration.RegisterOptions{
			TermsOfServiceAgreed: m.config.TOSAgreed,
		})
		if err != nil {
			return nil, fmt.Errorf("autocert: failed to register account: %w", err)
		}
		account.Registration = reg
		if err := m.saveAccount(account); err != nil {
			return nil, err
		}
	}

	req := certificate.ObtainRequest{
		Domains: domains,
		Bundle:  true,
	}
	res, err := m.client.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("autocert: failed to obtain certificate: %w", err)
	}

	cert, err := resourceToCertData(res, domains, account.Email)
	if err != nil {
		return nil, err
	}
	if err := m.store.SaveCertificate(cert); err != nil {
		return nil, fmt.Errorf("autocert: failed to store certificate: %w", err)
	}
	return cert, nil
}

// Renew renews an existing certificate and stores the updated version.
func (m *Manager) Renew(cert *CertificateData) (*CertificateData, error) {
	if cert == nil {
		return nil, errors.New("autocert: certificate is nil")
	}

	account, err := m.loadOrCreateAccount()
	if err != nil {
		return nil, err
	}
	if err := m.initClient(account); err != nil {
		return nil, err
	}

	res := certificate.Resource{
		Domain:      cert.Domains[0],
		CertURL:     cert.CertURL,
		Certificate: cert.CertPEM,
		PrivateKey:  cert.KeyPEM,
	}
	newRes, err := m.client.Renew(res, true, false, "")
	if err != nil {
		return nil, fmt.Errorf("autocert: failed to renew certificate: %w", err)
	}

	newCert, err := resourceToCertData(newRes, cert.Domains, account.Email)
	if err != nil {
		return nil, err
	}
	if err := m.store.SaveCertificate(newCert); err != nil {
		return nil, fmt.Errorf("autocert: failed to store renewed certificate: %w", err)
	}
	return newCert, nil
}

// initClient creates the lego client and configures challenge providers.
func (m *Manager) initClient(account *Account) error {
	if m.client != nil {
		return nil
	}

	legoCfg := lego.NewConfig(account)
	if m.config.CAURL != "" {
		legoCfg.CADirURL = m.config.CAURL
	} else {
		legoCfg.CADirURL = LEDirectoryProduction
	}
	legoCfg.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(legoCfg)
	if err != nil {
		return fmt.Errorf("autocert: failed to create lego client: %w", err)
	}

	switch m.config.ChallengeType {
	case ChallengeHTTP01:
		if err := client.Challenge.SetHTTP01Provider(m.httpProvider); err != nil {
			return fmt.Errorf("autocert: failed to set http-01 provider: %w", err)
		}
	case ChallengeDNS01:
		p, err := newDNS01Provider(m.config)
		if err != nil {
			return err
		}
		if err := client.Challenge.SetDNS01Provider(p); err != nil {
			return fmt.Errorf("autocert: failed to set dns-01 provider: %w", err)
		}
	}

	m.client = &legoClientAdapter{client: client}
	return nil
}

// loadOrCreateAccount loads the persisted account, or creates a new one.
func (m *Manager) loadOrCreateAccount() (*Account, error) {
	data, err := m.store.LoadAccount()
	if err == nil {
		priv, err := decodePrivateKeyPEM(data.PrivateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("autocert: failed to decode account key: %w", err)
		}
		var reg *registration.Resource
		if len(data.Registration) > 0 {
			reg = new(registration.Resource)
			if err := json.Unmarshal(data.Registration, reg); err != nil {
				return nil, fmt.Errorf("autocert: failed to unmarshal registration: %w", err)
			}
		}
		return &Account{
			Email:        data.Email,
			PrivateKey:   priv,
			Registration: reg,
		}, nil
	}
	if !errors.Is(err, ErrNoAccount) {
		return nil, err
	}

	acc, err := NewAccount(m.config.Email)
	if err != nil {
		return nil, err
	}
	if err := m.saveAccount(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

func (m *Manager) saveAccount(acc *Account) error {
	regBytes, err := json.Marshal(acc.Registration)
	if err != nil {
		return fmt.Errorf("autocert: failed to marshal registration: %w", err)
	}
	return m.store.SaveAccount(&AccountData{
		Email:         acc.Email,
		PrivateKeyPEM: encodePrivateKeyPEM(acc.PrivateKey),
		Registration:  regBytes,
	})
}

func resourceToCertData(res *certificate.Resource, domains []string, email string) (*CertificateData, error) {
	if res == nil {
		return nil, errors.New("autocert: nil certificate resource")
	}
	expiresAt, err := certExpiry(res.Certificate)
	if err != nil {
		return nil, fmt.Errorf("autocert: failed to parse certificate expiry: %w", err)
	}
	return &CertificateData{
		CertPEM:      res.Certificate,
		KeyPEM:       res.PrivateKey,
		Domains:      domains,
		AccountEmail: email,
		CertURL:      res.CertURL,
		ExpiresAt:    expiresAt,
	}, nil
}

func certExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, errors.New("autocert: failed to decode certificate PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}
