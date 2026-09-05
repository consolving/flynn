package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/flynn/flynn/controller/data"
	ct "github.com/flynn/flynn/controller/types"
	"github.com/flynn/flynn/pkg/autocert"
	"github.com/flynn/flynn/pkg/ctxhelper"
	"github.com/flynn/flynn/pkg/httphelper"
)

// acmeStoreWithRouteSync wraps an autocert.Store and updates any HTTP routes
// bound to the certificate's ACME domain whenever a certificate is saved.
type acmeStoreWithRouteSync struct {
	autocert.Store
	routes *data.RouteRepo
}

func (s *acmeStoreWithRouteSync) SaveCertificate(cert *autocert.CertificateData) error {
	if err := s.Store.SaveCertificate(cert); err != nil {
		return err
	}
	for _, domain := range cert.Domains {
		if err := s.routes.SyncACMECert(domain, string(cert.CertPEM), string(cert.KeyPEM)); err != nil {
			return err
		}
	}
	return nil
}

var errACMEUnconfigured = errors.New("controller: Let's Encrypt is not configured")

// autocertConfigFromEnv builds an autocert.Config from environment variables.
// The config is persisted at the cluster level by the bootstrap manifest.
func autocertConfigFromEnv() *autocert.Config {
	cfg := autocert.DefaultConfig()
	cfg.Enabled = true
	cfg.Email = os.Getenv("ACME_EMAIL")
	cfg.CAURL = os.Getenv("ACME_CA_URL")
	if cfg.CAURL == "" {
		cfg.CAURL = autocert.LEDirectoryProduction
	}
	cfg.ChallengeType = autocert.ChallengeType(os.Getenv("ACME_CHALLENGE_TYPE"))
	if cfg.ChallengeType == "" {
		cfg.ChallengeType = autocert.ChallengeHTTP01
	}
	cfg.DNSProvider = autocert.DNSProvider(os.Getenv("ACME_DNS_PROVIDER"))
	if v := os.Getenv("ACME_DNS_CONFIG"); v != "" {
		if err := json.Unmarshal([]byte(v), &cfg.DNSConfig); err != nil {
			logger.Warn("invalid ACME_DNS_CONFIG, ignoring", "err", err)
		}
	}
	return cfg
}

// acmeConfigResponse converts an autocert.Config to the API type.
func acmeConfigResponse(cfg *autocert.Config) *ct.ACMEConfig {
	out := &ct.ACMEConfig{
		Enabled:       cfg.Enabled,
		Email:         cfg.Email,
		CAURL:         cfg.CAURL,
		ChallengeType: string(cfg.ChallengeType),
		DNSProvider:   string(cfg.DNSProvider),
	}
	if cfg.DNSConfig != nil {
		out.DNSConfig = cfg.DNSConfig
	}
	return out
}

// acmeConfigFromRequest converts an API config into an autocert.Config.
func acmeConfigFromRequest(req *ct.ACMEConfig) *autocert.Config {
	cfg := autocert.DefaultConfig()
	cfg.Enabled = req.Enabled
	cfg.Email = req.Email
	if req.CAURL != "" {
		cfg.CAURL = req.CAURL
	}
	cfg.ChallengeType = autocert.ChallengeType(req.ChallengeType)
	if cfg.ChallengeType == "" {
		cfg.ChallengeType = autocert.ChallengeHTTP01
	}
	cfg.DNSProvider = autocert.DNSProvider(req.DNSProvider)
	cfg.DNSConfig = req.DNSConfig
	return cfg
}

func acmeCertFromAutocert(cert *autocert.CertificateData) *ct.ACMECert {
	out := &ct.ACMECert{
		ID:           cert.ID,
		Domains:      cert.Domains,
		Cert:         string(cert.CertPEM),
		Key:          string(cert.KeyPEM),
		AccountEmail: cert.AccountEmail,
		CertURL:      cert.CertURL,
		ExpiresAt:    cert.ExpiresAt,
		CreatedAt:    cert.CreatedAt,
		UpdatedAt:    cert.UpdatedAt,
	}
	if len(cert.Domains) > 0 {
		out.Domain = cert.Domains[0]
	}
	return out
}

// currentACMEConfig returns the effective ACME configuration.
func (c *controllerAPI) currentACMEConfig() *autocert.Config {
	c.acmeMtx.RLock()
	defer c.acmeMtx.RUnlock()
	if c.acmeMgrConfig == nil {
		return autocert.DefaultConfig()
	}
	cfg := *c.acmeMgrConfig
	return &cfg
}

// acmeManager returns the current ACME manager if configured.
func (c *controllerAPI) acmeManager() (*autocert.Manager, error) {
	c.acmeMtx.RLock()
	defer c.acmeMtx.RUnlock()
	if c.acmeMgr == nil {
		return nil, errACMEUnconfigured
	}
	return c.acmeMgr, nil
}

// setACMEConfig validates cfg and replaces the current ACME manager.
func (c *controllerAPI) setACMEConfig(cfg *autocert.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	c.acmeMtx.Lock()
	defer c.acmeMtx.Unlock()
	c.acmeMgrConfig = cfg
	if !cfg.Enabled || cfg.Email == "" {
		c.acmeMgr = nil
		return nil
	}
	c.acmeMgr = autocert.NewManager(cfg, c.acmeStore)
	return nil
}

// acmeObtain serializes certificate issuance to avoid duplicate ACME orders.
func (c *controllerAPI) acmeObtain(domains []string) (*autocert.CertificateData, error) {
	c.acmeObtainMtx.Lock()
	defer c.acmeObtainMtx.Unlock()
	mgr, err := c.acmeManager()
	if err != nil {
		return nil, err
	}
	return mgr.Obtain(domains)
}

// acmeRenewDue renews all certificates that are due for renewal.
func (c *controllerAPI) acmeRenewDue() {
	c.acmeObtainMtx.Lock()
	defer c.acmeObtainMtx.Unlock()
	mgr, err := c.acmeManager()
	if err != nil {
		logger.Warn("acme renewal skipped, not configured")
		return
	}
	for _, err := range mgr.RenewDue() {
		logger.Warn("acme renewal failed", "err", err)
	}
}

func (c *controllerAPI) ProvisionACME(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	var request ct.ACMECertRequest
	if err := httphelper.DecodeJSON(req, &request); err != nil {
		respondWithError(w, err)
		return
	}
	if len(request.Domains) == 0 {
		httphelper.ValidationError(w, "domains", "at least one domain is required")
		return
	}
	cert, err := c.acmeObtain(request.Domains)
	if err != nil {
		respondACMEError(w, err)
		return
	}
	httphelper.JSON(w, 200, acmeCertFromAutocert(cert))
}

func (c *controllerAPI) ListACMECerts(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	certs, err := c.acmeStore.ListCertificates()
	if err != nil {
		respondWithError(w, err)
		return
	}
	out := make([]*ct.ACMECert, 0, len(certs))
	for _, cert := range certs {
		out = append(out, acmeCertFromAutocert(cert))
	}
	httphelper.JSON(w, 200, out)
}

func (c *controllerAPI) GetACMECert(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	params, _ := ctxhelper.ParamsFromContext(ctx)
	cert, err := c.acmeStore.LoadCertificate(params.ByName("domain"))
	if errors.Is(err, data.ErrACMECertNotFound) {
		respondWithError(w, ErrNotFound)
		return
	}
	if err != nil {
		respondWithError(w, err)
		return
	}
	httphelper.JSON(w, 200, acmeCertFromAutocert(cert))
}

func (c *controllerAPI) RevokeACMECert(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	params, _ := ctxhelper.ParamsFromContext(ctx)
	cert, err := c.acmeStore.LoadCertificate(params.ByName("domain"))
	if errors.Is(err, data.ErrACMECertNotFound) {
		respondWithError(w, ErrNotFound)
		return
	}
	if err != nil {
		respondWithError(w, err)
		return
	}
	if err := c.acmeRevoke(cert); err != nil {
		respondACMEError(w, err)
		return
	}
	w.WriteHeader(200)
}

func (c *controllerAPI) acmeRevoke(cert *autocert.CertificateData) error {
	c.acmeObtainMtx.Lock()
	defer c.acmeObtainMtx.Unlock()
	mgr, err := c.acmeManager()
	if err != nil {
		return err
	}
	return mgr.Revoke(cert)
}

func (c *controllerAPI) GetACMEConfig(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	httphelper.JSON(w, 200, acmeConfigResponse(c.currentACMEConfig()))
}

func (c *controllerAPI) UpdateACMEConfig(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	var request ct.ACMEConfig
	if err := httphelper.DecodeJSON(req, &request); err != nil {
		respondWithError(w, err)
		return
	}
	if err := c.setACMEConfig(acmeConfigFromRequest(&request)); err != nil {
		httphelper.ValidationError(w, "acme", err.Error())
		return
	}
	httphelper.JSON(w, 200, acmeConfigResponse(c.currentACMEConfig()))
}

func respondACMEError(w http.ResponseWriter, err error) {
	if err == errACMEUnconfigured {
		httphelper.ServiceUnavailableError(w, errACMEUnconfigured.Error())
		return
	}
	respondWithError(w, err)
}

// acmeChallengeHandler serves HTTP-01 challenge responses from the current
// ACME manager. Mount at /.well-known/acme-challenge/.
type acmeChallengeHandler struct {
	c *controllerAPI
}

func (h acmeChallengeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.c.acmeManager()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mgr.HTTP01Handler().ServeHTTP(w, r)
}

// Start returns a stop function for the background ACME renewal loop.
func (c *controllerAPI) acmeRenewalLoop(interval time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		c.acmeRenewDue()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.acmeRenewDue()
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}
