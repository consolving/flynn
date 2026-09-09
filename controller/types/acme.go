package types

import "time"

// ACMECertRequest is the payload for provisioning a Let's Encrypt certificate.
type ACMECertRequest struct {
	// Domains lists the domains to obtain a certificate for. The first
	// domain is used as the primary/common name.
	Domains []string `json:"domains"`
}

// ACMECert is a Let's Encrypt certificate managed by the controller.
type ACMECert struct {
	ID           string    `json:"id"`
	Domain       string    `json:"domain"`
	Domains      []string  `json:"domains"`
	Cert         string    `json:"cert"`
	Key          string    `json:"key,omitempty"`
	AccountEmail string    `json:"account_email"`
	CertURL      string    `json:"cert_url"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ACMEConfig is the controller's ACME configuration.
type ACMEConfig struct {
	Enabled       bool              `json:"enabled"`
	Email         string            `json:"email"`
	CAURL         string            `json:"ca_url"`
	ChallengeType string            `json:"challenge_type"`
	DNSProvider   string            `json:"dns_provider,omitempty"`
	DNSConfig     map[string]string `json:"dns_config,omitempty"`
}
