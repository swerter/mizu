package validation

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message/textproto"
	"github.com/emersion/go-msgauth/dkim"
)

// TestDebugARCVerification debugs why ARC verification is failing
func TestDebugARCVerification(t *testing.T) {
	testFile := filepath.Join("..", "..", "tests", "arc-example.eml")
	rawEmailBytes, err := os.ReadFile(testFile)
	if err != nil {
		t.Skipf("Skipping test: could not read test file %s: %v", testFile, err)
		return
	}
	rawEmail := string(rawEmailBytes)

	// Parse headers
	h, err := textproto.ReadHeader(bufio.NewReader(strings.NewReader(rawEmail)))
	if err != nil {
		t.Fatalf("Failed to parse headers: %v", err)
	}

	// Extract ARC-Message-Signature
	arcMSValues := h.Values("ARC-Message-Signature")
	t.Logf("Found %d ARC-Message-Signature headers", len(arcMSValues))

	if len(arcMSValues) == 0 {
		t.Fatal("No ARC-Message-Signature found")
	}

	arcMS := arcMSValues[0]
	t.Logf("\nOriginal ARC-Message-Signature:\n%s", arcMS)

	// Extract domain and selector
	domain := extractDomain(arcMS)
	selector := extractSelector(arcMS)
	t.Logf("\nDomain: %s", domain)
	t.Logf("Selector: %s", selector)

	// Try to lookup the DNS record
	t.Logf("\nAttempting DNS lookup for: %s._domainkey.%s", selector, domain)
	records, err := lookupTXTWithTimeout(selector + "._domainkey." + domain)
	if err != nil {
		t.Logf("DNS lookup failed: %v", err)
	} else {
		t.Logf("DNS records found: %d", len(records))
		for i, rec := range records {
			t.Logf("  Record %d: %s", i+1, rec)
		}
	}

	// Convert to DKIM format
	dkimSig := convertARCMSToDKIMSignature(arcMS)
	t.Logf("\nConverted to DKIM-Signature:\n%s", dkimSig)

	// Reconstruct message without ARC headers at instance >= 1
	reconstructed := removeARCHeadersFromInstance(rawEmail, 1)
	t.Logf("\nReconstructed message length: %d (original: %d)", len(reconstructed), len(rawEmail))

	// Show first few lines of reconstructed message
	lines := strings.Split(reconstructed, "\n")
	t.Logf("\nFirst 20 header lines of reconstructed message:")
	for i := 0; i < 20 && i < len(lines); i++ {
		t.Logf("  %s", lines[i])
	}

	// Try DKIM verification
	messageWithSig := "DKIM-Signature: " + dkimSig + "\r\n" + reconstructed
	reader := strings.NewReader(messageWithSig)
	verifyOpts := &dkim.VerifyOptions{
		LookupTXT: lookupTXTWithTimeout,
	}

	t.Logf("\nAttempting DKIM verification...")
	verifications, err := dkim.VerifyWithOptions(reader, verifyOpts)
	if err != nil && err != io.EOF {
		t.Logf("DKIM verification error: %v", err)
	}

	t.Logf("\nDKIM verification results: %d verifications", len(verifications))
	for i, v := range verifications {
		t.Logf("\nVerification %d:", i+1)
		t.Logf("  Domain: %s", v.Domain)
		t.Logf("  Error: %v", v.Err)
		if v.Err != nil {
			t.Logf("  Error details: %v", v.Err)
		}
	}
}

// extractSelector extracts the selector (s=) from an ARC header
func extractSelector(header string) string {
	parts := strings.Split(header, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "s=") {
			selector := strings.TrimPrefix(part, "s=")
			return strings.TrimSpace(selector)
		}
	}
	return ""
}

// TestCompareOriginalVsReconstructed compares the original message with reconstructed
func TestCompareOriginalVsReconstructed(t *testing.T) {
	testFile := filepath.Join("..", "..", "tests", "arc-example.eml")
	rawEmailBytes, err := os.ReadFile(testFile)
	if err != nil {
		t.Skipf("Skipping test: could not read test file %s: %v", testFile, err)
		return
	}
	rawEmail := string(rawEmailBytes)

	// Reconstruct without ARC headers
	reconstructed := removeARCHeadersFromInstance(rawEmail, 1)

	// Parse both and compare
	origH, _ := textproto.ReadHeader(bufio.NewReader(strings.NewReader(rawEmail)))
	reconH, _ := textproto.ReadHeader(bufio.NewReader(strings.NewReader(reconstructed)))

	t.Logf("Original headers count: ~%d", origH.Len())
	t.Logf("Reconstructed headers count: ~%d", reconH.Len())

	// Check which headers are in original but not in reconstructed
	arcHeaders := []string{"ARC-Seal", "ARC-Message-Signature", "ARC-Authentication-Results"}
	for _, arcType := range arcHeaders {
		origCount := len(origH.Values(arcType))
		reconCount := len(reconH.Values(arcType))
		t.Logf("%s: original=%d, reconstructed=%d", arcType, origCount, reconCount)
	}

	// Check some important headers are preserved
	importantHeaders := []string{"From", "To", "Subject", "Date", "Message-ID", "DKIM-Signature"}
	for _, hdr := range importantHeaders {
		origVal := origH.Get(hdr)
		reconVal := reconH.Get(hdr)
		if origVal != reconVal {
			t.Logf("Header %s changed:", hdr)
			t.Logf("  Original: %s", origVal)
			t.Logf("  Reconstructed: %s", reconVal)
		}
	}
}

// TestDKIMVerificationOnOriginal tests if DKIM verification works on the original email
func TestDKIMVerificationOnOriginal(t *testing.T) {
	testFile := filepath.Join("..", "..", "tests", "arc-example.eml")
	rawEmailBytes, err := os.ReadFile(testFile)
	if err != nil {
		t.Skipf("Skipping test: could not read test file %s: %v", testFile, err)
		return
	}
	rawEmail := string(rawEmailBytes)

	t.Logf("Testing DKIM verification on original email...")

	reader := strings.NewReader(rawEmail)
	verifyOpts := &dkim.VerifyOptions{
		LookupTXT: lookupTXTWithTimeout,
	}

	verifications, err := dkim.VerifyWithOptions(reader, verifyOpts)
	if err != nil && err != io.EOF {
		t.Logf("DKIM verification error: %v", err)
	}

	t.Logf("Found %d DKIM verifications", len(verifications))
	for i, v := range verifications {
		t.Logf("\nDKIM Verification %d:", i+1)
		t.Logf("  Domain: %s", v.Domain)
		t.Logf("  Valid: %v", v.Err == nil)
		if v.Err != nil {
			t.Logf("  Error: %v", v.Err)
		}
	}

	// If original DKIM passes, then we know DNS is working and the email is intact
	hasValidDKIM := false
	for _, v := range verifications {
		if v.Err == nil {
			hasValidDKIM = true
			t.Logf("\n✓ Original email has valid DKIM signature from %s", v.Domain)
			break
		}
	}

	if !hasValidDKIM {
		t.Logf("\n✗ No valid DKIM signatures found in original email")
		t.Logf("This suggests either:")
		t.Logf("  1. Email was modified when saved")
		t.Logf("  2. DNS lookups are failing")
		t.Logf("  3. Signatures have expired")
	}
}
