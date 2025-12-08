package smtp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestHTTPAuthenticator_Authenticate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Test successful authentication
	t.Run("successful authentication", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer test-api-key" {
				t.Errorf("expected Bearer token, got %s", r.Header.Get("Authorization"))
			}

			// Decode request
			var req AuthRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}

			if req.Username != "testuser" || req.Password != "testpass" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(AuthResponse{
					Success: false,
				})
				return
			}

			// Return success with allowed addresses
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(AuthResponse{
				Success:     true,
				User:        "testuser",
				AllowedFrom: []string{"testuser@example.com", "alias@example.com"},
			})
		}))
		defer server.Close()

		auth := NewHTTPAuthenticator(server.URL, "test-api-key", logger)

		// Test authentication
		authenticated, err := auth.Authenticate("testuser", "testpass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !authenticated {
			t.Error("expected authentication to succeed")
		}

		// Test CanSendAs
		if !auth.CanSendAs("testuser", "testuser@example.com") {
			t.Error("expected user to be able to send from testuser@example.com")
		}
		if !auth.CanSendAs("testuser", "alias@example.com") {
			t.Error("expected user to be able to send from alias@example.com")
		}
		if auth.CanSendAs("testuser", "other@example.com") {
			t.Error("expected user NOT to be able to send from other@example.com")
		}
	})

	// Test failed authentication
	t.Run("failed authentication", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(AuthResponse{
				Success:      false,
				ErrorMessage: "invalid credentials",
			})
		}))
		defer server.Close()

		auth := NewHTTPAuthenticator(server.URL, "test-api-key", logger)

		authenticated, err := auth.Authenticate("testuser", "wrongpass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if authenticated {
			t.Error("expected authentication to fail")
		}
	})

	// Test authentication service error
	t.Run("service error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		auth := NewHTTPAuthenticator(server.URL, "test-api-key", logger)

		authenticated, err := auth.Authenticate("testuser", "testpass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if authenticated {
			t.Error("expected authentication to fail on service error")
		}
	})

	// Test caching
	t.Run("authentication caching", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(AuthResponse{
				Success:     true,
				User:        "testuser",
				AllowedFrom: []string{"testuser@example.com"},
			})
		}))
		defer server.Close()

		auth := NewHTTPAuthenticator(server.URL, "test-api-key", logger)
		auth.cacheTTL = 1 * time.Second

		// First authentication should hit the server
		auth.Authenticate("testuser", "testpass")
		if callCount != 1 {
			t.Errorf("expected 1 call, got %d", callCount)
		}

		// Second authentication should use cache
		auth.Authenticate("testuser", "testpass")
		if callCount != 1 {
			t.Errorf("expected 1 call (cached), got %d", callCount)
		}

		// Wait for cache to expire
		time.Sleep(1100 * time.Millisecond)

		// Third authentication should hit the server again
		auth.Authenticate("testuser", "testpass")
		if callCount != 2 {
			t.Errorf("expected 2 calls (cache expired), got %d", callCount)
		}
	})
}

func TestExtractEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user@example.com", "user@example.com"},
		{"User Name <user@example.com>", "user@example.com"},
		{"  user@example.com  ", "user@example.com"},
		{"<user@example.com>", "user@example.com"},
		{"User@Example.COM", "user@example.com"},
		{"User Name <User@Example.COM>", "user@example.com"},
	}

	for _, tt := range tests {
		result := extractEmail(tt.input)
		if result != tt.expected {
			t.Errorf("extractEmail(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
