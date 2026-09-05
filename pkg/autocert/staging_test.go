//go:build !windows

package autocert

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestManagerObtainWithLetsEncryptStaging performs a real end-to-end ACME
// DNS-01 challenge against the Let's Encrypt staging endpoint. The test creates
// a unique TXT record in the AutoDNS-managed zone and then deletes it via
// lego's DNS provider cleanup.
//
// Run it with:
//
//	ACME_LETSENCRYPT_STAGING_TEST=1 go test -vet=off -v -timeout 10m ./pkg/autocert/ -run TestManagerObtainWithLetsEncryptStaging
func TestManagerObtainWithLetsEncryptStaging(t *testing.T) {
	if os.Getenv("ACME_LETSENCRYPT_STAGING_TEST") == "" {
		t.Skip("set ACME_LETSENCRYPT_STAGING_TEST=1 to run Let's Encrypt staging test")
	}

	domain := fmt.Sprintf("flynn-acme-%d.lab.p22.de", time.Now().Unix())
	t.Logf("requesting certificate for %s", domain)

	cfg := DefaultConfig()
	cfg.Email = "acme-test@lab.p22.de"
	cfg.CAURL = LEDirectoryStaging
	cfg.ChallengeType = ChallengeDNS01
	cfg.DNSProvider = DNSProviderAutoDNS
	cfg.DNSConfig = autodnsConfig(t)
	cfg.TOSAgreed = true

	store := newFakeStore()
	mgr := NewManager(cfg, store)

	cert, err := mgr.Obtain([]string{domain})
	if err != nil {
		t.Fatalf("Obtain failed: %v", err)
	}
	if cert == nil {
		t.Fatal("Obtain returned nil certificate")
	}
	if len(cert.Domains) != 1 || cert.Domains[0] != domain {
		t.Fatalf("unexpected certificate domains: %v", cert.Domains)
	}
	if err := verifyCertIssued(cert.CertPEM, domain); err != nil {
		t.Fatalf("certificate validation failed: %v", err)
	}

	t.Logf("successfully obtained certificate for %s (expires %s)", domain, cert.ExpiresAt)
}

func autodnsConfig(t *testing.T) map[string]string {
	user := os.Getenv("ACME_AUTODNS_USER")
	pass := os.Getenv("ACME_AUTODNS_PASSWORD")
	if user == "" || pass == "" {
		user, pass = readEnpassAutodnsCredentials(t)
	}

	context := os.Getenv("ACME_AUTODNS_CONTEXT")
	if context == "" {
		context = "2258"
	}

	return map[string]string{
		"api_user":            user,
		"api_password":        pass,
		"endpoint":            "https://api.autodns.com/v1/",
		"context":             context,
		"ttl":                 "60",
		"propagation_timeout": "5m",
		"polling_interval":    "5s",
	}
}

func readEnpassAutodnsCredentials(t *testing.T) (string, string) {
	vault := "/root/.hermes/enpass-vault"
	masterPath := filepath.Join(vault, ".master-password")
	master, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatalf("failed to read Enpass master password file %s: %v", masterPath, err)
	}

	cmd := exec.Command("enpass-cli", "-vault", vault, "pass", "domain.Pixelx")
	cmd.Stdin = strings.NewReader(strings.TrimSpace(string(master)) + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to read Enpass entry: %v\n%s", err, out)
	}

	var pass string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Enter vault password") {
			continue
		}
		pass = strings.TrimSpace(strings.TrimRight(line, "\""))
		break
	}
	if pass == "" {
		t.Fatalf("failed to extract password from Enpass output: %s", out)
	}
	return "WVK-Astro2", pass
}

func verifyCertIssued(certPEM []byte, domain string) error {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}
	if cert.NotBefore.After(time.Now()) || cert.NotAfter.Before(time.Now()) {
		return fmt.Errorf("certificate is not currently valid: %s - %s", cert.NotBefore, cert.NotAfter)
	}

	matched := false
	for _, name := range cert.DNSNames {
		if name == domain {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("certificate DNSNames %v do not include %s", cert.DNSNames, domain)
	}

	if cert.Issuer.CommonName == cert.Subject.CommonName {
		return fmt.Errorf("certificate appears to be self-signed")
	}
	return nil
}
