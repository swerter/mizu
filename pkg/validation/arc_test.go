package validation

import (
	"io"

	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/authres"
	"log/slog"
)

func TestCheckARC_NoARCHeaders(t *testing.T) {
	rawEmail := `From: sender@example.com
To: recipient@example.com
Subject: Test message

This is a test message without ARC headers.
`

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := CheckARC(context.Background(), rawEmail, logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// No ARC headers should pass (nothing to validate)
	if !result.Pass {
		t.Error("Expected Pass=true for message without ARC headers")
	}

	if !result.ChainValid {
		t.Error("Expected ChainValid=true for message without ARC headers")
	}

	if result.Instance != 0 {
		t.Errorf("Expected Instance=0, got %d", result.Instance)
	}
}

func TestCheckARC_WithARCHeaders(t *testing.T) {
	// Sample email with ARC headers (simplified format)
	rawEmail := `ARC-Seal: i=1; a=rsa-sha256; d=example.com; s=selector; t=1234567890; cv=none;
 b=dGVzdHNpZ25hdHVyZQ==
ARC-Message-Signature: i=1; a=rsa-sha256; d=example.com; s=selector;
 h=from:to:subject; bh=aGVsbG8=; b=dGVzdHNpZ25hdHVyZQ==
ARC-Authentication-Results: i=1; example.com;
 spf=pass smtp.mailfrom=sender@example.com;
 dkim=pass header.d=example.com
From: sender@example.com
To: recipient@example.com
Subject: Test message

This is a test message with ARC headers.
`

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := CheckARC(context.Background(), rawEmail, logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Should detect ARC instance
	if result.Instance != 1 {
		t.Errorf("Expected Instance=1, got %d", result.Instance)
	}

	// Note: Actual signature verification will fail because these are dummy signatures
	// In a real test, we'd need valid signatures or mock the verification
}

func TestCheckARC_MultipleInstances(t *testing.T) {
	// Sample email with multiple ARC sets (forwarded through multiple hops)
	rawEmail := `ARC-Seal: i=2; a=rsa-sha256; d=forwarder.com; s=selector; t=1234567890; cv=pass;
 b=dGVzdHNpZ25hdHVyZTI=
ARC-Message-Signature: i=2; a=rsa-sha256; d=forwarder.com; s=selector;
 h=from:to:subject; bh=aGVsbG8=; b=dGVzdHNpZ25hdHVyZTI=
ARC-Authentication-Results: i=2; forwarder.com;
 arc=pass (i=1)
ARC-Seal: i=1; a=rsa-sha256; d=example.com; s=selector; t=1234567890; cv=none;
 b=dGVzdHNpZ25hdHVyZTE=
ARC-Message-Signature: i=1; a=rsa-sha256; d=example.com; s=selector;
 h=from:to:subject; bh=aGVsbG8=; b=dGVzdHNpZ25hdHVyZTE=
ARC-Authentication-Results: i=1; example.com;
 spf=pass smtp.mailfrom=sender@example.com
From: sender@example.com
To: recipient@example.com
Subject: Test message

This is a test message with multiple ARC headers.
`

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := CheckARC(context.Background(), rawEmail, logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Should detect highest ARC instance
	if result.Instance != 2 {
		t.Errorf("Expected Instance=2, got %d", result.Instance)
	}
}

func TestExtractInstance(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected int
	}{
		{
			name:     "valid instance 1",
			header:   "i=1; a=rsa-sha256; d=example.com",
			expected: 1,
		},
		{
			name:     "valid instance 2",
			header:   "i=2; a=rsa-sha256; d=example.com",
			expected: 2,
		},
		{
			name:     "no instance",
			header:   "a=rsa-sha256; d=example.com",
			expected: 0,
		},
		{
			name:     "invalid instance",
			header:   "i=abc; a=rsa-sha256; d=example.com",
			expected: 0,
		},
		{
			name:     "instance with spaces",
			header:   "i= 1 ; a=rsa-sha256; d=example.com",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractInstance(tt.header)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "valid domain",
			header:   "i=1; a=rsa-sha256; d=example.com",
			expected: "example.com",
		},
		{
			name:     "domain with subdomain",
			header:   "i=1; a=rsa-sha256; d=mail.example.com",
			expected: "mail.example.com",
		},
		{
			name:     "no domain",
			header:   "i=1; a=rsa-sha256",
			expected: "",
		},
		{
			name:     "domain with spaces",
			header:   "i=1; a=rsa-sha256; d= example.com ",
			expected: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDomain(tt.header)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestCheckARC_InvalidEmail(t *testing.T) {
	rawEmail := "invalid email format"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := CheckARC(context.Background(), rawEmail, logger)

	if err == nil {
		t.Error("Expected error for invalid email format, got nil")
	}
}

func TestCheckARC_IncompleteARCSet(t *testing.T) {
	// Email with incomplete ARC set (missing ARC-Message-Signature)
	rawEmail := `ARC-Seal: i=1; a=rsa-sha256; d=example.com; s=selector; t=1234567890; cv=none;
 b=dGVzdHNpZ25hdHVyZQ==
ARC-Authentication-Results: i=1; example.com;
 spf=pass smtp.mailfrom=sender@example.com
From: sender@example.com
To: recipient@example.com
Subject: Test message

This is a test message with incomplete ARC headers.
`

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := CheckARC(context.Background(), rawEmail, logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Should detect instance even if incomplete
	if result.Instance != 1 {
		t.Errorf("Expected Instance=1, got %d", result.Instance)
	}

	// Should fail validation due to incomplete set
	if result.Pass {
		t.Error("Expected Pass=false for incomplete ARC set")
	}

	if result.ChainValid {
		t.Error("Expected ChainValid=false for incomplete ARC set")
	}
}

func TestGetARCAuthenticationResults_NoARC(t *testing.T) {
	rawEmail := `From: sender@example.com
To: recipient@example.com
Subject: Test message

This is a test message without ARC headers.
`

	results, err := GetARCAuthenticationResults(rawEmail)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if results != nil {
		t.Errorf("Expected nil results for email without ARC, got %v", results)
	}
}

// TestCheckARC_ChainValidation tests the chain validation logic
func TestCheckARC_ChainValidation(t *testing.T) {
	tests := []struct {
		name           string
		rawEmail       string
		expectPass     bool
		expectInstance int
	}{
		{
			name: "no ARC headers",
			rawEmail: `From: sender@example.com
To: recipient@example.com
Subject: Test

Body`,
			expectPass:     true,
			expectInstance: 0,
		},
		{
			name: "single complete ARC set",
			rawEmail: `ARC-Seal: i=1; d=example.com
ARC-Message-Signature: i=1; d=example.com
ARC-Authentication-Results: i=1; example.com
From: sender@example.com
To: recipient@example.com
Subject: Test

Body`,
			expectPass:     false, // Will fail signature verification
			expectInstance: 1,
		},
		{
			name: "incomplete ARC set - missing seal",
			rawEmail: `ARC-Message-Signature: i=1; d=example.com
ARC-Authentication-Results: i=1; example.com
From: sender@example.com
To: recipient@example.com
Subject: Test

Body`,
			expectPass:     false,
			expectInstance: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			result, err := CheckARC(context.Background(), tt.rawEmail, logger)

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.Instance != tt.expectInstance {
				t.Errorf("Expected instance %d, got %d", tt.expectInstance, result.Instance)
			}

			if result.Pass != tt.expectPass {
				t.Errorf("Expected pass=%v, got %v", tt.expectPass, result.Pass)
			}
		})
	}
}

// TestARCSignerBasic tests basic ARC signing functionality
func TestARCSignerBasic(t *testing.T) {
	// Generate test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	signer := &ARCSigner{
		Domain:     "example.com",
		Selector:   "arc",
		PrivateKey: privateKey,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rawEmail := `From: sender@test.com
To: recipient@test.com
Subject: Test Email
Date: Mon, 1 Jan 2024 12:00:00 +0000
Message-ID: <test@example.com>

This is a test email body.
`

	spfResult := &SPFResult{
		Domain: "test.com",
		Result: authres.SPFResult{Value: authres.ResultPass},
	}

	dmarcResult := &DMARCResult{
		Pass: true,
	}

	signedEmail, err := signer.SignEmail(rawEmail, spfResult, dmarcResult, nil)
	if err != nil {
		t.Fatalf("Failed to sign email: %v", err)
	}

	// Check that ARC headers were added
	if !strings.Contains(signedEmail, "ARC-Seal:") {
		t.Error("Expected ARC-Seal header in signed email")
	}
	if !strings.Contains(signedEmail, "ARC-Message-Signature:") {
		t.Error("Expected ARC-Message-Signature header in signed email")
	}
	if !strings.Contains(signedEmail, "ARC-Authentication-Results:") {
		t.Error("Expected ARC-Authentication-Results header in signed email")
	}

	// Check that ARC headers come before original email
	arcSealIdx := strings.Index(signedEmail, "ARC-Seal:")
	fromIdx := strings.Index(signedEmail, "From:")
	if arcSealIdx > fromIdx {
		t.Error("ARC headers should come before original email headers")
	}

	// Check instance number
	if !strings.Contains(signedEmail, "i=1") {
		t.Error("Expected instance i=1 in ARC headers")
	}
}

// TestARCSignerMultipleInstances tests ARC signing with existing ARC chain
func TestARCSignerMultipleInstances(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	signer := &ARCSigner{
		Domain:     "forwarder.com",
		Selector:   "arc",
		PrivateKey: privateKey,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Email already has ARC instance 1
	rawEmail := `ARC-Seal: i=1; d=original.com; s=arc; cv=none; b=dGVzdA==
ARC-Message-Signature: i=1; d=original.com; s=arc; h=from:to; b=dGVzdA==
ARC-Authentication-Results: i=1; original.com; spf=pass
From: sender@test.com
To: recipient@test.com
Subject: Test Email

Body
`

	arcResult := &ARCResult{
		Pass:     true,
		Instance: 1,
	}

	signedEmail, err := signer.SignEmail(rawEmail, nil, nil, arcResult)
	if err != nil {
		t.Fatalf("Failed to sign email: %v", err)
	}

	// Should add instance 2
	if !strings.Contains(signedEmail, "i=2") {
		t.Error("Expected instance i=2 in new ARC headers")
	}

	// Should still have original instance 1
	if !strings.Contains(signedEmail, "i=1") {
		t.Error("Expected instance i=1 to be preserved")
	}
}

// TestNewARCSignerFromFile tests loading RSA key from file
func TestNewARCSignerFromFile(t *testing.T) {
	// Generate test key
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "arc-key-*.pem")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Encode as PEM
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	if err := pem.Encode(tmpFile, pemBlock); err != nil {
		t.Fatalf("Failed to encode PEM: %v", err)
	}
	tmpFile.Close()

	// Load signer
	signer, err := NewARCSigner("example.com", "arc", tmpFile.Name(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Failed to create ARC signer: %v", err)
	}

	if signer.Domain != "example.com" {
		t.Errorf("Expected domain example.com, got %s", signer.Domain)
	}

	if signer.Selector != "arc" {
		t.Errorf("Expected selector arc, got %s", signer.Selector)
	}

	if signer.PrivateKey == nil {
		t.Error("Expected private key to be loaded")
	}
}

// TestNewARCSignerInvalidKey tests error handling for invalid keys
func TestNewARCSignerInvalidKey(t *testing.T) {
	// Non-existent file
	_, err := NewARCSigner("example.com", "arc", "/nonexistent/key.pem", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Error("Expected error for non-existent key file")
	}

	// Invalid PEM file
	tmpFile, err := os.CreateTemp("", "invalid-*.pem")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString("not a valid PEM file")
	tmpFile.Close()

	_, err = NewARCSigner("example.com", "arc", tmpFile.Name(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Error("Expected error for invalid PEM file")
	}
}

// TestBuildAuthenticationResults tests the authentication results header building
func TestBuildAuthenticationResults(t *testing.T) {
	signer := &ARCSigner{
		Domain:   "example.com",
		Selector: "arc",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	spfResult := &SPFResult{
		Domain: "test.com",
		Result: authres.SPFResult{Value: authres.ResultPass},
	}

	dmarcResult := &DMARCResult{
		Pass: true,
	}

	arcResult := &ARCResult{
		Pass:     true,
		Instance: 1,
	}

	aar := signer.buildAuthenticationResults(2, spfResult, dmarcResult, arcResult)

	if !strings.Contains(aar, "i=2") {
		t.Error("Expected instance i=2")
	}
	if !strings.Contains(aar, "example.com") {
		t.Error("Expected domain example.com")
	}
	if !strings.Contains(aar, "spf=pass") {
		t.Error("Expected spf=pass")
	}
	if !strings.Contains(aar, "dmarc=pass") {
		t.Error("Expected dmarc=pass")
	}
	if !strings.Contains(aar, "arc=pass") {
		t.Error("Expected arc=pass")
	}
}
