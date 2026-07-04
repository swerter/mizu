package validation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	// MXLookupTimeout is the maximum time to wait for MX record lookup
	MXLookupTimeout = 5 * time.Second
)

// CheckMXRecord verifies that the domain can receive email.
// Per RFC 5321, this checks for MX records, and if none exist, falls back to A/AAAA records.
// This validates that the sender's domain can receive bounce messages and replies.
//
// Validation is performed in three layers:
// 1. Blacklist: Reject known invalid/test domains (localhost, example.com, etc.)
// 2. Public Suffix List: Reject domains with invalid TLDs (.local, .internal, bare TLDs)
// 3. DNS: Check for MX records, fall back to A/AAAA records
//
// Returns true if the domain can receive mail (has MX or A/AAAA records), false otherwise.
func CheckMXRecord(ctx context.Context, domain string, resolver *net.Resolver, timeout time.Duration) (bool, error) {
	// Normalize domain: remove any angle brackets and trim whitespace
	domain = strings.Trim(domain, "<>")
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain) // DNS is case-insensitive

	if domain == "" {
		return false, fmt.Errorf("empty domain")
	}

	// Reject common invalid/test domains that should never be used for real email
	// These domains are reserved, local-only, or used for testing per RFC 2606
	invalidDomains := []string{
		"localhost",
		"localhost.localdomain",
		"example.com", // RFC 2606 - reserved for examples
		"example.org", // RFC 2606 - reserved for examples
		"example.net", // RFC 2606 - reserved for examples
		"test.com",    // Common test domain
		"test",        // Invalid TLD
		"invalid",     // RFC 2606 - reserved for invalid domains
	}
	for _, invalid := range invalidDomains {
		if domain == invalid {
			// Return false (no MX records) rather than an error
			// This allows the caller to handle it as "no MX records found"
			return false, nil
		}
	}

	// Validate domain structure using Public Suffix List
	// This catches:
	// - Bare TLDs (e.g., "com", "org", "test")
	// - Domains with invalid TLDs (e.g., "foo.internal", "foo.local")
	// - Malformed domains
	//
	// IMPORTANT: This is a best-effort check. The embedded PSL may be outdated,
	// so we only reject obviously invalid TLDs that should NEVER be used for
	// internet email (.local, .internal, .localhost, .invalid, .test, .onion)
	publicSuffix, icann := publicsuffix.PublicSuffix(domain)

	// Only reject single-label bare TLDs (e.g., "com", "org", "test")
	// Do NOT reject second-level registry domains (e.g., "nic.in", "co.uk", "gov.uk")
	// which ARE valid organizational domains that can send email.
	if publicSuffix == domain && !strings.Contains(domain, ".") {
		// Domain is a bare TLD without any dots (e.g., "com", "test")
		// Can't send mail from a bare TLD - this is always invalid
		return false, nil
	}

	// For non-ICANN TLDs, only reject known-invalid ones
	// This prevents false positives if PSL is outdated
	if !icann {
		// Check if it's a known private/invalid TLD that should never be used
		knownInvalidTLDs := []string{
			"local",     // RFC 6762 (mDNS/Bonjour)
			"localhost", // RFC 6761
			"internal",  // Common corporate use
			"invalid",   // RFC 6761
			"test",      // RFC 6761
			"example",   // RFC 6761
			"onion",     // RFC 7686 (Tor)
		}
		for _, invalidTLD := range knownInvalidTLDs {
			if publicSuffix == invalidTLD {
				// Definitively invalid - safe to reject
				return false, nil
			}
		}
		// Unknown non-ICANN TLD - might be a new TLD we don't know about yet
		// Log for visibility but don't reject
		// The MX/A lookup will determine if it's real
	}

	// Use default resolver if none provided
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	// Use default timeout if not specified
	if timeout == 0 {
		timeout = MXLookupTimeout
	}

	// Create context with timeout
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform MX lookup
	mxRecords, err := resolver.LookupMX(lookupCtx, domain)
	if err != nil {
		// Check if it's a timeout
		if lookupCtx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("MX lookup timeout for domain %s: %w", domain, err)
		}

		// DNS errors mean no MX records found
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			if dnsErr.IsNotFound {
				return false, nil // No MX records, but not an error
			}
			// Log other DNS errors for debugging
			return false, fmt.Errorf("MX lookup DNS error for domain %s (IsTemporary=%v, IsTimeout=%v): %w",
				domain, dnsErr.IsTemporary, dnsErr.IsTimeout, err)
		}

		return false, fmt.Errorf("MX lookup failed for domain %s: %w", domain, err)
	}

	// Check if we got any MX records
	if len(mxRecords) > 0 {
		// RFC 7505: a single MX record of "0 ." (empty/root target) is a "null
		// MX" by which the domain explicitly declares it accepts no mail. Such a
		// sender could never receive bounces or replies, so treat it as invalid.
		if len(mxRecords) == 1 && strings.TrimSuffix(mxRecords[0].Host, ".") == "" {
			return false, nil
		}
		return true, nil // Domain has usable MX records
	}

	// No MX records found - fall back to A/AAAA records per RFC 5321
	// If the domain has A or AAAA records, it can receive mail at those addresses
	addrs, err := resolver.LookupHost(lookupCtx, domain)
	if err != nil {
		// No A/AAAA records either
		return false, nil
	}

	if len(addrs) > 0 {
		// Domain has A/AAAA records, can receive mail
		return true, nil
	}

	// No MX and no A/AAAA records
	return false, nil
}
