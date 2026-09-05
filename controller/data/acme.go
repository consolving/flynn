package data

import (
	"errors"
	"strings"
	"time"

	"github.com/flynn/flynn/pkg/autocert"
	"github.com/flynn/flynn/pkg/postgres"
	"github.com/jackc/pgx"
)

// ACMEStore persists ACME accounts and certificates in PostgreSQL. It
// implements autocert.Store.
type ACMEStore struct {
	db *postgres.DB
}

// ErrACMECertNotFound is returned when a certificate does not exist.
var ErrACMECertNotFound = errors.New("controller: certificate not found")

func NewACMEStore(db *postgres.DB) *ACMEStore {
	return &ACMEStore{db: db}
}

func (s *ACMEStore) SaveAccount(a *autocert.AccountData) error {
	return s.db.QueryRow(
		"acme_account_insert",
		a.Email,
		string(a.PrivateKeyPEM),
		nullableString(a.Registration),
	).Scan(new(string), new(string), new(time.Time), new(time.Time))
}

func (s *ACMEStore) LoadAccount() (*autocert.AccountData, error) {
	var (
		acct       autocert.AccountData
		privateKey string
		reg        *string
	)
	err := s.db.QueryRow("acme_account_select").Scan(
		new(string),
		&acct.Email,
		&privateKey,
		&reg,
		new(time.Time),
		new(time.Time),
	)
	if err == pgx.ErrNoRows {
		return nil, autocert.ErrNoAccount
	}
	if err != nil {
		return nil, err
	}
	acct.PrivateKeyPEM = []byte(privateKey)
	if reg != nil {
		acct.Registration = []byte(*reg)
	}
	return &acct, nil
}

func (s *ACMEStore) SaveCertificate(c *autocert.CertificateData) error {
	if len(c.Domains) == 0 {
		return errors.New("controller: cannot store certificate without domains")
	}
	return s.db.QueryRow(
		"acme_certificate_upsert",
		normalizeDomain(c.Domains[0]),
		c.Domains,
		string(c.CertPEM),
		string(c.KeyPEM),
		c.CertURL,
		c.AccountEmail,
		c.ExpiresAt,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

func (s *ACMEStore) LoadCertificate(domain string) (*autocert.CertificateData, error) {
	c, err := scanACMECertificate(s.db.QueryRow("acme_certificate_select", normalizeDomain(domain)))
	if err == pgx.ErrNoRows {
		return nil, ErrACMECertNotFound
	}
	return c, err
}

func (s *ACMEStore) ListCertificates() ([]*autocert.CertificateData, error) {
	rows, err := s.db.Query("acme_certificate_select_all")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []*autocert.CertificateData
	for rows.Next() {
		c, err := scanACMECertificate(rows)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

func (s *ACMEStore) DeleteCertificate(domain string) error {
	return s.db.Exec("acme_certificate_delete", normalizeDomain(domain))
}

func scanACMECertificate(row postgres.Scanner) (*autocert.CertificateData, error) {
	var (
		c       autocert.CertificateData
		domains []string
	)
	if err := row.Scan(
		&c.ID,
		new(string),
		&domains,
		&c.CertPEM,
		&c.KeyPEM,
		&c.CertURL,
		&c.AccountEmail,
		&c.ExpiresAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.Domains = domains
	return &c, nil
}

func nullableString(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(d), ".")
}
