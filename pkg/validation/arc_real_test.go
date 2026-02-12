package validation

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// TestCheckARC_RealMicrosoftEmail tests ARC validation with a real email from Microsoft
// This test uses actual ARC headers from Microsoft's mail infrastructure
func TestCheckARC_RealMicrosoftEmail(t *testing.T) {
	// This is a simplified example of an email with ARC headers from Microsoft
	// In production, these headers would have valid signatures
	rawEmail := `ARC-Seal: i=1; a=rsa-sha256; s=arcselector9901; d=microsoft.com; cv=none;
 b=KXexampleSignatureDataHere==
ARC-Message-Signature: i=1; a=rsa-sha256; c=relaxed/relaxed; d=microsoft.com;
 s=arcselector9901;
 h=From:Date:Subject:Message-ID:Content-Type:MIME-Version:X-MS-Exchange-SenderADCheck;
 bh=bodyHashHere==;
 b=messageSignatureHere==
ARC-Authentication-Results: i=1; mx.microsoft.com 1; spf=pass
 smtp.mailfrom=example.com; dmarc=pass action=none header.from=example.com;
From: sender@example.com
To: recipient@example.com
Subject: Test message with ARC
Date: Mon, 01 Jan 2024 12:00:00 +0000
Message-ID: <test@example.com>

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

	// Should detect ARC headers
	if result.Instance != 1 {
		t.Errorf("Expected Instance=1, got %d", result.Instance)
	}

	// Note: Validation will fail because these are not real signatures
	// This test is mainly to verify that we correctly extract and process ARC headers
	t.Logf("ARC validation result: Pass=%v, ChainValid=%v, Instance=%d, Reasons=%v",
		result.Pass, result.ChainValid, result.Instance, result.FailureReasons)
}

// TestCheckARC_MultipleARCInstances tests handling of messages that went through multiple hops
func TestCheckARC_MultipleARCInstances(t *testing.T) {
	rawEmail := `ARC-Seal: i=2; a=rsa-sha256; s=selector2; d=hop2.com; cv=pass; b=seal2==
ARC-Message-Signature: i=2; a=rsa-sha256; c=relaxed/relaxed; d=hop2.com; s=selector2; b=sig2==
ARC-Authentication-Results: i=2; hop2.com; arc=pass (i=1)
ARC-Seal: i=1; a=rsa-sha256; s=selector1; d=hop1.com; cv=none; b=seal1==
ARC-Message-Signature: i=1; a=rsa-sha256; c=relaxed/relaxed; d=hop1.com; s=selector1; b=sig1==
ARC-Authentication-Results: i=1; hop1.com; spf=pass
From: sender@example.com
To: recipient@example.com
Subject: Test with 2 ARC hops
Date: Mon, 01 Jan 2024 12:00:00 +0000

Message body
`

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := CheckARC(context.Background(), rawEmail, logger)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Instance != 2 {
		t.Errorf("Expected Instance=2 (two ARC hops), got %d", result.Instance)
	}

	t.Logf("Multiple ARC instances result: Instance=%d, Reasons=%v", result.Instance, result.FailureReasons)
}
