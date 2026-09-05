package autocert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/miekg/dns"
)

// autodnsProvider implements challenge.Provider using AutoDNS's zone stream
// API. Unlike lego's bundled provider, this implementation resolves the real
// zone apex before adding/removing records, so it works for subdomains like
// flynn.example.com where the managed zone is example.com.
type autodnsProvider struct {
	username  string
	password  string
	context   int
	ttl       int
	endpoint  *url.URL
	http      *http.Client
	propagate time.Duration
	poll      time.Duration
}

func newAutodnsProvider(settings map[string]string) (challenge.Provider, error) {
	username := strings.TrimSpace(settings["api_user"])
	password := strings.TrimSpace(settings["api_password"])
	if username == "" || password == "" {
		return nil, fmt.Errorf("autocert: autodns api_user and api_password are required")
	}

	endpoint := "https://api.autodns.com/v1/"
	if v := settings["endpoint"]; v != "" {
		endpoint = v
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("autocert: invalid autodns endpoint: %w", err)
	}

	ctx := 4
	if v := settings["context"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns context: %w", err)
		}
		ctx = n
	}

	ttl := 60
	if v := settings["ttl"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns ttl: %w", err)
		}
		ttl = n
	}

	propagate := 5 * time.Minute
	if v := settings["propagation_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns propagation_timeout: %w", err)
		}
		propagate = d
	}

	poll := 5 * time.Second
	if v := settings["polling_interval"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("autocert: invalid autodns polling_interval: %w", err)
		}
		poll = d
	}

	return &autodnsProvider{
		username:  username,
		password:  password,
		context:   ctx,
		ttl:       ttl,
		endpoint:  u,
		http:      &http.Client{Timeout: 30 * time.Second},
		propagate: propagate,
		poll:      poll,
	}, nil
}

func (p *autodnsProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	zone, err := p.findZone(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("autodns: could not determine zone for %s: %w", info.EffectiveFQDN, err)
	}

	name := relativeRecordName(info.EffectiveFQDN, zone)
	if err := p.update(zone, "adds", name, info.Value); err != nil {
		return fmt.Errorf("autodns: add record: %w", err)
	}

	if err := p.waitForTXT(info.EffectiveFQDN, info.Value); err != nil {
		return fmt.Errorf("autodns: %w", err)
	}
	return nil
}

func (p *autodnsProvider) CleanUp(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	zone, err := p.findZone(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("autodns: could not determine zone for %s: %w", info.EffectiveFQDN, err)
	}

	name := relativeRecordName(info.EffectiveFQDN, zone)
	if err := p.update(zone, "removes", name, info.Value); err != nil {
		return fmt.Errorf("autodns: remove record: %w", err)
	}
	return nil
}

func (p *autodnsProvider) findZone(fqdn string) (string, error) {
	zone, err := dns01.FindZoneByFqdn(fqdn)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(zone, "."), nil
}

func relativeRecordName(fqdn, zone string) string {
	fqdn = strings.TrimSuffix(fqdn, ".")
	zone = strings.TrimSuffix(zone, ".")
	if strings.HasSuffix(strings.ToLower(fqdn), "."+strings.ToLower(zone)) {
		return strings.TrimSuffix(fqdn, "."+zone)
	}
	if strings.EqualFold(fqdn, zone) {
		return ""
	}
	return fqdn
}

func (p *autodnsProvider) update(zone, action, name, value string) error {
	payload := &zoneStream{
		Adds:    nil,
		Removes: nil,
	}
	record := resourceRecord{Name: name, Type: "TXT", TTL: int64(p.ttl), Value: value}
	switch action {
	case "adds":
		payload.Adds = []*resourceRecord{&record}
	case "removes":
		payload.Removes = []*resourceRecord{&record}
	default:
		return fmt.Errorf("unknown action %q", action)
	}

	u := p.endpoint.JoinPath("zone", zone, "_stream")

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, u.String(), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Domainrobot-Context", strconv.Itoa(p.context))
	req.SetBasicAuth(p.username, p.password)

	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status code: %d body: %s", resp.StatusCode, body)
	}
	return nil
}

func (p *autodnsProvider) waitForTXT(fqdn, value string) error {
	deadline := time.Now().Add(p.propagate)
	for time.Now().Before(deadline) {
		found, err := p.txtExists(fqdn, value)
		if err == nil && found {
			return nil
		}
		time.Sleep(p.poll)
	}
	return fmt.Errorf("TXT %s did not propagate within %s", fqdn, p.propagate)
}

func (p *autodnsProvider) txtExists(fqdn, value string) (bool, error) {
	zone, err := p.findZone(fqdn)
	if err != nil {
		return false, err
	}

	authoritative, err := p.authoritativeServers(zone)
	if err != nil {
		return false, err
	}

	msg := new(dns.Msg)
	msg.SetQuestion(fqdn, dns.TypeTXT)
	msg.RecursionDesired = false

	for _, ns := range authoritative {
		r, _, err := (&dns.Client{Timeout: 5 * time.Second}).Exchange(msg, ns)
		if err != nil {
			continue
		}
		if r.Rcode != dns.RcodeSuccess {
			continue
		}
		for _, rr := range r.Answer {
			txt, ok := rr.(*dns.TXT)
			if !ok {
				continue
			}
			joined := strings.Join(txt.Txt, "")
			if joined == value {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *autodnsProvider) authoritativeServers(zone string) ([]string, error) {
	// Use system recursive resolvers to fetch NS records for the zone.
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 {
		return nil, fmt.Errorf("no nameservers in /etc/resolv.conf")
	}

	zone = dns.Fqdn(zone)
	msg := new(dns.Msg)
	msg.SetQuestion(zone, dns.TypeNS)
	msg.RecursionDesired = true

	var nss []string
	for _, s := range config.Servers {
		nsAddr := net.JoinHostPort(s, "53")
		r, _, err := (&dns.Client{Timeout: 5 * time.Second}).Exchange(msg, nsAddr)
		if err != nil {
			continue
		}
		if r.Rcode != dns.RcodeSuccess {
			continue
		}
		for _, rr := range r.Answer {
			if n, ok := rr.(*dns.NS); ok {
				// Resolve nameserver hostname to IPv4/IPv6 and return addresses.
				addrs, err := net.LookupHost(n.Ns)
				if err != nil {
					continue
				}
				for _, a := range addrs {
					nss = append(nss, net.JoinHostPort(a, "53"))
				}
			}
		}
		if len(nss) > 0 {
			break
		}
	}
	if len(nss) == 0 {
		return nil, fmt.Errorf("could not find authoritative nameservers for %s", zone)
	}
	return nss, nil
}

type zoneStream struct {
	Adds    []*resourceRecord `json:"adds,omitempty"`
	Removes []*resourceRecord `json:"removes,omitempty"`
}

type resourceRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   int64  `json:"ttl"`
	Value string `json:"value"`
}
