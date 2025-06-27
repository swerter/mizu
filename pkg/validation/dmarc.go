package validation

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"strings"

	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
)

// DMARCResult represents the result of DMARC validation
// DMARC (Domain-based Message Authentication, Reporting & Conformance) helps prevent email spoofing
type DMARCResult struct {
	Pass           bool     // Whether DMARC validation passed (SPF or DKIM aligned)
	Policy         string   // Domain's DMARC policy: none, quarantine, or reject
	SPFAligned     bool     // Whether SPF passed AND domain aligned with From header
	DKIMAligned    bool     // Whether DKIM passed AND domain aligned with From header
	FailureReasons []string // List of reasons why validation failed
	NoDMARCRecord  bool     // Whether domain has no DMARC record
	ShouldBeJunk   bool     // Whether message should be marked as junk (no DMARC + SPF/DKIM failure)
}

// CheckDMARC performs DMARC validation on an email
// It validates DKIM signatures and checks DMARC policy compliance
// Note: SPF validation needs to be done separately at SMTP time
func CheckDMARC(ctx context.Context, rawEmail string, remoteIP net.IP, heloHost string) (*DMARCResult, error) {
	// Parse the email message to extract headers
	msg, err := mail.ReadMessage(strings.NewReader(rawEmail))
	if err != nil {
		return nil, fmt.Errorf("failed to parse email: %w", err)
	}

	// Extract the From header - this is what recipients see as the sender
	fromHeader := msg.Header.Get("From")
	if fromHeader == "" {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"missing From header"},
		}, nil
	}

	// Parse From address to get domain
	fromAddrs, err := mail.ParseAddressList(fromHeader)
	if err != nil || len(fromAddrs) == 0 {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"invalid From header format"},
		}, nil
	}

	fromAddr := fromAddrs[0].Address
	parts := strings.Split(fromAddr, "@")
	if len(parts) != 2 {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"invalid From address format"},
		}, nil
	}
	fromDomain := parts[1]

	// Create result structure
	result := &DMARCResult{
		FailureReasons: make([]string, 0),
		Policy:         "none",
	}

	// Look up DMARC policy via DNS TXT record (_dmarc.domain.com)
	record, err := dmarc.Lookup(fromDomain)
	noDMARCRecord := false
	if err != nil {
		log.Printf("DMARC lookup failed for %s: %v", fromDomain, err)
		// No DMARC record - continue processing to check SPF/DKIM
		noDMARCRecord = true
		result.NoDMARCRecord = true
		// Don't return early - we need to check SPF/DKIM to determine if it should be junk
	} else {
		// Set the DMARC policy
		result.Policy = string(record.Policy)
	}

	// Verify DKIM signatures
	reader := strings.NewReader(rawEmail)
	verifications, err := dkim.Verify(reader)
	if err != nil && err != io.EOF {
		log.Printf("DKIM verification error: %v", err)
		result.FailureReasons = append(result.FailureReasons, fmt.Sprintf("DKIM verification error: %v", err))
	}

	// Check DKIM results and alignment
	for _, v := range verifications {
		if v.Err == nil {
			// DKIM signature is valid, check domain alignment
			signingDomain := v.Domain

			// Check DKIM alignment based on DMARC alignment mode
			aligned := false
			if !noDMARCRecord && record.DKIMAlignment == dmarc.AlignmentStrict {
				// Strict alignment: exact domain match
				aligned = strings.EqualFold(signingDomain, fromDomain)
			} else {
				// Relaxed alignment (default): organizational domain match
				aligned = isAligned(fromDomain, signingDomain, false)
			}

			if aligned {
				result.DKIMAligned = true
				log.Printf("DKIM passed and aligned for domain %s (signing domain: %s)", fromDomain, signingDomain)
				break
			} else {
				log.Printf("DKIM passed but not aligned for domain %s (signing domain: %s)", fromDomain, signingDomain)
			}
		} else {
			log.Printf("DKIM verification failed: %v", v.Err)
			result.FailureReasons = append(result.FailureReasons, fmt.Sprintf("DKIM failed: %v", v.Err))
		}
	}

	if len(verifications) == 0 {
		log.Printf("No DKIM signatures found for domain %s", fromDomain)
		result.FailureReasons = append(result.FailureReasons, "no DKIM signatures found")
	}

	// For SPF alignment, we would need the envelope sender (Return-Path)
	// and the SPF result from the SMTP session
	// Since we're checking DMARC after receiving the email, we can't do real-time SPF validation
	// The SMTP server should have already done SPF checking
	// For now, we'll check if there's an Authentication-Results header with SPF info
	authResultsHeader := msg.Header.Get("Authentication-Results")
	if authResultsHeader != "" {
		_, results, err := authres.Parse(authResultsHeader)
		if err == nil {
			for _, r := range results {
				// Type assert to SPFResult
				if spfResult, ok := r.(*authres.SPFResult); ok {
					if spfResult.Value == authres.ResultPass {
						// Check if SPF domain aligns with From domain
						spfDomain := ""
						if spfResult.From != "" {
							spfDomain = extractDomain(spfResult.From)
						}

						if spfDomain != "" {
							if !noDMARCRecord && record.SPFAlignment == dmarc.AlignmentStrict {
								result.SPFAligned = strings.EqualFold(spfDomain, fromDomain)
							} else {
								result.SPFAligned = isAligned(fromDomain, spfDomain, false)
							}
							if result.SPFAligned {
								log.Printf("SPF passed and aligned for domain %s (envelope domain: %s)", fromDomain, spfDomain)
							}
						}
					}
				}
			}
		} else {
			log.Printf("Failed to parse Authentication-Results header: %v", err)
		}
	}

	// DMARC passes if EITHER SPF or DKIM is aligned (not both required)
	result.Pass = result.SPFAligned || result.DKIMAligned

	// Special handling for no DMARC record case
	if noDMARCRecord {
		// If no DMARC record exists:
		// - If SPF or DKIM is aligned, message passes (result.Pass is already set correctly)
		// - If neither SPF nor DKIM is aligned, mark as junk
		if !result.SPFAligned && !result.DKIMAligned {
			result.ShouldBeJunk = true
			result.FailureReasons = append(result.FailureReasons,
				"No DMARC record and neither SPF nor DKIM aligned - marking as junk")
			log.Printf("Marking as junk: No DMARC record for %s and authentication failed", fromDomain)
		} else {
			// Even with no DMARC, if SPF or DKIM passes, we let it through
			result.Pass = true
		}
	} else {
		// Add specific failure reason for reject policy
		if !result.Pass && result.Policy == "reject" {
			result.FailureReasons = append(result.FailureReasons,
				"DMARC policy is 'reject' and neither SPF nor DKIM aligned")
		}
	}

	log.Printf("DMARC result for %s: Policy=%s, SPF Aligned=%v, DKIM Aligned=%v, Pass=%v, NoDMARC=%v, ShouldBeJunk=%v",
		fromDomain, result.Policy, result.SPFAligned, result.DKIMAligned, result.Pass, result.NoDMARCRecord, result.ShouldBeJunk)

	return result, nil
}

// isAligned checks if two domains are aligned according to DMARC rules
// Alignment ensures the authenticated domain matches the visible From domain
func isAligned(fromDomain, authDomain string, strict bool) bool {
	// Normalize domains for comparison
	fromDomain = strings.ToLower(strings.TrimSpace(fromDomain))
	authDomain = strings.ToLower(strings.TrimSpace(authDomain))

	if strict {
		// Strict alignment requires exact domain match
		return fromDomain == authDomain
	}

	// Relaxed alignment allows subdomains to align with parent domain
	// e.g., mail.example.com aligns with example.com
	fromOrg := getOrganizationalDomain(fromDomain)
	authOrg := getOrganizationalDomain(authDomain)
	return fromOrg == authOrg
}

// getOrganizationalDomain returns the organizational domain
// This is a simplified version - production systems should use the Public Suffix List
// to properly handle domains like .co.uk, .com.au, etc.
func getOrganizationalDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		// Return last two parts (e.g., "example.com" from "mail.example.com")
		// NOTE: This doesn't handle multi-level TLDs like .co.uk correctly
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return domain
}

// extractDomain extracts domain from an email address
func extractDomain(email string) string {
	email = strings.TrimSpace(email)
	email = strings.Trim(email, "<>")
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
