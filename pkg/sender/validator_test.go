package sender

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewValidator tests validator creation
func TestNewValidator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name    string
		config  ValidatorConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ValidatorConfig{
				URL:    "https://api.example.com/validate",
				Logger: logger,
			},
			wantErr: false,
		},
		{
			name: "missing URL",
			config: ValidatorConfig{
				Logger: logger,
			},
			wantErr: true,
		},
		{
			name: "missing logger",
			config: ValidatorConfig{
				URL: "https://api.example.com/validate",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator, err := NewValidator(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewValidator() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && validator == nil {
				t.Error("Expected non-nil validator")
			}
		})
	}

	t.Log("✓ Validator creation works correctly")
}

// TestValidator_AcceptSender tests accepting a valid sender
func TestValidator_AcceptSender(t *testing.T) {
	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("Missing or incorrect Authorization header")
		}

		// Return 200 OK
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Sender accepted",
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:       server.URL + "/validate?email=$email",
		AuthToken: "test-token",
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()
	resp, err := validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	if !resp.Accepted {
		t.Error("Expected sender to be accepted")
	}

	if resp.Message != "Sender accepted" {
		t.Errorf("Unexpected message: %s", resp.Message)
	}

	t.Log("✓ Accepting valid sender works")
}

// TestValidator_RejectSender_NotFound tests rejecting unauthorized sender
func TestValidator_RejectSender_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Sender not authorized",
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate?email=$email",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()
	resp, err := validator.Validate(ctx, "192.168.1.1", "unauthorized@example.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if resp.Accepted {
		t.Error("Expected sender to be rejected")
	}

	if resp.Message != "Sender not authorized" {
		t.Errorf("Expected 'Sender not authorized', got %s", resp.Message)
	}

	t.Log("✓ Rejecting unauthorized sender works")
}

// TestValidator_RejectSender_Forbidden tests blocking sender
func TestValidator_RejectSender_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Sender address rejected",
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate?email=$email",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()
	resp, err := validator.Validate(ctx, "192.168.1.1", "blocked@example.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if resp.Accepted {
		t.Error("Expected sender to be rejected")
	}

	if resp.Message != "Sender address rejected" {
		t.Errorf("Unexpected message: %s", resp.Message)
	}

	t.Log("✓ Blocking sender works")
}

// TestValidator_RateLimit tests rate limit handling
func TestValidator_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Rate limit exceeded"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()
	_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err == nil {
		t.Error("Expected error for rate limit")
	}

	if err.Error() != "rate limit exceeded (HTTP 429)" {
		t.Errorf("Unexpected error: %v", err)
	}

	t.Log("✓ Rate limit handling works")
}

// TestValidator_TemporaryFailure tests temporary failure handling
func TestValidator_TemporaryFailure(t *testing.T) {
	statusCodes := []int{
		http.StatusServiceUnavailable,
		http.StatusBadGateway,
		http.StatusGatewayTimeout,
	}

	for _, statusCode := range statusCodes {
		t.Run(fmt.Sprintf("HTTP_%d", statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			validator, err := NewValidator(ValidatorConfig{
				URL:    server.URL + "/validate",
				Logger: logger,
			})
			if err != nil {
				t.Fatalf("Failed to create validator: %v", err)
			}

			ctx := context.Background()
			_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
			if err == nil {
				t.Error("Expected error for temporary failure")
			}

			expectedErr := fmt.Sprintf("temporary failure (HTTP %d)", statusCode)
			if err.Error() != expectedErr {
				t.Errorf("Expected '%s', got '%v'", expectedErr, err)
			}
		})
	}

	t.Log("✓ Temporary failure handling works")
}

// TestValidator_URLInterpolation tests URL parameter interpolation
func TestValidator_URLInterpolation(t *testing.T) {
	capturedURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate?ip=$ip&ptr=$ptr&helo=$helo&from=$from&email=$email",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "")
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	// Verify all parameters were interpolated (URL-encoded)
	expectedParams := map[string]string{
		"ip":    "192.168.1.1",
		"ptr":   "mail.example.com",
		"helo":  "helo.example.com",
		"from":  "sender%40example.com", // URL-encoded @
		"email": "sender%40example.com", // URL-encoded @
	}

	for key, value := range expectedParams {
		expected := fmt.Sprintf("%s=%s", key, value)
		if !contains(capturedURL, expected) {
			t.Errorf("URL missing parameter %s. Got: %s", expected, capturedURL)
		}
	}

	t.Log("✓ URL parameter interpolation works")
}

// TestValidator_Caching tests caching behavior
func TestValidator_Caching(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"message": fmt.Sprintf("Request #%d", requestCount),
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:             server.URL + "/validate?email=$email",
		CacheTTLSeconds: 5, // 5 second cache
		Logger:          logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// First request - should hit server
	resp1, err := validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("First validation failed: %v", err)
	}

	// Second request - should use cache
	resp2, err := validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("Second validation failed: %v", err)
	}

	// Should only have made one HTTP request
	if requestCount != 1 {
		t.Errorf("Expected 1 HTTP request, got %d (caching not working)", requestCount)
	}

	// Both responses should be identical (from cache)
	if resp1.Message != resp2.Message {
		t.Errorf("Cached response differs from original")
	}

	t.Logf("✓ Caching works correctly (requestCount=%d)", requestCount)
}

// TestValidator_CacheExpiration tests cache expiration
func TestValidator_CacheExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping cache expiration test in short mode")
	}

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:             server.URL + "/validate",
		CacheTTLSeconds: 1, // 1 second cache
		Logger:          logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// First request
	_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("First validation failed: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(1100 * time.Millisecond)

	// Second request - cache expired, should hit server again
	_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("Second validation failed: %v", err)
	}

	// Should have made two HTTP requests
	if requestCount != 2 {
		t.Errorf("Expected 2 HTTP requests after cache expiration, got %d", requestCount)
	}

	t.Log("✓ Cache expiration works correctly")
}

// TestValidator_FlushCache tests cache flushing
func TestValidator_FlushCache(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// First request
	_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("First validation failed: %v", err)
	}

	// Flush cache
	validator.FlushCache()

	// Second request - cache flushed, should hit server again
	_, err = validator.Validate(ctx, "192.168.1.1", "sender@example.com")
	if err != nil {
		t.Fatalf("Second validation failed: %v", err)
	}

	// Should have made two HTTP requests
	if requestCount != 2 {
		t.Errorf("Expected 2 HTTP requests after flush, got %d", requestCount)
	}

	t.Log("✓ Cache flushing works correctly")
}

// TestValidator_GetStats tests statistics
func TestValidator_GetStats(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    "https://api.example.com/validate",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	stats := validator.GetStats()

	if stats == nil {
		t.Error("Expected non-nil stats")
	}

	if _, ok := stats["cache_entries"]; !ok {
		t.Error("Stats missing cache_entries")
	}

	if _, ok := stats["url_template"]; !ok {
		t.Error("Stats missing url_template")
	}

	t.Log("✓ GetStats works correctly")
}

// TestValidator_NoCacheForRejected tests that rejected senders are not cached
func TestValidator_NoCacheForRejected(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNotFound) // Reject
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// Make multiple requests for rejected sender
	for i := 0; i < 3; i++ {
		_, err = validator.Validate(ctx, "192.168.1.1", "unauthorized@example.com")
		if err != nil {
			t.Fatalf("Validation %d failed: %v", i+1, err)
		}
	}

	// Rejected responses should not be cached (to avoid caching temporary failures)
	// So we expect 3 HTTP requests
	if requestCount != 3 {
		t.Errorf("Expected 3 HTTP requests (no caching for rejected), got %d", requestCount)
	}

	t.Log("✓ Rejected senders are not cached")
}

// TestValidator_DefaultTimeouts tests default timeout values
func TestValidator_DefaultTimeouts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    "https://api.example.com/validate",
		Logger: logger,
		// Not setting HTTPTimeoutSeconds or CacheTTLSeconds to test defaults
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	// Check that defaults were applied
	if validator.httpClient.Timeout != 5*time.Second {
		t.Errorf("Expected default HTTP timeout of 5s, got %v", validator.httpClient.Timeout)
	}

	if validator.cacheTTL != 300*time.Second {
		t.Errorf("Expected default cache TTL of 300s, got %v", validator.cacheTTL)
	}

	t.Log("✓ Default timeout values are correct")
}

// TestValidator_AuthenticatedUserHeader tests that X-Auth-User header is sent
func TestValidator_AuthenticatedUserHeader(t *testing.T) {
	capturedHeaders := make(http.Header)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture headers
		for k, v := range r.Header {
			capturedHeaders[k] = v
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:    server.URL + "/validate",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// Test with authenticated user
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "testuser@example.com")
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	// Verify X-Auth-User header was sent
	if capturedHeaders.Get("X-Auth-User") != "testuser@example.com" {
		t.Errorf("Expected X-Auth-User header to be 'testuser@example.com', got '%s'", capturedHeaders.Get("X-Auth-User"))
	}

	// Clear captured headers
	capturedHeaders = make(http.Header)

	// Test without authenticated user (empty string)
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "")
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	// Verify X-Auth-User header was NOT sent
	if capturedHeaders.Get("X-Auth-User") != "" {
		t.Errorf("Expected X-Auth-User header to be empty, got '%s'", capturedHeaders.Get("X-Auth-User"))
	}

	t.Log("✓ X-Auth-User header handling works correctly")
}

// TestValidator_CachingWithAuthenticatedUser tests that cache is keyed by authenticated user
func TestValidator_CachingWithAuthenticatedUser(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator, err := NewValidator(ValidatorConfig{
		URL:             server.URL + "/validate",
		CacheTTLSeconds: 5,
		Logger:          logger,
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	ctx := context.Background()

	// Request 1: user1@example.com
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "user1@example.com")
	if err != nil {
		t.Fatalf("Validation 1 failed: %v", err)
	}

	// Request 2: same params but different user - should NOT use cache
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "user2@example.com")
	if err != nil {
		t.Fatalf("Validation 2 failed: %v", err)
	}

	// Request 3: same as request 1 - should use cache
	_, err = validator.ValidateWithContext(ctx, "192.168.1.1", "mail.example.com", "helo.example.com", "sender@example.com", "user1@example.com")
	if err != nil {
		t.Fatalf("Validation 3 failed: %v", err)
	}

	// Should have made 2 HTTP requests (request 3 used cache)
	if requestCount != 2 {
		t.Errorf("Expected 2 HTTP requests (third should use cache), got %d", requestCount)
	}

	t.Log("✓ Caching correctly keys by authenticated user")
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
