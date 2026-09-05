package data

import (
	"strings"
	"testing"
	"time"

	"github.com/flynn/flynn/pkg/autocert"
	"github.com/flynn/flynn/pkg/random"
	"github.com/flynn/flynn/router/testutils"
	. "github.com/flynn/go-check"
)

func TestACMEStore(t *testing.T) { TestingT(t) }

type ACMEStoreSuite struct{}

var _ = Suite(&ACMEStoreSuite{})

func (ACMEStoreSuite) TestAccountCRUD(c *C) {
	db := setupTestDB(c, "controllertest_acme_store_account")
	m := &testMigrator{c: c, db: db}
	m.migrateTo(50)

	store := NewACMEStore(db)

	// Loading a missing account returns ErrNoAccount.
	_, err := store.LoadAccount()
	c.Assert(err, Equals, autocert.ErrNoAccount)

	// Save and load an account.
	account := &autocert.AccountData{
		Email:         "acme@example.com",
		PrivateKeyPEM: []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALRiMLAH\n-----END RSA PRIVATE KEY-----\n"),
		Registration:  []byte(`{"uri":"https://acme.example.com/account/1"}`),
	}
	c.Assert(store.SaveAccount(account), IsNil)

	loaded, err := store.LoadAccount()
	c.Assert(err, IsNil)
	c.Assert(loaded.Email, Equals, account.Email)
	c.Assert(string(loaded.PrivateKeyPEM), Equals, string(account.PrivateKeyPEM))
	c.Assert(string(loaded.Registration), Equals, string(account.Registration))

	// Updating the registration for the same email overwrites the row.
	account.Registration = []byte(`{"uri":"https://acme.example.com/account/2"}`)
	c.Assert(store.SaveAccount(account), IsNil)

	loaded, err = store.LoadAccount()
	c.Assert(err, IsNil)
	c.Assert(string(loaded.Registration), Equals, string(account.Registration))
}

func (ACMEStoreSuite) TestCertificateCRUD(c *C) {
	db := setupTestDB(c, "controllertest_acme_store_cert")
	m := &testMigrator{c: c, db: db}
	m.migrateTo(50)

	store := NewACMEStore(db)

	// Loading a missing certificate returns ErrACMECertNotFound.
	_, err := store.LoadCertificate("example.com")
	c.Assert(err, Equals, ErrACMECertNotFound)

	cert := testACMECert(c, "example.com")
	c.Assert(store.SaveCertificate(cert), IsNil)
	c.Assert(cert.ID, Not(Equals), "")
	c.Assert(cert.CreatedAt.IsZero(), Equals, false)
	c.Assert(cert.UpdatedAt.IsZero(), Equals, false)

	loaded, err := store.LoadCertificate("example.com")
	c.Assert(err, IsNil)
	c.Assert(loaded.Domains, DeepEquals, []string{"example.com"})
	c.Assert(loaded.CertPEM, DeepEquals, cert.CertPEM)
	c.Assert(loaded.KeyPEM, DeepEquals, cert.KeyPEM)
	c.Assert(loaded.CertURL, Equals, cert.CertURL)
	c.Assert(loaded.AccountEmail, Equals, cert.AccountEmail)
	c.Assert(loaded.ExpiresAt.Equal(cert.ExpiresAt), Equals, true)

	// Upsert updates an existing certificate for the same domain.
	cert.CertURL = "https://acme.example.com/cert/renewed"
	cert.ExpiresAt = cert.ExpiresAt.Add(24 * time.Hour)
	oldUpdatedAt := loaded.UpdatedAt
	c.Assert(store.SaveCertificate(cert), IsNil)

	loaded, err = store.LoadCertificate("example.com")
	c.Assert(err, IsNil)
	c.Assert(loaded.CertURL, Equals, "https://acme.example.com/cert/renewed")
	c.Assert(loaded.ExpiresAt.Equal(cert.ExpiresAt), Equals, true)
	c.Assert(loaded.UpdatedAt.After(oldUpdatedAt), Equals, true)

	// Domain normalization: lookup with trailing dot or uppercase works.
	loaded, err = store.LoadCertificate("EXAMPLE.COM.")
	c.Assert(err, IsNil)
	c.Assert(loaded.Domains, DeepEquals, []string{"example.com"})

	// List returns all non-deleted certificates.
	cert2 := testACMECert(c, "www.example.com")
	c.Assert(store.SaveCertificate(cert2), IsNil)

	list, err := store.ListCertificates()
	c.Assert(err, IsNil)
	c.Assert(len(list), Equals, 2)

	// Delete marks the certificate as deleted.
	c.Assert(store.DeleteCertificate("example.com"), IsNil)
	_, err = store.LoadCertificate("example.com")
	c.Assert(err, Equals, ErrACMECertNotFound)

	list, err = store.ListCertificates()
	c.Assert(err, IsNil)
	c.Assert(len(list), Equals, 1)
	c.Assert(list[0].Domains[0], Equals, "www.example.com")
}

func (ACMEStoreSuite) TestCertificateDomainUniqueness(c *C) {
	db := setupTestDB(c, "controllertest_acme_store_unique")
	m := &testMigrator{c: c, db: db}
	m.migrateTo(50)

	store := NewACMEStore(db)

	cert1 := testACMECert(c, "shared.example.com")
	cert1.ExpiresAt = time.Now().Add(30 * 24 * time.Hour)
	c.Assert(store.SaveCertificate(cert1), IsNil)

	// Deleting then re-adding the same domain should succeed (partial unique
	// index only applies to non-deleted rows).
	c.Assert(store.DeleteCertificate("shared.example.com"), IsNil)

	cert2 := testACMECert(c, "shared.example.com")
	cert2.ExpiresAt = time.Now().Add(60 * 24 * time.Hour)
	c.Assert(store.SaveCertificate(cert2), IsNil)

	loaded, err := store.LoadCertificate("shared.example.com")
	c.Assert(err, IsNil)
	c.Assert(loaded.ExpiresAt.Equal(cert2.ExpiresAt), Equals, true)
}

func (ACMEStoreSuite) TestACMECertSyncsBoundRoutes(c *C) {
	db := setupTestDB(c, "controllertest_acme_store_sync")
	m := &testMigrator{c: c, db: db}
	m.migrateTo(51)

	appID := random.UUID()
	c.Assert(db.Exec(`INSERT INTO apps (app_id, name) VALUES ($1, $2)`, appID, "acme-sync-app"), IsNil)

	var routeID string
	c.Assert(db.QueryRow(`
		INSERT INTO http_routes (parent_ref, service, domain, acme_domain, drain_backends)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		"app/"+appID, "acme-sync-app-web", "example.com", "example.com", true,
	).Scan(&routeID), IsNil)

	routes := NewRouteRepo(db)
	tlsCert := testutils.TLSConfigForDomain("example.com")
	certPEM := strings.TrimSpace(tlsCert.Cert)
	keyPEM := strings.TrimSpace(tlsCert.PrivateKey)
	c.Assert(routes.SyncACMECert("example.com", certPEM, keyPEM), IsNil)

	var certID, certCert, certKey string
	c.Assert(db.QueryRow(`
		SELECT c.id, c.cert, c.key FROM certificates c
		JOIN route_certificates rc ON rc.certificate_id = c.id
		WHERE rc.http_route_id = $1`, routeID,
	).Scan(&certID, &certCert, &certKey), IsNil)
	c.Assert(certID, Not(Equals), "")
	c.Assert(strings.TrimSpace(certCert), Equals, certPEM)
	c.Assert(strings.TrimSpace(certKey), Equals, keyPEM)

	// Renewing with a fresh cert updates the route to the new certificate.
	tlsCert2 := testutils.RefreshTLSConfigForDomain("example.com")
	certPEM2 := strings.TrimSpace(tlsCert2.Cert)
	keyPEM2 := strings.TrimSpace(tlsCert2.PrivateKey)
	c.Assert(routes.SyncACMECert("example.com", certPEM2, keyPEM2), IsNil)

	var certID2 string
	c.Assert(db.QueryRow(`
		SELECT c.id FROM certificates c
		JOIN route_certificates rc ON rc.certificate_id = c.id
		WHERE rc.http_route_id = $1`, routeID,
	).Scan(&certID2), IsNil)
	c.Assert(certID2, Not(Equals), certID)
}

func testACMECert(c *C, domain string) *autocert.CertificateData {
	tlsCert := testutils.TLSConfigForDomain(domain)
	return &autocert.CertificateData{
		CertPEM:      []byte(strings.TrimSpace(tlsCert.Cert)),
		KeyPEM:       []byte(strings.TrimSpace(tlsCert.PrivateKey)),
		Domains:      []string{domain},
		AccountEmail: "acme@example.com",
		CertURL:      "https://acme.example.com/cert/" + random.String(8),
		ExpiresAt:    time.Now().Add(90 * 24 * time.Hour),
	}
}
