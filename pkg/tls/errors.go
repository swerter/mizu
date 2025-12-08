package tls

import "errors"

// ErrMissingServerName is returned when a TLS handshake is attempted without SNI
var ErrMissingServerName = errors.New("missing server name")

// ErrHostNotAllowed is returned when a TLS handshake is attempted for a domain not in the allowlist
var ErrHostNotAllowed = errors.New("host not allowed")

// ErrCertificateUnavailable is returned when a certificate cannot be retrieved (cache miss + ACME failure)
// This is often a transient error (S3 down, ACME rate limit, network issues) and should not crash the server
var ErrCertificateUnavailable = errors.New("certificate unavailable")
