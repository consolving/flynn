package autocert

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/autodns"
)

func newDNS01Provider(cfg *Config) (challenge.Provider, error) {
	switch cfg.DNSProvider {
	case DNSProviderAutoDNS:
		return newAutoDNSProvider(cfg.DNSConfig)
	default:
		return nil, fmt.Errorf("autocert: unsupported dns provider %q", cfg.DNSProvider)
	}
}

func newAutoDNSProvider(settings map[string]string) (challenge.Provider, error) {
	c := autodns.NewDefaultConfig()

	c.Username = settings["api_user"]
	c.Password = settings["api_password"]

	if c.Username == "" || c.Password == "" {
		return nil, fmt.Errorf("autocert: autodns api_user and api_password are required")
	}

	if endpoint := settings["endpoint"]; endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns endpoint: %w", err)
		}
		c.Endpoint = u
	}

	if ctx := settings["context"]; ctx != "" {
		n, err := strconv.Atoi(ctx)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns context: %w", err)
		}
		c.Context = n
	}

	if ttl := settings["ttl"]; ttl != "" {
		n, err := strconv.Atoi(ttl)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns ttl: %w", err)
		}
		c.TTL = n
	}

	if timeout := settings["propagation_timeout"]; timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns propagation_timeout: %w", err)
		}
		c.PropagationTimeout = d
	}

	if interval := settings["polling_interval"]; interval != "" {
		d, err := time.ParseDuration(interval)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns polling_interval: %w", err)
		}
		c.PollingInterval = d
	}

	return autodns.NewDNSProviderConfig(c)
}
