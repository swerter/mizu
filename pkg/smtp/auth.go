package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPAuthenticator authenticates users via HTTPS API
type HTTPAuthenticator struct {
	url        string
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger

	// Cache for successful authentications (username -> cached data)
	cache      map[string]*authCacheEntry
	cacheMu    sync.RWMutex
	cacheTTL   time.Duration
	cacheClean time.Duration
}

type authCacheEntry struct {
	allowedFromAddresses []string
	expiresAt            time.Time
}

// NewHTTPAuthenticator creates a new HTTPS-based authenticator
func NewHTTPAuthenticator(url, apiKey string, logger *slog.Logger) *HTTPAuthenticator {
	auth := &HTTPAuthenticator{
		url:    url,
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger:     logger,
		cache:      make(map[string]*authCacheEntry),
		cacheTTL:   5 * time.Minute,  // Cache successful auth for 5 minutes
		cacheClean: 10 * time.Minute, // Cleanup every 10 minutes
	}

	// Start cache cleanup goroutine
	go auth.cleanupCache()

	return auth
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
	// Check cache first (for username only, not password)
	// This is safe because we only cache successful authentications
	// and password changes will expire after cacheTTL
	if entry := a.getCached(username); entry != nil {
		a.logger.Debug("authentication cache hit", "username", username)
		return true, nil
	}

	reqBody := AuthRequest{
		Username: username,
		Password: password,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		a.logger.Error("Failed to marshal auth request", "error", err)
		return false, fmt.Errorf("internal error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", a.url, bytes.NewBuffer(jsonData))
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
		a.logger.Error("Auth request failed", "url", a.url, "error", err)
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
		// Cache successful authentication with allowed FROM addresses
		a.cacheAuth(username, authResp.AllowedFrom)
		a.logger.Info("Authentication successful", "username", username, "user", authResp.User)
		return true, nil
	}

	a.logger.Info("Authentication failed", "username", username, "reason", authResp.ErrorMessage)
	return false, nil
}

// CanSendAs checks if authenticated user can send as a specific FROM address
func (a *HTTPAuthenticator) CanSendAs(authenticatedUser, fromAddress string) bool {
	// Get cached entry to check allowed FROM addresses
	entry := a.getCached(authenticatedUser)
	if entry == nil {
		// Not in cache - shouldn't happen if Authenticate was called first
		a.logger.Warn("CanSendAs called but user not in cache", "user", authenticatedUser)
		return false
	}

	// Normalize addresses for comparison
	authUser := strings.ToLower(strings.TrimSpace(authenticatedUser))
	fromAddr := extractEmail(fromAddress)

	// If no specific allowed addresses configured, default to username match
	if len(entry.allowedFromAddresses) == 0 {
		if authUser == fromAddr {
			return true
		}
		a.logger.Warn("FROM address mismatch (no allowed_from list)",
			"authenticated", authUser,
			"from", fromAddr)
		return false
	}

	// Check if from address is in allowed list
	for _, allowed := range entry.allowedFromAddresses {
		allowedEmail := extractEmail(allowed)
		if allowedEmail == fromAddr {
			return true
		}
	}

	a.logger.Warn("FROM address not in allowed list",
		"authenticated", authUser,
		"from", fromAddr,
		"allowed", entry.allowedFromAddresses)
	return false
}

// extractEmail extracts the email address from "Name <email>" or just "email" format
func extractEmail(address string) string {
	addr := strings.TrimSpace(address)

	// Extract email address from "Name <email>" format
	if strings.Contains(addr, "<") && strings.Contains(addr, ">") {
		start := strings.Index(addr, "<")
		end := strings.Index(addr, ">")
		if start < end {
			addr = addr[start+1 : end]
		}
	}

	return strings.ToLower(strings.TrimSpace(addr))
}

// getCached retrieves a cached authentication entry
func (a *HTTPAuthenticator) getCached(username string) *authCacheEntry {
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()

	entry, ok := a.cache[username]
	if !ok {
		return nil
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		return nil
	}

	return entry
}

// cacheAuth stores a successful authentication in cache
func (a *HTTPAuthenticator) cacheAuth(username string, allowedFromAddresses []string) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()

	a.cache[username] = &authCacheEntry{
		allowedFromAddresses: allowedFromAddresses,
		expiresAt:            time.Now().Add(a.cacheTTL),
	}
}

// cleanupCache periodically removes expired entries
func (a *HTTPAuthenticator) cleanupCache() {
	ticker := time.NewTicker(a.cacheClean)
	defer ticker.Stop()

	for range ticker.C {
		a.cacheMu.Lock()
		now := time.Now()
		for username, entry := range a.cache {
			if now.After(entry.expiresAt) {
				delete(a.cache, username)
			}
		}
		a.cacheMu.Unlock()
	}
}

// FlushCache clears the authentication cache
func (a *HTTPAuthenticator) FlushCache() {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	a.cache = make(map[string]*authCacheEntry)
	a.logger.Info("authentication cache flushed")
}

// LoginServer implements the LOGIN SASL mechanism server
// LOGIN is non-standard but widely used for SMTP authentication
type LoginServer struct {
	authenticator func(username, password string) error
	username      string
	step          int
}

// Next processes the LOGIN authentication handshake
func (l *LoginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.step {
	case 0:
		// First step: request username
		l.step = 1
		return []byte("Username:"), false, nil
	case 1:
		// Second step: got username, request password
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 2:
		// Third step: got password, authenticate
		password := string(response)
		err = l.authenticator(l.username, password)
		return nil, true, err
	default:
		return nil, true, fmt.Errorf("unexpected step in LOGIN authentication")
	}
}

// NewLoginServer creates a new LOGIN SASL server
func NewLoginServer(authenticator func(username, password string) error) *LoginServer {
	return &LoginServer{
		authenticator: authenticator,
		step:          0,
	}
}
