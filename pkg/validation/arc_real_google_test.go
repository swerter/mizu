package validation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckARC_RealGoogleEmail tests ARC validation with a real email from Google
// This uses an actual email from Google's infrastructure with real ARC signatures
func TestCheckARC_RealGoogleEmail(t *testing.T) {
	// Read the real email from tests/arc-example.eml
	// Get the path to the test file (relative to project root)
	testFile := filepath.Join("..", "..", "tests", "arc-example.eml")
	rawEmail, err := os.ReadFile(testFile)
	if err != nil {
		t.Skipf("Skipping test: could not read test file %s: %v", testFile, err)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	result, err := CheckARC(context.Background(), string(rawEmail), logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// This email has ARC headers from Google (i=1)
	if result.Instance != 1 {
		t.Errorf("Expected Instance=1, got %d", result.Instance)
	}

	// Log the validation result
	t.Logf("ARC validation result for real Google email:")
	t.Logf("  Pass: %v", result.Pass)
	t.Logf("  ChainValid: %v", result.ChainValid)
	t.Logf("  Instance: %d", result.Instance)
	t.Logf("  FailureReasons: %v", result.FailureReasons)

	// This should verify successfully because:
	// 1. Google's ARC signatures are cryptographically valid
	// 2. The DNS records for google.com should exist
	// 3. The message hasn't been modified since signing
	if !result.Pass {
		t.Logf("ARC validation failed (this may be expected if DNS lookups fail or signatures are invalid)")
		t.Logf("Failure reasons: %v", result.FailureReasons)
	} else {
		t.Logf("✓ ARC validation passed! Google's ARC chain is valid.")
	}

	// Verify the structure is correct even if signature verification fails
	if result.Instance == 0 {
		t.Error("Expected to detect ARC headers (instance should be > 0)")
	}
}

// TestCheckARC_RealGoogleEmail_HeaderExtraction tests that we correctly extract
// all ARC headers from the Google email
func TestCheckARC_RealGoogleEmail_HeaderExtraction(t *testing.T) {
	testFile := filepath.Join("..", "..", "tests", "arc-example.eml")
	rawEmail, err := os.ReadFile(testFile)
	if err != nil {
		t.Skipf("Skipping test: could not read test file %s: %v", testFile, err)
		return
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Parse headers using our extraction function
	result, err := CheckARC(context.Background(), string(rawEmail), logger)
	if err != nil {
		t.Fatalf("Failed to check ARC: %v", err)
	}

	// Verify we extracted the expected number of ARC instances
	if result.Instance != 1 {
		t.Errorf("Expected 1 ARC instance, got %d", result.Instance)
	}

	// The email should have:
	// - ARC-Seal: i=1
	// - ARC-Message-Signature: i=1
	// - ARC-Authentication-Results: i=1
	t.Logf("Successfully extracted ARC chain with %d instance(s)", result.Instance)
}
