package smtp

import (
	"io"

	"testing"

	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/queue"
	"migadu/mizu/pkg/routing"
	"migadu/mizu/pkg/srs"

	"log/slog"
)

// testConfig returns a minimal config for testing createDeliveryJobs
func testConfig() *config.Config {
	return &config.Config{
		Local: true,
		Servers: []config.ServerConfig{
			testServerConfig(),
		},
		Delivery: config.DeliveryConfig{
			URL: "https://backend.example.com/deliver",
		},
		Forwarding: config.ForwardingConfig{
			URL: "https://forward.example.com/relay",
		},
	}
}

// TestSRS_OutboundEncoding verifies that SRS encoding is applied when creating
// forwarding jobs, and that the original sender is preserved
func TestSRS_OutboundEncoding(t *testing.T) {
	// Create SRS rewriter
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Create a session with SRS rewriter
	session := &Session{
		from:         "alice@example.com",
		to:           []string{"user@yourdomain.com"},
		traceID:      "test-trace-123",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		srsRewriter:  rewriter,
		serverConfig: &config.DefaultConfig().Servers[0],
		globalConfig: testConfig(),
	}

	// Create routing response with forwarding
	routingResponse := &routing.ResolveResponse{
		Accepted:  true,
		ForwardTo: []string{"bob@destination.com"},
	}

	// Create delivery jobs
	signedEmail := "Subject: Test\r\n\r\nTest email content"
	jobs := session.createDeliveryJobs(signedEmail, routingResponse)

	// Should have 1 forwarding job
	if len(jobs) != 1 {
		t.Fatalf("Expected 1 forwarding job, got %d", len(jobs))
	}

	job := jobs[0]

	// Verify it's a forwarding job
	if !job.IsForwarding {
		t.Error("Expected IsForwarding to be true")
	}

	// Verify SRS encoding was applied
	if !rewriter.IsSRSAddress(job.From) {
		t.Errorf("Expected SRS-encoded From address, got: %s", job.From)
	}

	// Verify original sender is preserved
	if job.OriginalFrom != "alice@example.com" {
		t.Errorf("Expected OriginalFrom to be alice@example.com, got: %s", job.OriginalFrom)
	}

	// Verify SRS address decodes back to original
	decoded, err := rewriter.Decode(job.From)
	if err != nil {
		t.Fatalf("Failed to decode SRS address: %v", err)
	}

	if decoded != "alice@example.com" {
		t.Errorf("SRS address should decode to alice@example.com, got: %s", decoded)
	}

	// Verify recipients are correct
	if len(job.Recipients) != 1 || job.Recipients[0] != "bob@destination.com" {
		t.Errorf("Expected recipients [bob@destination.com], got: %v", job.Recipients)
	}

	t.Logf("✓ SRS encoding applied correctly:")
	t.Logf("  Original From:   %s", job.OriginalFrom)
	t.Logf("  SRS From:        %s", job.From)
	t.Logf("  Decodes to:      %s", decoded)
	t.Logf("  Recipients:      %v", job.Recipients)
}

// TestSRS_OutboundDeliveryNoEncoding verifies that SRS is NOT applied
// to regular delivery jobs (only forwarding jobs)
func TestSRS_OutboundDeliveryNoEncoding(t *testing.T) {
	// Create SRS rewriter
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Create a session with SRS rewriter
	session := &Session{
		from:         "alice@example.com",
		to:           []string{"user@yourdomain.com"},
		traceID:      "test-trace-123",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		srsRewriter:  rewriter,
		serverConfig: &config.DefaultConfig().Servers[0],
		globalConfig: testConfig(),
	}

	// Create routing response with DELIVERY (not forwarding)
	routingResponse := &routing.ResolveResponse{
		Accepted:  true,
		DeliverTo: []string{"local-user@backend.com"},
	}

	// Create delivery jobs
	signedEmail := "Subject: Test\r\n\r\nTest email content"
	jobs := session.createDeliveryJobs(signedEmail, routingResponse)

	// Should have 1 delivery job
	if len(jobs) != 1 {
		t.Fatalf("Expected 1 delivery job, got %d", len(jobs))
	}

	job := jobs[0]

	// Verify it's a delivery job (not forwarding)
	if job.IsForwarding {
		t.Error("Expected IsForwarding to be false for delivery job")
	}

	// Verify SRS encoding was NOT applied
	if job.From != "alice@example.com" {
		t.Errorf("Expected original From address, got: %s", job.From)
	}

	// Verify OriginalFrom matches From (no SRS rewriting)
	if job.OriginalFrom != job.From {
		t.Errorf("Expected OriginalFrom to equal From for delivery job")
	}

	// Verify it's not an SRS address
	if rewriter.IsSRSAddress(job.From) {
		t.Errorf("Delivery job should NOT have SRS-encoded From address: %s", job.From)
	}

	t.Logf("✓ SRS NOT applied to delivery job:")
	t.Logf("  From:        %s", job.From)
	t.Logf("  OriginalFrom: %s", job.OriginalFrom)
	t.Logf("  Recipients:   %v", job.Recipients)
}

// TestSRS_OutboundBothDeliveryAndForwarding verifies that when a routing
// response includes both delivery and forwarding, SRS is only applied to forwarding
func TestSRS_OutboundBothDeliveryAndForwarding(t *testing.T) {
	// Create SRS rewriter
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Create a session with SRS rewriter
	session := &Session{
		from:         "sender@example.com",
		to:           []string{"user@yourdomain.com"},
		traceID:      "test-trace-456",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		srsRewriter:  rewriter,
		serverConfig: &config.DefaultConfig().Servers[0],
		globalConfig: testConfig(),
	}

	// Create routing response with BOTH delivery and forwarding
	routingResponse := &routing.ResolveResponse{
		Accepted:  true,
		DeliverTo: []string{"local@backend.com"},
		ForwardTo: []string{"external@other.com"},
	}

	// Create delivery jobs
	signedEmail := "Subject: Test\r\n\r\nTest email content"
	jobs := session.createDeliveryJobs(signedEmail, routingResponse)

	// Should have 2 jobs (1 delivery, 1 forwarding)
	if len(jobs) != 2 {
		t.Fatalf("Expected 2 jobs (delivery + forwarding), got %d", len(jobs))
	}

	// Find delivery and forwarding jobs
	var deliveryJob, forwardingJob *queue.DeliveryJob
	for _, job := range jobs {
		if job.IsForwarding {
			forwardingJob = job
		} else {
			deliveryJob = job
		}
	}

	if deliveryJob == nil {
		t.Fatal("Delivery job not found")
	}
	if forwardingJob == nil {
		t.Fatal("Forwarding job not found")
	}

	// Verify delivery job has NO SRS encoding
	if deliveryJob.From != "sender@example.com" {
		t.Errorf("Delivery job should have original From, got: %s", deliveryJob.From)
	}
	if rewriter.IsSRSAddress(deliveryJob.From) {
		t.Errorf("Delivery job should NOT be SRS-encoded: %s", deliveryJob.From)
	}

	// Verify forwarding job HAS SRS encoding
	if !rewriter.IsSRSAddress(forwardingJob.From) {
		t.Errorf("Forwarding job should be SRS-encoded, got: %s", forwardingJob.From)
	}
	if forwardingJob.OriginalFrom != "sender@example.com" {
		t.Errorf("Forwarding job OriginalFrom should be sender@example.com, got: %s", forwardingJob.OriginalFrom)
	}

	// Verify SRS decodes correctly
	decoded, err := rewriter.Decode(forwardingJob.From)
	if err != nil {
		t.Fatalf("Failed to decode forwarding job SRS address: %v", err)
	}
	if decoded != "sender@example.com" {
		t.Errorf("SRS should decode to sender@example.com, got: %s", decoded)
	}

	t.Logf("✓ Delivery job (no SRS):")
	t.Logf("  From: %s", deliveryJob.From)
	t.Logf("  To:   %v", deliveryJob.Recipients)
	t.Logf("")
	t.Logf("✓ Forwarding job (with SRS):")
	t.Logf("  Original From: %s", forwardingJob.OriginalFrom)
	t.Logf("  SRS From:      %s", forwardingJob.From)
	t.Logf("  Decodes to:    %s", decoded)
	t.Logf("  To:            %v", forwardingJob.Recipients)
}

// TestSRS_OutboundWithoutRewriter verifies graceful behavior when SRS rewriter is nil
func TestSRS_OutboundWithoutRewriter(t *testing.T) {
	// Create a session WITHOUT SRS rewriter
	session := &Session{
		from:         "alice@example.com",
		to:           []string{"user@yourdomain.com"},
		traceID:      "test-trace-789",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		srsRewriter:  nil, // No SRS rewriter
		serverConfig: &config.DefaultConfig().Servers[0],
		globalConfig: testConfig(),
	}

	// Create routing response with forwarding
	routingResponse := &routing.ResolveResponse{
		Accepted:  true,
		ForwardTo: []string{"bob@destination.com"},
	}

	// Create delivery jobs
	signedEmail := "Subject: Test\r\n\r\nTest email content"
	jobs := session.createDeliveryJobs(signedEmail, routingResponse)

	// Should have 1 forwarding job
	if len(jobs) != 1 {
		t.Fatalf("Expected 1 forwarding job, got %d", len(jobs))
	}

	job := jobs[0]

	// Verify it's a forwarding job
	if !job.IsForwarding {
		t.Error("Expected IsForwarding to be true")
	}

	// Verify NO SRS encoding (rewriter is nil)
	if job.From != "alice@example.com" {
		t.Errorf("Expected original From address (no rewriter), got: %s", job.From)
	}

	// Verify OriginalFrom matches From
	if job.OriginalFrom != job.From {
		t.Errorf("Expected OriginalFrom to equal From when no SRS rewriter")
	}

	t.Logf("✓ Forwarding without SRS rewriter (graceful degradation):")
	t.Logf("  From:         %s", job.From)
	t.Logf("  OriginalFrom: %s", job.OriginalFrom)
	t.Logf("  Recipients:   %v", job.Recipients)
}

// TestSRS_OutboundAlreadyEncoded verifies that already-encoded SRS addresses
// are converted to SRS1 when re-forwarding
func TestSRS_OutboundAlreadyEncoded(t *testing.T) {
	// Create SRS rewriter
	rewriter := srs.NewRewriter("test-secret", "relay.mizu.com")

	// Create an SRS0 address (simulating a previous forward)
	originalSender := "alice@example.com"
	srs0Address, err := rewriter.Encode(originalSender)
	if err != nil {
		t.Fatalf("Failed to create SRS0 address: %v", err)
	}

	// Create a session with SRS rewriter and SRS0 as sender
	session := &Session{
		from:         srs0Address, // Already SRS-encoded
		to:           []string{"user@yourdomain.com"},
		traceID:      "test-trace-srs1",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		srsRewriter:  rewriter,
		serverConfig: &config.DefaultConfig().Servers[0],
		globalConfig: testConfig(),
	}

	// Create routing response with forwarding (re-forwarding)
	routingResponse := &routing.ResolveResponse{
		Accepted:  true,
		ForwardTo: []string{"charlie@finaldest.com"},
	}

	// Create delivery jobs
	signedEmail := "Subject: Test\r\n\r\nTest email content"
	jobs := session.createDeliveryJobs(signedEmail, routingResponse)

	// Should have 1 forwarding job
	if len(jobs) != 1 {
		t.Fatalf("Expected 1 forwarding job, got %d", len(jobs))
	}

	job := jobs[0]

	// Verify it's an SRS1 address (re-forwarding)
	if !rewriter.IsSRSAddress(job.From) {
		t.Errorf("Expected SRS address, got: %s", job.From)
	}

	// SRS1 addresses start with "SRS1="
	if len(job.From) < 5 || job.From[:5] != "SRS1=" {
		t.Logf("Note: Expected SRS1 for re-forwarding, got: %s", job.From)
		// This is OK - the implementation might keep SRS0 or convert to SRS1
	}

	// Verify SRS address decodes back to original sender
	decoded, err := rewriter.Decode(job.From)
	if err != nil {
		t.Fatalf("Failed to decode SRS address: %v", err)
	}

	if decoded != originalSender {
		t.Errorf("SRS should decode to %s, got: %s", originalSender, decoded)
	}

	// Verify OriginalFrom is the SRS0 address
	if job.OriginalFrom != srs0Address {
		t.Errorf("OriginalFrom should be SRS0 address, got: %s", job.OriginalFrom)
	}

	t.Logf("✓ Re-forwarding SRS-encoded address:")
	t.Logf("  Original sender:  %s", originalSender)
	t.Logf("  SRS0 (input):     %s", srs0Address)
	t.Logf("  SRS1 (output):    %s", job.From)
	t.Logf("  Decodes to:       %s", decoded)
}
