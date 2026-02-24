package tls

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// hashKey creates a deterministic hash of the certificate key for storage.
// This prevents issues with special characters in certificate domain names.
func hashKey(key string) string {
	// Normalize the key
	key = strings.ToLower(strings.TrimSpace(key))

	// Hash the key for safe storage
	h := sha256.New()
	h.Write([]byte(key))
	hash := hex.EncodeToString(h.Sum(nil))

	// Return hash with a readable prefix for debugging
	return fmt.Sprintf("cert-%s", hash)
}

// formatSupportedVersions converts TLS version uint16 codes to human-readable strings.
// Uses raw constants because this package is named "tls" which shadows crypto/tls.
func formatSupportedVersions(versions []uint16) []string {
	names := make([]string, 0, len(versions))
	for _, v := range versions {
		switch v {
		case 0x0301: // tls.VersionTLS10
			names = append(names, "TLS1.0")
		case 0x0302: // tls.VersionTLS11
			names = append(names, "TLS1.1")
		case 0x0303: // tls.VersionTLS12
			names = append(names, "TLS1.2")
		case 0x0304: // tls.VersionTLS13
			names = append(names, "TLS1.3")
		default:
			names = append(names, fmt.Sprintf("0x%04x", v))
		}
	}
	return names
}
