package smtp

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	ssha512PrefixB64         = "{SSHA512}"
	ssha512PrefixB64Explicit = "{SSHA512.b64}"
	ssha512PrefixHex         = "{SSHA512.HEX}"

	sha512PrefixB64         = "{SHA512}"
	sha512PrefixB64Explicit = "{SHA512.b64}"
	sha512PrefixHex         = "{SHA512.HEX}"

	blfCryptPrefix = "{BLF-CRYPT}"

	// Standard bcrypt prefixes
	bcryptPrefix2a = "$2a$"
	bcryptPrefix2b = "$2b$"
	bcryptPrefix2y = "$2y$"

	// sha512HashLength is the expected length of a SHA512 hash in bytes.
	sha512HashLength = 64
	// ssha512MinSaltLength is the minimum length of a salt for SSHA512.
	ssha512MinSaltLength = 1
)

// VerifyPassword checks if the provided password matches the stored password hash.
// It supports bcrypt, BLF-CRYPT, SSHA512, and SHA512 formats with different encodings.
func VerifyPassword(hashedPassword, password string) error {
	switch {
	case strings.HasPrefix(hashedPassword, ssha512PrefixB64),
		strings.HasPrefix(hashedPassword, ssha512PrefixB64Explicit),
		strings.HasPrefix(hashedPassword, ssha512PrefixHex):
		return verifySSHA512(hashedPassword, password)

	case strings.HasPrefix(hashedPassword, sha512PrefixB64),
		strings.HasPrefix(hashedPassword, sha512PrefixB64Explicit),
		strings.HasPrefix(hashedPassword, sha512PrefixHex):
		return verifySHA512(hashedPassword, password)

	case strings.HasPrefix(hashedPassword, blfCryptPrefix):
		// BLF-CRYPT is just bcrypt with a prefix
		return verifyBcrypt(hashedPassword, password)

	case strings.HasPrefix(hashedPassword, bcryptPrefix2a),
		strings.HasPrefix(hashedPassword, bcryptPrefix2b),
		strings.HasPrefix(hashedPassword, bcryptPrefix2y):
		// Standard bcrypt format
		return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))

	default:
		// No known scheme prefix
		return fmt.Errorf("unknown password hash scheme: %s", hashedPassword[:min(10, len(hashedPassword))])
	}
}

// verifySSHA512 checks if the provided password matches the SSHA512 hashed password
func verifySSHA512(hashedPassword, password string) error {
	decoded, err := decodePasswordData(hashedPassword, ssha512PrefixB64, ssha512PrefixB64Explicit, ssha512PrefixHex)
	if err != nil {
		return fmt.Errorf("invalid SSHA512 format/data: %w", err)
	}

	// The SHA512 hash is 64 bytes, everything after is the salt
	if len(decoded) < sha512HashLength+ssha512MinSaltLength {
		return errors.New("invalid SSHA512 hash: too short")
	}

	// Extract the hash and salt
	storedHash := decoded[:sha512HashLength]
	salt := decoded[sha512HashLength:]

	// Calculate hash for the provided password with the same salt
	h := sha512.New()
	h.Write([]byte(password))
	h.Write(salt)
	calculatedHash := h.Sum(nil)

	// Compare the hashes (constant-time to prevent timing attacks)
	if subtle.ConstantTimeCompare(storedHash, calculatedHash) != 1 {
		return errors.New("invalid password")
	}

	return nil
}

// verifySHA512 checks if the provided password matches the SHA512 hashed password (without salt)
func verifySHA512(hashedPassword, password string) error {
	storedHash, err := decodePasswordData(hashedPassword, sha512PrefixB64, sha512PrefixB64Explicit, sha512PrefixHex)
	if err != nil {
		return fmt.Errorf("invalid SHA512 format/data: %w", err)
	}

	// SHA512 hash should be exactly 64 bytes
	if len(storedHash) != sha512HashLength {
		return errors.New("invalid SHA512 hash: incorrect length")
	}

	// Calculate hash for the provided password
	h := sha512.New()
	h.Write([]byte(password))
	calculatedHash := h.Sum(nil)

	// Compare the hashes (constant-time to prevent timing attacks)
	if subtle.ConstantTimeCompare(storedHash, calculatedHash) != 1 {
		return errors.New("invalid password")
	}

	return nil
}

// verifyBcrypt checks if the provided password matches the bcrypt hashed password
func verifyBcrypt(hashedPassword, password string) error {
	// For {BLF-CRYPT}, remove the prefix unconditionally
	hashedPassword = strings.TrimPrefix(hashedPassword, blfCryptPrefix)
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}

// decodePasswordData handles prefix checking and decoding for common hash formats.
// It returns the raw decoded data.
func decodePasswordData(hashedPassword, pB64, pB64Explicit, pHex string) (data []byte, err error) {
	var encodedData string
	var isHexEncoded bool

	switch {
	case strings.HasPrefix(hashedPassword, pB64Explicit): // Check more specific explicit b64 first
		encodedData = hashedPassword[len(pB64Explicit):]
	case strings.HasPrefix(hashedPassword, pB64):
		encodedData = hashedPassword[len(pB64):]
	case strings.HasPrefix(hashedPassword, pHex):
		encodedData = hashedPassword[len(pHex):]
		isHexEncoded = true
	default:
		return nil, fmt.Errorf("invalid or missing prefix (expected one of %s, %s, %s)", pB64, pB64Explicit, pHex)
	}

	if isHexEncoded {
		data, err = hex.DecodeString(encodedData)
		if err != nil {
			return nil, fmt.Errorf("error decoding hex data: %w", err)
		}
	} else {
		data, err = base64.StdEncoding.DecodeString(encodedData)
		if err != nil {
			return nil, fmt.Errorf("error decoding base64 data: %w", err)
		}
	}
	return data, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
