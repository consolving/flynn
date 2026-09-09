package autocert

import (
	"fmt"

	"github.com/go-acme/lego/v4/challenge"
)

func newDNS01Provider(cfg *Config) (challenge.Provider, error) {
	switch cfg.DNSProvider {
	case DNSProviderAutoDNS:
		return newAutodnsProvider(cfg.DNSConfig)
	default:
		return nil, fmt.Errorf("autocert: unsupported dns provider %q", cfg.DNSProvider)
	}
}
