package smtp

import (
	"fmt"
	"net"
	"strings"
	"time"

	"migadu/mizu/pkg/validation"
)

// InjectMizuHeaders adds Received and X-Mizu-* headers to the email
// These headers provide email tracing, authentication results, and debugging information
func InjectMizuHeaders(rawEmail, domain, remoteAddr, heloHostname, traceID string, tlsVersion string, spfResult *validation.SPFResult, dmarcResult *validation.DMARCResult, arcResult *validation.ARCResult, isJunk bool) string {
	// Build the Received header
	receivedHeader := buildReceivedHeader(domain, remoteAddr, heloHostname, traceID, tlsVersion)

	// Build X-Mizu-* headers
	mizuHeaders := buildMizuHeaders(traceID, spfResult, dmarcResult, arcResult, isJunk)

	// Prepend headers to the email
	// RFC 5322: Headers come before the body, separated by CRLF
	allHeaders := receivedHeader + mizuHeaders
	return allHeaders + rawEmail
}

// buildReceivedHeader creates a standard Received header for email tracing
// Format follows RFC 5321 section 4.4 (Trace Information)
func buildReceivedHeader(domain, remoteAddr, heloHostname, traceID, tlsVersion string) string {
	// Extract IP and port from remoteAddr
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // Fallback if parsing fails
	}

	// Determine protocol (SMTP, ESMTP, ESMTPS, ESMTPSA)
	protocol := "ESMTP"
	if tlsVersion != "none" && tlsVersion != "" {
		protocol = "ESMTPS" // S suffix indicates TLS
	}

	// RFC 5322 date format: "Mon, 02 Jan 2006 15:04:05 -0700"
	timestamp := time.Now().Format(time.RFC1123Z)

	// Build Received header
	// Format: Received: from <client> by <server> with <protocol> id <id>; <timestamp>
	var sb strings.Builder
	sb.WriteString("Received: from ")
	sb.WriteString(heloHostname)
	sb.WriteString(" (")
	sb.WriteString(host)
	sb.WriteString(")\r\n")
	sb.WriteString("\tby ")
	sb.WriteString(domain)
	sb.WriteString(" with ")
	sb.WriteString(protocol)
	sb.WriteString(" id ")
	sb.WriteString(traceID)
	sb.WriteString(";\r\n")
	sb.WriteString("\t")
	sb.WriteString(timestamp)
	sb.WriteString("\r\n")

	return sb.String()
}

// buildMizuHeaders creates custom X-Mizu-* headers for debugging and analysis
func buildMizuHeaders(traceID string, spfResult *validation.SPFResult, dmarcResult *validation.DMARCResult, arcResult *validation.ARCResult, isJunk bool) string {
	var sb strings.Builder

	// X-Mizu-Trace-ID: Unique identifier for this email transaction
	sb.WriteString("X-Mizu-Trace-ID: ")
	sb.WriteString(traceID)
	sb.WriteString("\r\n")

	// X-Mizu-Authenticated: Summary of authentication results
	sb.WriteString("X-Mizu-Authenticated: ")
	sb.WriteString(buildAuthenticationSummary(spfResult, dmarcResult, arcResult))
	sb.WriteString("\r\n")

	// X-Mizu-Junk: Spam classification
	if isJunk {
		sb.WriteString("X-Mizu-Junk: YES\r\n")
	} else {
		sb.WriteString("X-Mizu-Junk: NO\r\n")
	}

	return sb.String()
}

// buildAuthenticationSummary creates a summary string of authentication results
// Format: "spf=pass dkim=pass dmarc=pass arc=pass"
func buildAuthenticationSummary(spfResult *validation.SPFResult, dmarcResult *validation.DMARCResult, arcResult *validation.ARCResult) string {
	var parts []string

	// SPF result
	if spfResult != nil {
		parts = append(parts, fmt.Sprintf("spf=%s", spfResult.Result.Value))
	} else {
		parts = append(parts, "spf=none")
	}

	// DKIM result (from DMARC alignment check)
	if dmarcResult != nil {
		if dmarcResult.DKIMAligned {
			parts = append(parts, "dkim=pass")
		} else {
			parts = append(parts, "dkim=fail")
		}
	} else {
		parts = append(parts, "dkim=none")
	}

	// DMARC result
	if dmarcResult != nil {
		if dmarcResult.Pass {
			parts = append(parts, "dmarc=pass")
		} else {
			parts = append(parts, "dmarc=fail")
		}
	} else {
		parts = append(parts, "dmarc=none")
	}

	// ARC result
	if arcResult != nil {
		if arcResult.Pass {
			parts = append(parts, "arc=pass")
		} else {
			parts = append(parts, "arc=fail")
		}
	} else {
		parts = append(parts, "arc=none")
	}

	return strings.Join(parts, " ")
}

// addJunkHeader adds a custom header to mark the message as junk/spam
func addJunkHeader(rawEmail, headerName string) string {
	header := fmt.Sprintf("%s: YES\r\n", headerName)
	return header + rawEmail
}

// modifySubject modifies the Subject header according to the provided pattern
// The pattern should contain %s which will be replaced with the original subject
func modifySubject(rawEmail, pattern string) string {
	lines := strings.Split(rawEmail, "\r\n")
	var result []string
	subjectModified := false

	for i, line := range lines {
		// Look for Subject header (case-insensitive)
		if !subjectModified && len(line) >= 8 && strings.EqualFold(line[:8], "Subject:") {
			// Extract original subject (everything after "Subject: ")
			originalSubject := strings.TrimSpace(line[8:])

			// Handle multi-line subjects (RFC 5322 folding)
			// Check if next lines are continuations (start with whitespace)
			for i+1 < len(lines) && len(lines[i+1]) > 0 && (lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				originalSubject += " " + strings.TrimSpace(lines[i])
			}

			// Apply pattern
			newSubject := fmt.Sprintf(pattern, originalSubject)
			result = append(result, "Subject: "+newSubject)
			subjectModified = true
		} else {
			result = append(result, line)
		}
	}

	// If no subject was found, add one
	if !subjectModified {
		// Find the end of headers (empty line)
		for i, line := range result {
			if line == "" {
				// Insert subject before the empty line
				newSubject := fmt.Sprintf(pattern, "(no subject)")
				result = append(result[:i], append([]string{"Subject: " + newSubject}, result[i:]...)...)
				break
			}
		}
	}

	return strings.Join(result, "\r\n")
}
