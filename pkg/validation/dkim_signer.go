package validation

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"
)

// DKIMSigner signs outbound emails with DKIM signatures
type DKIMSigner struct {
	domain     string
	selector   string
	privateKey *rsa.PrivateKey
	logger     *slog.Logger
}

// NewDKIMSigner creates a new DKIM signer
func NewDKIMSigner(domain, selector, privateKeyPath string, logger *slog.Logger) (*DKIMSigner, error) {
	// Load private key from file
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}

	// Parse PEM block
	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block from private key")
	}

	// Parse RSA private key
	var privateKey *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS1 private key: %w", err)
		}
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS8 private key: %w", err)
		}
		var ok bool
		privateKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
	default:
		return nil, fmt.Errorf("unsupported key type: %s", block.Type)
	}

	return &DKIMSigner{
		domain:     domain,
		selector:   selector,
		privateKey: privateKey,
		logger:     logger,
	}, nil
}

// SignEmail signs an email with DKIM signature
func (s *DKIMSigner) SignEmail(rawEmail string) (string, error) {
	// Create DKIM signer options
	options := &dkim.SignOptions{
		Domain:   s.domain,
		Selector: s.selector,
		Signer:   s.privateKey,
		// Sign common headers
		HeaderKeys: []string{
			"From",
			"To",
			"Subject",
			"Date",
			"Message-ID",
			"MIME-Version",
			"Content-Type",
		},
		// Use current timestamp
		Expiration: time.Now().Add(72 * time.Hour), // Signature valid for 72 hours
	}

	// Sign the email
	var signedEmail strings.Builder
	if err := dkim.Sign(&signedEmail, strings.NewReader(rawEmail), options); err != nil {
		s.logger.Error("Failed to sign email with DKIM",
			"domain", s.domain,
			"selector", s.selector,
			"error", err)
		return "", fmt.Errorf("DKIM signing failed: %w", err)
	}

	s.logger.Debug("Email signed with DKIM",
		"domain", s.domain,
		"selector", s.selector)

	return signedEmail.String(), nil
}

// Domain returns the DKIM domain
func (s *DKIMSigner) Domain() string {
	return s.domain
}

// Selector returns the DKIM selector
func (s *DKIMSigner) Selector() string {
	return s.selector
}
