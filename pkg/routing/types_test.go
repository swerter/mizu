package routing

import (
	"testing"
	"time"
)

func TestCachedResult_IsExpired(t *testing.T) {
	// Test expired result
	expired := &CachedResult{
		Response: &ResolveResponse{Accepted: true},
		CachedAt: time.Now().Add(-10 * time.Minute),
		TTL:      5 * time.Minute,
	}

	if !expired.IsExpired() {
		t.Error("Result should be expired")
	}

	// Test non-expired result
	notExpired := &CachedResult{
		Response: &ResolveResponse{Accepted: true},
		CachedAt: time.Now().Add(-2 * time.Minute),
		TTL:      5 * time.Minute,
	}

	if notExpired.IsExpired() {
		t.Error("Result should not be expired")
	}

	// Test result that just expired
	justExpired := &CachedResult{
		Response: &ResolveResponse{Accepted: true},
		CachedAt: time.Now().Add(-5*time.Minute - time.Second),
		TTL:      5 * time.Minute,
	}

	if !justExpired.IsExpired() {
		t.Error("Result should be expired")
	}
}

func TestCachedResult_IsExpired_EdgeCases(t *testing.T) {
	// Test with zero TTL
	zeroTTL := &CachedResult{
		Response: &ResolveResponse{Accepted: true},
		CachedAt: time.Now(),
		TTL:      0,
	}

	if !zeroTTL.IsExpired() {
		t.Error("Result with zero TTL should be expired")
	}

	// Test freshly cached
	fresh := &CachedResult{
		Response: &ResolveResponse{Accepted: true},
		CachedAt: time.Now(),
		TTL:      5 * time.Minute,
	}

	if fresh.IsExpired() {
		t.Error("Freshly cached result should not be expired")
	}
}

func TestErrorCodes(t *testing.T) {
	// Test error code constants exist
	codes := []string{
		ErrorCodeDomainNotFound,
		ErrorCodeRecipientNotFound,
		ErrorCodeRecipientBlocked,
		ErrorCodePolicyRejection,
		ErrorCodeQuotaExceeded,
	}

	for _, code := range codes {
		if code == "" {
			t.Error("Error code should not be empty")
		}
	}

	// Verify specific values
	if ErrorCodeDomainNotFound != "domain_not_found" {
		t.Errorf("ErrorCodeDomainNotFound = %s, want domain_not_found", ErrorCodeDomainNotFound)
	}
	if ErrorCodeRecipientNotFound != "recipient_not_found" {
		t.Errorf("ErrorCodeRecipientNotFound = %s, want recipient_not_found", ErrorCodeRecipientNotFound)
	}
}

func TestResolveRequest(t *testing.T) {
	req := ResolveRequest{
		Recipient: "user@example.com",
		Sender:    "sender@example.com",
		ClientIP:  "192.168.1.1",
		Subject:   "Test Subject",
	}

	if req.Recipient != "user@example.com" {
		t.Error("Recipient field incorrect")
	}
	if req.Sender != "sender@example.com" {
		t.Error("Sender field incorrect")
	}
	if req.ClientIP != "192.168.1.1" {
		t.Error("ClientIP field incorrect")
	}
	if req.Subject != "Test Subject" {
		t.Error("Subject field incorrect")
	}
}

func TestResolveResponse(t *testing.T) {
	resp := ResolveResponse{
		Accepted:         true,
		DeliverTo:        []string{"user1@example.com", "user2@example.com"},
		ForwardTo:        []string{"external@other.com"},
		DeliveryEndpoint: "https://api.example.com/deliver",
		ForwardEndpoint:  "smtp://relay.example.com",
		IsCatchall:       false,
		ErrorCode:        "",
		ErrorMessage:     "",
	}

	if !resp.Accepted {
		t.Error("Accepted should be true")
	}
	if len(resp.DeliverTo) != 2 {
		t.Error("DeliverTo should have 2 entries")
	}
	if len(resp.ForwardTo) != 1 {
		t.Error("ForwardTo should have 1 entry")
	}
}

func TestResolveResponse_Rejected(t *testing.T) {
	resp := ResolveResponse{
		Accepted:     false,
		ErrorCode:    ErrorCodeRecipientNotFound,
		ErrorMessage: "Recipient does not exist",
	}

	if resp.Accepted {
		t.Error("Accepted should be false")
	}
	if resp.ErrorCode != ErrorCodeRecipientNotFound {
		t.Error("ErrorCode incorrect")
	}
	if resp.ErrorMessage == "" {
		t.Error("ErrorMessage should not be empty")
	}
}
