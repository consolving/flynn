package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	controller "github.com/flynn/flynn/controller/client"
	ct "github.com/flynn/flynn/controller/types"
	"github.com/flynn/go-docopt"
)

func init() {
	register("cert", runCert, `
usage: flynn cert
       flynn cert letsencrypt [--challenge <challenge>] [--dns-provider <provider>] [--dns-config <key=value>...] <domain>...
       flynn cert letsencrypt --status <domain>
       flynn cert letsencrypt --revoke <domain>
       flynn cert letsencrypt --list
       flynn cert letsencrypt --config [--enabled <enabled>] [--email <email>] [--ca-url <url>] [--challenge <challenge>] [--dns-provider <provider>] [--dns-config <key=value>...]

Manage TLS certificates and ACME (Let's Encrypt) configuration.

Options:
	--status=<domain>            show the certificate status for a domain
	--revoke=<domain>            revoke the certificate for a domain
	--list                       list certificates managed via Let's Encrypt
	--config                     show or update the ACME configuration
	--enabled=<enabled>          enable or disable ACME provisioning (true/false)
	--email=<email>              ACME account contact email
	--ca-url=<url>               ACME directory URL
	--challenge=<challenge>      challenge type: http-01 or dns-01 [default: http-01]
	--dns-provider=<provider>    DNS provider for dns-01 challenges (e.g. autodns)
	--dns-config=<key=value>     DNS provider configuration (repeatable)

Commands:
	With no arguments, shows this help.

Examples:

	$ flynn cert letsencrypt example.com

	$ flynn cert letsencrypt example.com www.example.com

	$ flynn cert letsencrypt example.com --challenge dns-01 --dns-provider autodns --dns-config api_user=user --dns-config api_password=secret

	$ flynn cert letsencrypt --list

	$ flynn cert letsencrypt --status example.com

	$ flynn cert letsencrypt --revoke example.com

	$ flynn cert letsencrypt --config --email admin@example.com
`)
}

func runCert(args *docopt.Args, client controller.Client) error {
	if !args.Bool["letsencrypt"] {
		return errors.New("Usage: flynn cert letsencrypt ...")
	}
	switch {
	case args.Bool["--list"]:
		return runACMECertList(client)
	case args.String["--status"] != "":
		return runACMECertStatus(args, client)
	case args.String["--revoke"] != "":
		return runACMECertRevoke(args, client)
	case args.Bool["--config"]:
		return runACMECertConfig(args, client)
	default:
		return runACMECertIssue(args, client)
	}
}

func runACMECertIssue(args *docopt.Args, client controller.Client) error {
	domains := args.All["<domain>"].([]string)
	if len(domains) == 0 {
		return errors.New("at least one domain is required")
	}
	challenge := args.String["--challenge"]
	if challenge == "" {
		challenge = "http-01"
	}

	cert, err := client.ProvisionACMECert(&ct.ACMECertRequest{Domains: domains})
	if err != nil {
		return err
	}
	fmt.Printf("Provisioned certificate for %s (expires %s)\n", cert.Domain, cert.ExpiresAt.Format(time.RFC3339))
	return nil
}

func runACMECertStatus(args *docopt.Args, client controller.Client) error {
	domain := args.String["<domain>"]
	cert, err := client.GetACMECert(domain)
	if err != nil {
		return err
	}
	printACMECert(cert)
	return nil
}

func runACMECertList(client controller.Client) error {
	certs, err := client.ACMECertList()
	if err != nil {
		return err
	}
	if len(certs) == 0 {
		fmt.Println("No certificates managed via Let's Encrypt.")
		return nil
	}
	w := tabWriter()
	defer w.Flush()
	listRec(w, "DOMAIN", "EXPIRES", "ACCOUNT")
	for _, cert := range certs {
		listRec(w, cert.Domain, humanTime(&cert.ExpiresAt), cert.AccountEmail)
	}
	return nil
}

func runACMECertRevoke(args *docopt.Args, client controller.Client) error {
	domain := args.String["<domain>"]
	if err := client.RevokeACMECert(domain); err != nil {
		return err
	}
	fmt.Printf("Revoked certificate for %s\n", domain)
	return nil
}

func runACMECertConfig(args *docopt.Args, client controller.Client) error {
	cfg, err := client.GetACMEConfig()
	if err != nil {
		return err
	}

	update := false
	req := &ct.ACMEConfig{
		Enabled:       cfg.Enabled,
		Email:         cfg.Email,
		CAURL:         cfg.CAURL,
		ChallengeType: cfg.ChallengeType,
		DNSProvider:   cfg.DNSProvider,
		DNSConfig:     cfg.DNSConfig,
	}
	if v := args.String["--email"]; v != "" {
		req.Email = v
		update = true
	}
	if v := args.String["--ca-url"]; v != "" {
		req.CAURL = v
		update = true
	}
	if v := args.String["--challenge"]; v != "" {
		req.ChallengeType = v
		update = true
	}
	if v := args.String["--dns-provider"]; v != "" {
		req.DNSProvider = v
		update = true
	}
	if v := args.String["--enabled"]; v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid value for --enabled: %q", v)
		}
		req.Enabled = enabled
		update = true
	}
	if kvs, ok := args.All["--dns-config"].([]string); ok && len(kvs) > 0 {
		config := make(map[string]string)
		for _, kv := range kvs {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid dns config %q, expected key=value", kv)
			}
			config[parts[0]] = parts[1]
		}
		req.DNSConfig = config
		update = true
	}

	if update {
		var err error
		cfg, err = client.UpdateACMEConfig(req)
		if err != nil {
			return err
		}
	}

	fmt.Printf("Enabled:        %t\n", cfg.Enabled)
	fmt.Printf("Email:          %s\n", cfg.Email)
	if cfg.CAURL != "" {
		fmt.Printf("CA URL:         %s\n", cfg.CAURL)
	}
	fmt.Printf("Challenge type: %s\n", cfg.ChallengeType)
	if cfg.DNSProvider != "" {
		fmt.Printf("DNS provider:   %s\n", cfg.DNSProvider)
	}
	return nil
}

func printACMECert(cert *ct.ACMECert) {
	fmt.Printf("ID:             %s\n", cert.ID)
	fmt.Printf("Domain:         %s\n", cert.Domain)
	fmt.Printf("Domains:        %s\n", strings.Join(cert.Domains, ", "))
	fmt.Printf("Account email:  %s\n", cert.AccountEmail)
	fmt.Printf("Issued:         %s\n", cert.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:        %s\n", cert.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("Expires:        %s (%s)\n", cert.ExpiresAt.Format(time.RFC3339), humanTime(&cert.ExpiresAt))
}
