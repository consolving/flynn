// Package autocert provides Let's Encrypt ACME certificate provisioning and
// renewal on top of go-acme/lego.
package autocert

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Let's Encrypt ACME directory URLs.
const (
	LEDirectoryProduction = "https://acme-v02.api.letsencrypt.org/directory"
	LEDirectoryStaging    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// DefaultRenewBefore is the default duration before expiry at which a
// certificate will be renewed (30 days).
const DefaultRenewBefore = 30 * 24 * time.Hour

// ChallengeType selects the ACME challenge method.
type ChallengeType string

const (
	ChallengeHTTP01 ChallengeType = "http-01"
	ChallengeDNS01  ChallengeType = "dns-01"
)

// DNSProvider selects the DNS provider used for DNS-01 challenges.
type DNSProvider string

const (
	DNSProviderAutoDNS DNSProvider = "autodns"
)

// Config configures ACME certificate management.
type Config struct {
	// Enabled enables automatic certificate provisioning.
	Enabled bool `json:"enabled"`

	// Email is the ACME account contact email.
	Email string `json:"email"`

	// CAURL is the ACME directory URL. Defaults to Let's Encrypt production.
	CAURL string `json:"ca_url"`

	// ChallengeType is either "http-01" or "dns-01".
	ChallengeType ChallengeType `json:"challenge_type"`

	// DNSProvider selects the DNS-01 provider (e.g. "autodns").
	DNSProvider DNSProvider `json:"dns_provider"`

	// DNSConfig holds provider-specific settings. For AutoDNS:
	//   api_user, api_password, endpoint, context, ttl.
	DNSConfig map[string]string `json:"dns_config"`

	// HTTPClient, if set, is used for ACME directory and challenge HTTP
	// requests. Tests use this to trust a custom CA such as Pebble.
	HTTPClient *http.Client `json:"-"`

	// RenewBefore is the time before expiry to trigger renewal.
	// Defaults to DefaultRenewBefore.
	RenewBefore time.Duration `json:"renew_before"`

	// TOSAgreed must be true to register with the ACME server.
	TOSAgreed bool `json:"tos_agreed"`
}

// Validate returns an error if the configuration is incomplete or invalid.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Email == "" {
		return errors.New("autocert: email is required")
	}
	switch c.ChallengeType {
	case ChallengeHTTP01, ChallengeDNS01:
	default:
		return fmt.Errorf("autocert: invalid challenge type %q", c.ChallengeType)
	}
	if c.ChallengeType == ChallengeDNS01 {
		if c.DNSProvider == "" {
			return errors.New("autocert: dns_provider is required for dns-01")
		}
	}
	return nil
}

// DefaultConfig returns a Config with sane defaults.
func DefaultConfig() *Config {
	return &Config{
		CAURL:       LEDirectoryProduction,
		RenewBefore: DefaultRenewBefore,
		TOSAgreed:   true,
	}
}
