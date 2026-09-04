package autocert

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/flynn/flynn/pkg/certgen"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/registration"
)

type fakeStore struct {
	mu      sync.Mutex
	account *AccountData
	certs   map[string]*CertificateData
}

func newFakeStore() *fakeStore {
	return &fakeStore{certs: make(map[string]*CertificateData)}
}

func (s *fakeStore) SaveAccount(a *AccountData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.account = a
	return nil
}

func (s *fakeStore) LoadAccount() (*AccountData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return nil, ErrNoAccount
	}
	return s.account, nil
}

func (s *fakeStore) SaveCertificate(c *CertificateData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.Domains) == 0 {
		return errors.New("no domains")
	}
	c2 := *c
	if c2.ID == "" {
		c2.ID = "cert-" + c2.Domains[0]
	}
	now := time.Now()
	if c2.CreatedAt.IsZero() {
		c2.CreatedAt = now
	}
	c2.UpdatedAt = now
	s.certs[c2.Domains[0]] = &c2
	return nil
}

func (s *fakeStore) LoadCertificate(domain string) (*CertificateData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certs[domain]
	if !ok {
		return nil, errors.New("not found")
	}
	return c, nil
}

func (s *fakeStore) ListCertificates() ([]*CertificateData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CertificateData, 0, len(s.certs))
	for _, c := range s.certs {
		out = append(out, c)
	}
	return out, nil
}

func (s *fakeStore) DeleteCertificate(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.certs, domain)
	return nil
}

type fakeLegoClient struct {
	reg  *registration.Resource
	cert *certificate.Resource
}

func (c *fakeLegoClient) Register(opts registration.RegisterOptions) (*registration.Resource, error) {
	return c.reg, nil
}

func (c *fakeLegoClient) Obtain(req certificate.ObtainRequest) (*certificate.Resource, error) {
	if c.cert == nil {
		return nil, errors.New("no cert")
	}
	return c.cert, nil
}

func (c *fakeLegoClient) Renew(cert certificate.Resource, bundle, mustStaple bool, preferredChain string) (*certificate.Resource, error) {
	return c.Obtain(certificate.ObtainRequest{Domains: []string{cert.Domain}})
}

func makeCert(t *testing.T, host string) *certgen.Certificate {
	c, err := certgen.Generate(certgen.Params{Hosts: []string{host}})
	if err != nil {
		t.Fatalf("certgen.Generate failed: %v", err)
	}
	return c
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.CAURL != LEDirectoryProduction {
		t.Errorf("expected production CA, got %q", cfg.CAURL)
	}
	if cfg.RenewBefore != DefaultRenewBefore {
		t.Errorf("expected RenewBefore %v, got %v", DefaultRenewBefore, cfg.RenewBefore)
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := &Config{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled config should be valid, got %v", err)
	}

	cfg = &Config{Enabled: true, ChallengeType: ChallengeHTTP01}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for missing email")
	}

	cfg = &Config{Enabled: true, Email: "test@example.com", ChallengeType: ChallengeHTTP01}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid http-01 config rejected: %v", err)
	}

	cfg = &Config{Enabled: true, Email: "test@example.com", ChallengeType: ChallengeDNS01}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for dns-01 without provider")
	}

	cfg.DNSProvider = DNSProviderAutoDNS
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid dns-01 config rejected: %v", err)
	}
}

func TestHTTP01Provider(t *testing.T) {
	p := NewHTTP01Provider()
	if err := p.Present("example.com", "token1", "keyauth1"); err != nil {
		t.Fatalf("Present failed: %v", err)
	}

	rec := &fakeResponseWriter{}
	req := newRequest("GET", "/.well-known/acme-challenge/token1")
	p.ServeHTTP(rec, req)
	if rec.status != 200 {
		t.Fatalf("expected status 200, got %d", rec.status)
	}
	if string(rec.body) != "keyauth1" {
		t.Fatalf("expected body keyauth1, got %q", rec.body)
	}

	if err := p.CleanUp("example.com", "token1", "keyauth1"); err != nil {
		t.Fatalf("CleanUp failed: %v", err)
	}
	rec = &fakeResponseWriter{}
	p.ServeHTTP(rec, newRequest("GET", "/.well-known/acme-challenge/token1"))
	if rec.status != 404 {
		t.Fatalf("expected status 404 after cleanup, got %d", rec.status)
	}
}

func TestManagerObtain(t *testing.T) {
	store := newFakeStore()
	cfg := DefaultConfig()
	cfg.Email = "test@example.com"
	cfg.ChallengeType = ChallengeHTTP01

	m := NewManager(cfg, store)
	m.client = &fakeLegoClient{
		reg: &registration.Resource{URI: "https://acme/account/1"},
		cert: &certificate.Resource{
			Domain:      "example.com",
			CertURL:     "https://acme/cert/1",
			Certificate: []byte(makeCert(t, "example.com").PEM),
			PrivateKey:  []byte(makeCert(t, "example.com").KeyPEM),
		},
	}

	cert, err := m.Obtain([]string{"example.com"})
	if err != nil {
		t.Fatalf("Obtain failed: %v", err)
	}
	if len(cert.Domains) != 1 || cert.Domains[0] != "example.com" {
		t.Errorf("unexpected domains: %v", cert.Domains)
	}
	if cert.ExpiresAt.IsZero() {
		t.Error("expected non-zero expiry")
	}

	stored, err := store.LoadCertificate("example.com")
	if err != nil {
		t.Fatalf("certificate not stored: %v", err)
	}
	if !stored.ExpiresAt.Equal(cert.ExpiresAt) {
		t.Errorf("stored expiry mismatch: %v vs %v", stored.ExpiresAt, cert.ExpiresAt)
	}
}

func TestManagerRenew(t *testing.T) {
	store := newFakeStore()
	cfg := DefaultConfig()
	cfg.Email = "test@example.com"
	cfg.ChallengeType = ChallengeHTTP01

	oldCert := makeCert(t, "example.com")
	store.SaveCertificate(&CertificateData{
		CertPEM:      []byte(oldCert.PEM),
		KeyPEM:       []byte(oldCert.KeyPEM),
		Domains:      []string{"example.com"},
		AccountEmail: cfg.Email,
		CertURL:      "https://acme/cert/old",
		ExpiresAt:    time.Now().Add(-24 * time.Hour),
	})

	newCert := makeCert(t, "example.com")
	m := NewManager(cfg, store)
	m.client = &fakeLegoClient{
		reg: &registration.Resource{URI: "https://acme/account/1"},
		cert: &certificate.Resource{
			Domain:      "example.com",
			CertURL:     "https://acme/cert/new",
			Certificate: []byte(newCert.PEM),
			PrivateKey:  []byte(newCert.KeyPEM),
		},
	}

	renewed, err := m.Renew(store.certs["example.com"])
	if err != nil {
		t.Fatalf("Renew failed: %v", err)
	}
	if renewed.CertURL != "https://acme/cert/new" {
		t.Errorf("expected new cert URL, got %q", renewed.CertURL)
	}

	stored, err := store.LoadCertificate("example.com")
	if err != nil {
		t.Fatalf("certificate missing after renew: %v", err)
	}
	if stored.CertURL != "https://acme/cert/new" {
		t.Errorf("stored cert URL not updated: %q", stored.CertURL)
	}
}

func TestRenewerSelectsExpiredCerts(t *testing.T) {
	store := newFakeStore()
	cfg := DefaultConfig()
	cfg.Email = "test@example.com"
	cfg.ChallengeType = ChallengeHTTP01

	expiredCert := makeCert(t, "expired.example.com")
	store.SaveCertificate(&CertificateData{
		CertPEM:      []byte(expiredCert.PEM),
		KeyPEM:       []byte(expiredCert.KeyPEM),
		Domains:      []string{"expired.example.com"},
		AccountEmail: cfg.Email,
		CertURL:      "https://acme/cert/expired",
		ExpiresAt:    time.Now().Add(-24 * time.Hour),
	})

	newCert := makeCert(t, "expired.example.com")
	m := NewManager(cfg, store)
	m.client = &fakeLegoClient{
		reg: &registration.Resource{URI: "https://acme/account/1"},
		cert: &certificate.Resource{
			Domain:      "expired.example.com",
			CertURL:     "https://acme/cert/renewed",
			Certificate: []byte(newCert.PEM),
			PrivateKey:  []byte(newCert.KeyPEM),
		},
	}

	renewer := NewRenewer(m, 0)
	renewer.renewAll()

	stored, err := store.LoadCertificate("expired.example.com")
	if err != nil {
		t.Fatalf("certificate missing after renew: %v", err)
	}
	if stored.CertURL != "https://acme/cert/renewed" {
		t.Errorf("expected renewed cert URL, got %q", stored.CertURL)
	}
}
