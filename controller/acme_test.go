package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flynn/flynn/controller/data"
	ct "github.com/flynn/flynn/controller/types"
	"github.com/flynn/flynn/pkg/autocert"
	"github.com/flynn/flynn/pkg/ctxhelper"
	"github.com/julienschmidt/httprouter"
)

func jsonRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestACMEProvisionValidation(t *testing.T) {
	api := newTestACMEAPI(t)

	for _, body := range []string{`{}`, `{"domains":[]}`, `{"domains":""}`} {
		rec := httptest.NewRecorder()
		api.ProvisionACME(context.Background(), rec, jsonRequest("POST", "/certs/letsencrypt", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for body %s, got %d", body, rec.Code)
		}
	}
}

func TestACMEProvisionUnconfigured(t *testing.T) {
	api := newTestACMEAPI(t)
	rec := httptest.NewRecorder()
	api.ProvisionACME(context.Background(), rec, jsonRequest("POST", "/certs/letsencrypt", `{"domains":["example.com"]}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when unconfigured, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	api.RevokeACMECert(context.Background(), rec, httptest.NewRequest("DELETE", "/certs/letsencrypt/nonexistent.com", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing cert, got %d", rec.Code)
	}
}

func TestACMECertNotFound(t *testing.T) {
	api := newTestACMEAPI(t)
	ctx := ctxhelper.NewContextParams(context.Background(), httprouter.Params{{Key: "domain", Value: "example.com"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/certs/letsencrypt/example.com", nil)
	api.GetACMECert(ctx, rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing certificate, got %d", rec.Code)
	}
}

func TestACMECertListEmpty(t *testing.T) {
	api := newTestACMEAPI(t)
	rec := httptest.NewRecorder()
	api.ListACMECerts(context.Background(), rec, httptest.NewRequest("GET", "/certs/letsencrypt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var certs []*ct.ACMECert
	if err := json.Unmarshal(rec.Body.Bytes(), &certs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("expected no certificates, got %d", len(certs))
	}
}

func TestACMEConfigValidation(t *testing.T) {
	api := newTestACMEAPI(t)

	// missing email should fail validation
	body := `{"enabled":true,"challenge_type":"http-01"}`
	rec := httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing email, got %d", rec.Code)
	}

	// invalid challenge type should fail validation
	body = `{"enabled":true,"email":"test@example.com","challenge_type":"tls-01"}`
	rec = httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid challenge type, got %d", rec.Code)
	}

	// valid config should succeed
	body = `{"enabled":true,"email":"test@example.com","challenge_type":"http-01","ca_url":"https://acme.test/directory"}`
	rec = httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid config, got %d: %s", rec.Code, rec.Body.String())
	}

	var cfg ct.ACMEConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if cfg.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %q", cfg.Email)
	}
	if cfg.ChallengeType != "http-01" {
		t.Errorf("expected challenge_type http-01, got %q", cfg.ChallengeType)
	}

	// GET config should return the stored config
	rec = httptest.NewRecorder()
	api.GetACMEConfig(context.Background(), rec, httptest.NewRequest("GET", "/certs/letsencrypt/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET config, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if cfg.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %q", cfg.Email)
	}
}

func TestACMEReconfigDisablesManager(t *testing.T) {
	api := newTestACMEAPI(t)

	// enable ACME
	body := `{"enabled":true,"email":"test@example.com","challenge_type":"http-01"}`
	rec := httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 enabling ACME, got %d", rec.Code)
	}

	// disabling should return 503 on provision
	body = `{"enabled":false}`
	rec = httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 disabling ACME, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	api.ProvisionACME(context.Background(), rec, jsonRequest("POST", "/certs/letsencrypt", `{"domains":["example.com"]}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 after disabled ACME, got %d", rec.Code)
	}
}

func TestACMEChallengeHandler(t *testing.T) {
	api := newTestACMEAPI(t)

	// unconfigured challenge handler returns 404
	rec := httptest.NewRecorder()
	api.acmeChallenge(rec, httptest.NewRequest("GET", "/.well-known/acme-challenge/token", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when unconfigured, got %d", rec.Code)
	}

	// configure with http-01 and present a challenge via the manager
	body := `{"enabled":true,"email":"test@example.com","challenge_type":"http-01"}`
	rec = httptest.NewRecorder()
	api.UpdateACMEConfig(context.Background(), rec, jsonRequest("PUT", "/certs/letsencrypt/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 enabling ACME, got %d", rec.Code)
	}

	mgr, err := api.acmeManager()
	if err != nil {
		t.Fatalf("expected manager after config, got %v", err)
	}
	if err := mgr.HTTP01Provider().Present("example.com", "token", "keyauth"); err != nil {
		t.Fatalf("Present failed: %v", err)
	}

	rec = httptest.NewRecorder()
	api.acmeChallenge(rec, httptest.NewRequest("GET", "/.well-known/acme-challenge/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for known token, got %d", rec.Code)
	}
	if rec.Body.String() != "keyauth" {
		t.Errorf("expected body keyauth, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	api.acmeChallenge(rec, httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", rec.Code)
	}
}

func (api *controllerAPI) acmeChallenge(w http.ResponseWriter, r *http.Request) {
	acmeChallengeHandler{c: api}.ServeHTTP(w, r)
}

func newTestACMEAPI(t *testing.T) *controllerAPI {
	api := &controllerAPI{
		acmeStore: &fakeACMEStore{certs: make(map[string]*autocert.CertificateData)},
	}
	return api
}

type fakeACMEStore struct {
	mu      sync.Mutex
	account *autocert.AccountData
	certs   map[string]*autocert.CertificateData
}

func (s *fakeACMEStore) SaveAccount(a *autocert.AccountData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.account = a
	return nil
}

func (s *fakeACMEStore) LoadAccount() (*autocert.AccountData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return nil, autocert.ErrNoAccount
	}
	return s.account, nil
}

func (s *fakeACMEStore) SaveCertificate(c *autocert.CertificateData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.Domains) == 0 {
		return errors.New("no domains")
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.ID == "" {
		c.ID = "cert-" + c.Domains[0]
	}
	s.certs[c.Domains[0]] = c
	return nil
}

func (s *fakeACMEStore) LoadCertificate(domain string) (*autocert.CertificateData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certs[domain]
	if !ok {
		return nil, data.ErrACMECertNotFound
	}
	return c, nil
}

func (s *fakeACMEStore) ListCertificates() ([]*autocert.CertificateData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*autocert.CertificateData, 0, len(s.certs))
	for _, c := range s.certs {
		out = append(out, c)
	}
	return out, nil
}

func (s *fakeACMEStore) DeleteCertificate(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.certs, domain)
	return nil
}
