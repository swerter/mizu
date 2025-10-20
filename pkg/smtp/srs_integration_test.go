package smtp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"migadu/mizu/pkg/srs"
	"migadu/mizu/pkg/stats"

	"github.com/emersion/go-smtp"
)

// TestSRS_IncomingDecoding verifies that incoming SRS addresses are decoded
// before being passed to routing and the HTTP backend
func TestSRS_IncomingDecoding(t *testing.T) {
	// Create SRS rewriter
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Encode an address (simulating an outbound forward)
	originalAddress := "alice@example.com"
	srsAddress, err := rewriter.Encode(originalAddress)
	if err != nil {
		t.Fatalf("Failed to encode address: %v", err)
	}

	t.Logf("Original address: %s", originalAddress)
	t.Logf("SRS address: %s", srsAddress)

	// Create a session to test RCPT TO handling
	cfg := testConfig()

	statsManager := stats.NewManager(false, 0, "test", false, 0, nil, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer statsManager.Stop()

	session := &Session{
		conn:         (*smtp.Conn)(nil), // Nil conn - we'll bypass timeout checks
		ctx:          context.Background(),
		helo:         "test.example.com",
		from:         "bounce@destination.com", // Simulating a bounce
		to:           make([]string, 0),
		serverConfig: &cfg.Servers[0],
		globalConfig: cfg,
		statsManager: statsManager,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		remoteAddr:   "192.0.2.1:12345",
		commandState: stateMail, // After MAIL FROM
		srsRewriter:  rewriter,  // Add SRS rewriter
	}

	// Test RCPT TO with SRS address
	err = session.Rcpt(srsAddress, &smtp.RcptOptions{})
	if err != nil {
		t.Fatalf("RCPT TO failed with SRS address: %v", err)
	}

	// Verify that the address was decoded
	if len(session.to) != 1 {
		t.Fatalf("Expected 1 recipient, got %d", len(session.to))
	}

	if session.to[0] != originalAddress {
		t.Errorf("Expected decoded address %s, got %s", originalAddress, session.to[0])
	}

	t.Logf("✓ SRS address decoded correctly: %s → %s", srsAddress, session.to[0])
}

// TestSRS_InvalidSRSAddress verifies that invalid SRS addresses are rejected
func TestSRS_InvalidSRSAddress(t *testing.T) {
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	cfg := testConfig()

	statsManager := stats.NewManager(false, 0, "test", false, 0, nil, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer statsManager.Stop()

	session := &Session{
		ctx:          context.Background(),
		helo:         "test.example.com",
		from:         "sender@example.com",
		to:           make([]string, 0),
		serverConfig: &cfg.Servers[0],
		globalConfig: cfg,
		statsManager: statsManager,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		remoteAddr:   "192.0.2.1:12345",
		commandState: stateMail,
		srsRewriter:  rewriter,
	}

	// Try with an invalid SRS address (wrong hash)
	invalidSRS := "SRS0=FAKE=5Z=example.com=alice@relay.mizu.com"

	err := session.Rcpt(invalidSRS, &smtp.RcptOptions{})
	if err == nil {
		t.Fatal("Expected error for invalid SRS address")
	}

	smtpErr, ok := err.(*smtp.SMTPError)
	if !ok {
		t.Fatalf("Expected *smtp.SMTPError, got %T: %v", err, err)
	}

	if smtpErr.Code != 550 {
		t.Errorf("Expected SMTP code 550, got %d", smtpErr.Code)
	}

	if smtpErr.Message != "invalid SRS address" {
		t.Errorf("Expected 'invalid SRS address' message, got: %s", smtpErr.Message)
	}

	t.Logf("✓ Invalid SRS address rejected: %v", err)
}

// TestSRS_NonSRSAddress verifies that non-SRS addresses are not modified
func TestSRS_NonSRSAddress(t *testing.T) {
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	cfg := testConfig()

	statsManager := stats.NewManager(false, 0, "test", false, 0, nil, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer statsManager.Stop()

	session := &Session{
		ctx:          context.Background(),
		helo:         "test.example.com",
		from:         "sender@example.com",
		to:           make([]string, 0),
		serverConfig: &cfg.Servers[0],
		globalConfig: cfg,
		statsManager: statsManager,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		remoteAddr:   "192.0.2.1:12345",
		commandState: stateMail,
		srsRewriter:  rewriter,
	}

	// Test with normal address (not SRS)
	normalAddress := "user@example.com"

	err := session.Rcpt(normalAddress, &smtp.RcptOptions{})
	if err != nil {
		t.Fatalf("RCPT TO failed with normal address: %v", err)
	}

	// Verify that the address was NOT decoded (remains the same)
	if len(session.to) != 1 {
		t.Fatalf("Expected 1 recipient, got %d", len(session.to))
	}

	if session.to[0] != normalAddress {
		t.Errorf("Expected address unchanged %s, got %s", normalAddress, session.to[0])
	}

	t.Logf("✓ Normal address passed through unchanged: %s", session.to[0])
}

// TestSRS_WithoutRewriter verifies that sessions without SRS rewriter work normally
func TestSRS_WithoutRewriter(t *testing.T) {
	cfg := testConfig()

	statsManager := stats.NewManager(false, 0, "test", false, 0, nil, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer statsManager.Stop()

	session := &Session{
		ctx:          context.Background(),
		helo:         "test.example.com",
		from:         "sender@example.com",
		to:           make([]string, 0),
		serverConfig: &cfg.Servers[0],
		globalConfig: cfg,
		statsManager: statsManager,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		remoteAddr:   "192.0.2.1:12345",
		commandState: stateMail,
		srsRewriter:  nil, // No SRS rewriter
	}

	// Test with SRS-looking address but no rewriter
	srsLikeAddress := "SRS0=abcd=5Z=example.com=alice@relay.mizu.com"

	err := session.Rcpt(srsLikeAddress, &smtp.RcptOptions{})
	if err != nil {
		t.Fatalf("RCPT TO failed: %v", err)
	}

	// Address should be accepted as-is (no decoding without rewriter)
	if len(session.to) != 1 {
		t.Fatalf("Expected 1 recipient, got %d", len(session.to))
	}

	if session.to[0] != srsLikeAddress {
		t.Errorf("Expected address %s, got %s", srsLikeAddress, session.to[0])
	}

	t.Logf("✓ SRS address accepted without rewriter (no decoding): %s", session.to[0])
}

// TestSRS_DoubleEncoded verifies that SRS1 addresses are decoded correctly
func TestSRS_DoubleEncoded(t *testing.T) {
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Create SRS0 address
	originalAddress := "alice@example.com"
	srs0, err := rewriter.Encode(originalAddress)
	if err != nil {
		t.Fatalf("Failed to create SRS0: %v", err)
	}

	// Create SRS1 address (re-forwarding)
	srs1, err := rewriter.Encode(srs0)
	if err != nil {
		t.Fatalf("Failed to create SRS1: %v", err)
	}

	t.Logf("Original: %s", originalAddress)
	t.Logf("SRS0:     %s", srs0)
	t.Logf("SRS1:     %s", srs1)

	// Create session
	cfg := testConfig()

	statsManager := stats.NewManager(false, 0, "test", false, 0, nil, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer statsManager.Stop()

	session := &Session{
		ctx:          context.Background(),
		helo:         "test.example.com",
		from:         "bounce@destination.com",
		to:           make([]string, 0),
		serverConfig: &cfg.Servers[0],
		globalConfig: cfg,
		statsManager: statsManager,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		remoteAddr:   "192.0.2.1:12345",
		commandState: stateMail,
		srsRewriter:  rewriter,
	}

	// Test RCPT TO with SRS1 address
	err = session.Rcpt(srs1, &smtp.RcptOptions{})
	if err != nil {
		t.Fatalf("RCPT TO failed with SRS1 address: %v", err)
	}

	// Verify that SRS1 decoded to original address
	if len(session.to) != 1 {
		t.Fatalf("Expected 1 recipient, got %d", len(session.to))
	}

	if session.to[0] != originalAddress {
		t.Errorf("Expected decoded address %s, got %s", originalAddress, session.to[0])
	}

	t.Logf("✓ SRS1 address decoded correctly: %s → %s", srs1, session.to[0])
}
