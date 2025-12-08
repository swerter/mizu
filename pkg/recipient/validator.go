package recipient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"log/slog"
)

// Validator handles recipient validation with caching
type Validator struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger

	// Caching
	cache    *expirable.LRU[string, *ValidateResponse]
	cacheTTL time.Duration

	mu sync.RWMutex
}

// ValidatorConfig holds configuration for the recipient validator
type ValidatorConfig struct {
	URL                string
	APIKey             string
	HTTPTimeoutSeconds int
	CacheTTLSeconds    int
	Logger             *slog.Logger
}

// NewValidator creates a new recipient validator
func NewValidator(cfg ValidatorConfig) (*Validator, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("recipient validation URL is required")
	}

	if cfg.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	// Set defaults
	if cfg.HTTPTimeoutSeconds == 0 {
		cfg.HTTPTimeoutSeconds = 5
	}
	if cfg.CacheTTLSeconds == 0 {
		cfg.CacheTTLSeconds = 300 // 5 minutes
	}

	httpClient := &http.Client{
		Timeout: time.Duration(cfg.HTTPTimeoutSeconds) * time.Second,
	}

	// Create LRU cache
	cache := expirable.NewLRU[string, *ValidateResponse](
		10000, // Max 10k cached entries
		nil,   // no eviction callback
		time.Duration(cfg.CacheTTLSeconds)*time.Second,
	)

	return &Validator{
		endpoint:   cfg.URL,
		apiKey:     cfg.APIKey,
		httpClient: httpClient,
		logger:     cfg.Logger,
		cache:      cache,
		cacheTTL:   time.Duration(cfg.CacheTTLSeconds) * time.Second,
	}, nil
}

// Validate checks if a recipient should be accepted during RCPT TO
func (v *Validator) Validate(ctx context.Context, clientIP, from, to string) (*ValidateResponse, error) {
	cacheKey := v.buildCacheKey(clientIP, from, to)

	// Check cache first
	if cached, ok := v.cache.Get(cacheKey); ok {
		v.logger.Debug("Recipient validation cache hit",
			"to", to,
			"from", from,
			"client_ip", clientIP)
		return cached, nil
	}

	// Cache miss - query the endpoint
	v.logger.Debug("Recipient validation cache miss - querying endpoint",
		"to", to,
		"endpoint", v.endpoint)

	req := ValidateRequest{
		ClientIP:     clientIP,
		EnvelopeFrom: from,
		EnvelopeTo:   to,
	}

	resp, err := v.queryEndpoint(ctx, req)
	if err != nil {
		v.logger.Warn("Recipient validation failed",
			"to", to,
			"error", err)
		return nil, err
	}

	// Cache the result (only cache accepted recipients to avoid caching temporary failures)
	if resp.Accepted {
		v.cache.Add(cacheKey, resp)
		v.logger.Debug("Cached recipient validation result",
			"key", cacheKey,
			"accepted", resp.Accepted)
	}

	return resp, nil
}

// queryEndpoint makes the HTTP request to the validation endpoint
func (v *Validator) queryEndpoint(ctx context.Context, req ValidateRequest) (*ValidateResponse, error) {
	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", v.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+v.apiKey)
	}

	// Execute request
	httpResp, err := v.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle different HTTP status codes
	switch httpResp.StatusCode {
	case http.StatusOK:
		// 200 OK - recipient accepted
		var resp ValidateResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			// If body is not JSON, treat as accepted with empty message
			return &ValidateResponse{Accepted: true}, nil
		}
		resp.Accepted = true
		return &resp, nil

	case http.StatusNotFound:
		// 404 Not Found - recipient does not exist
		var resp ValidateResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			// Default message if body is not JSON
			return &ValidateResponse{
				Accepted: false,
				Message:  "User unknown",
			}, nil
		}
		resp.Accepted = false
		return &resp, nil

	case http.StatusForbidden:
		// 403 Forbidden - sender blocked by recipient
		var resp ValidateResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			// Default message if body is not JSON
			return &ValidateResponse{
				Accepted: false,
				Message:  "Delivery not authorized",
			}, nil
		}
		resp.Accepted = false
		return &resp, nil

	case http.StatusTooManyRequests:
		// 429 Too Many Requests - rate limit exceeded
		return nil, fmt.Errorf("rate limit exceeded (HTTP 429)")

	default:
		// All other status codes are treated as errors
		return nil, fmt.Errorf("validation endpoint returned status %d: %s", httpResp.StatusCode, string(respBody))
	}
}

// buildCacheKey creates a cache key from request parameters
func (v *Validator) buildCacheKey(clientIP, from, to string) string {
	// Include all parameters in cache key for granular caching
	return fmt.Sprintf("%s:%s:%s", clientIP, from, to)
}

// GetStats returns cache statistics
func (v *Validator) GetStats() map[string]interface{} {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return map[string]interface{}{
		"cache_entries": v.cache.Len(),
		"endpoint":      v.endpoint,
	}
}

// FlushCache clears all cached entries
func (v *Validator) FlushCache() {
	v.cache.Purge()
	v.logger.Info("Recipient validation cache flushed")
}
