package autocert

import (
	"errors"
	"time"
)

// ErrNoAccount is returned by LoadAccount when no account has been saved.
var ErrNoAccount = errors.New("autocert: no account found")

// AccountData is the persisted representation of an ACME account.
type AccountData struct {
	// Email is the ACME account contact email.
	Email string

	// PrivateKeyPEM is the PKCS#1 RSA private key in PEM format.
	PrivateKeyPEM []byte

	// Registration is the JSON-encoded lego/registration.Resource.
	Registration []byte
}

// CertificateData is the persisted representation of an ACME certificate.
type CertificateData struct {
	// ID is a unique identifier for the certificate.
	ID string

	// CertPEM contains the certificate chain in PEM format.
	CertPEM []byte

	// KeyPEM contains the private key in PEM format.
	KeyPEM []byte

	// Domains lists the domains the certificate is valid for.
	Domains []string

	// AccountEmail is the ACME account used to issue the certificate.
	AccountEmail string

	// CertURL is the ACME certificate URL, used for renewal.
	CertURL string

	// ExpiresAt is the certificate NotAfter time.
	ExpiresAt time.Time

	// CreatedAt is when the certificate was first stored.
	CreatedAt time.Time

	// UpdatedAt is when the certificate was last updated.
	UpdatedAt time.Time
}

// Store persists ACME account and certificate data. Implementations are
// provided by the controller (PostgreSQL).
type Store interface {
	// SaveAccount persists an ACME account.
	SaveAccount(*AccountData) error

	// LoadAccount returns the persisted ACME account or ErrNoAccount.
	LoadAccount() (*AccountData, error)

	// SaveCertificate persists a certificate. The implementation should
	// upsert by the set of domains.
	SaveCertificate(*CertificateData) error

	// LoadCertificate returns the most recent certificate for a domain.
	LoadCertificate(domain string) (*CertificateData, error)

	// ListCertificates returns all stored certificates.
	ListCertificates() ([]*CertificateData, error)

	// DeleteCertificate removes a certificate.
	DeleteCertificate(domain string) error
}
