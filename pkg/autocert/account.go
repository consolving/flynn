package autocert

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/go-acme/lego/v4/registration"
)

// Account implements lego's registration.User interface.
type Account struct {
	Email        string
	PrivateKey   *rsa.PrivateKey
	Registration *registration.Resource
}

// GetEmail returns the ACME account email.
func (a *Account) GetEmail() string {
	return a.Email
}

// GetRegistration returns the ACME registration resource.
func (a *Account) GetRegistration() *registration.Resource {
	return a.Registration
}

// GetPrivateKey returns the account private key.
func (a *Account) GetPrivateKey() crypto.PrivateKey {
	return a.PrivateKey
}

// NewAccount creates a new ACME account with a fresh RSA private key.
func NewAccount(email string) (*Account, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("autocert: failed to generate account key: %w", err)
	}
	return &Account{Email: email, PrivateKey: priv}, nil
}

// encodePrivateKeyPEM encodes an RSA private key to PKCS#1 PEM.
func encodePrivateKeyPEM(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

// decodePrivateKeyPEM decodes a PKCS#1 RSA private key from PEM.
func decodePrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("autocert: failed to decode PEM private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
