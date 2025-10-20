package smtp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HTTPAuthenticator authenticates users via HTTPS API
type HTTPAuthenticator struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewHTTPAuthenticator creates a new HTTPS-based authenticator
func NewHTTPAuthenticator(endpoint, apiKey string, logger *slog.Logger) *HTTPAuthenticator {
	return &HTTPAuthenticator{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// AuthRequest represents the authentication request payload
type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse represents the authentication response
type AuthResponse struct {
	Success      bool     `json:"success"`
	User         string   `json:"user,omitempty"`
	AllowedFrom  []string `json:"allowed_from,omitempty"` // Email addresses user can send as
	ErrorMessage string   `json:"error,omitempty"`
}

// Authenticate verifies username and password via HTTP endpoint
func (a *HTTPAuthenticator) Authenticate(username, password string) (bool, error) {
	reqBody := AuthRequest{
		Username: username,
		Password: password,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		a.logger.Error("Failed to marshal auth request", "error", err)
		return false, fmt.Errorf("internal error")
	}

	req, err := http.NewRequest("POST", a.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		a.logger.Error("Failed to create auth request", "error", err)
		return false, fmt.Errorf("internal error")
	}

	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("Auth request failed", "endpoint", a.endpoint, "error", err)
		return false, fmt.Errorf("authentication service unavailable")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logger.Error("Failed to read auth response", "error", err)
		return false, fmt.Errorf("internal error")
	}

	if resp.StatusCode != http.StatusOK {
		a.logger.Warn("Auth request failed",
			"username", username,
			"status", resp.StatusCode,
			"response", string(body))
		return false, nil // Failed auth is not an error
	}

	var authResp AuthResponse
	if err := json.Unmarshal(body, &authResp); err != nil {
		a.logger.Error("Failed to parse auth response", "error", err, "body", string(body))
		return false, fmt.Errorf("internal error")
	}

	if authResp.Success {
		a.logger.Info("Authentication successful", "username", username, "user", authResp.User)
		return true, nil
	}

	a.logger.Info("Authentication failed", "username", username, "reason", authResp.ErrorMessage)
	return false, nil
}

// CanSendAs checks if authenticated user can send as a specific FROM address
func (a *HTTPAuthenticator) CanSendAs(authenticatedUser, fromAddress string) bool {
	// Normalize addresses for comparison
	authUser := strings.ToLower(strings.TrimSpace(authenticatedUser))
	fromAddr := strings.ToLower(strings.TrimSpace(fromAddress))

	// Extract email address from FROM (may include display name)
	// e.g., "John Doe <john@example.com>" -> "john@example.com"
	if strings.Contains(fromAddr, "<") && strings.Contains(fromAddr, ">") {
		start := strings.Index(fromAddr, "<")
		end := strings.Index(fromAddr, ">")
		if start < end {
			fromAddr = fromAddr[start+1 : end]
		}
	}

	// Simple rule: authenticated user must match FROM address
	// For more complex rules (aliases, domains), the auth endpoint should return allowed_from list
	if authUser == fromAddr {
		return true
	}

	a.logger.Warn("FROM address mismatch",
		"authenticated", authUser,
		"from", fromAddr)
	return false
}
