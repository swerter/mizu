package spamcheck

import (
	"context"
	"strings"
)

// Adapter wraps rspamd Client to implement the SMTP SpamChecker interface
type Adapter struct {
	client          *Client
	spamHeader      string // Header name to add when spam detected (e.g., "X-Junk")
	spamHeaderValue string // Header value for spam (e.g., "yes")
	hamHeaderValue  string // Header value for ham (empty = don't add for ham)
	rejectOnAction  string // Reject if rspamd action matches this (e.g., "reject")
}

// SpamCheckResult represents the spam check result for SMTP session
type SpamCheckResult struct {
	IsSpam       bool              // True if message should be treated as spam
	Action       string            // Rspamd action (e.g., "add header", "reject")
	Score        float64           // Spam score
	AddHeaders   map[string]string // Headers to add (from rspamd milter OR configured spam header)
	ShouldReject bool              // True if message should be rejected based on action
}

// NewAdapter creates a new spam checker adapter
func NewAdapter(client *Client, spamHeader, spamHeaderValue, hamHeaderValue, rejectOnAction string) *Adapter {
	// Set defaults
	if spamHeader == "" {
		spamHeader = "X-Junk"
	}
	if spamHeaderValue == "" {
		spamHeaderValue = "yes"
	}

	return &Adapter{
		client:          client,
		spamHeader:      spamHeader,
		spamHeaderValue: spamHeaderValue,
		hamHeaderValue:  hamHeaderValue,
		rejectOnAction:  rejectOnAction,
	}
}

// Check performs spam checking and returns result
func (a *Adapter) Check(ctx context.Context, message, clientIP, from string, rcpt []string, helo string) (SpamCheckResult, error) {
	// Call rspamd
	result, err := a.client.Check(ctx, message, clientIP, from, rcpt, helo)
	if err != nil {
		return SpamCheckResult{}, err
	}

	// Build adapter result
	adapterResult := SpamCheckResult{
		IsSpam:     result.IsSpam,
		Action:     result.Action,
		Score:      result.Score,
		AddHeaders: make(map[string]string),
	}

	// Check if we should reject based on configured action
	if a.rejectOnAction != "" && strings.EqualFold(result.Action, a.rejectOnAction) {
		adapterResult.ShouldReject = true
	}

	// Copy headers from rspamd milter response
	for name, value := range result.AddHeaders {
		adapterResult.AddHeaders[name] = value
	}

	// Add configured spam/ham header based on result
	if result.IsSpam && a.spamHeaderValue != "" {
		adapterResult.AddHeaders[a.spamHeader] = a.spamHeaderValue
	} else if !result.IsSpam && a.hamHeaderValue != "" {
		adapterResult.AddHeaders[a.spamHeader] = a.hamHeaderValue
	}

	return adapterResult, nil
}
