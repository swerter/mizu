package poster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// HTTPClient is the shared HTTP client for posting emails to the destination endpoint.
// Configured with a 30-second timeout to handle potentially slow backend systems.
var HTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// PostEmailToDestination sends the raw email content to the destination with retry logic.
// This is a convenience wrapper that uses a background context.
func PostEmailToDestination(rawEmail string, destinationURL, apiKey string, maxRetryAttempts int) error {
	return PostEmailToDestinationWithContext(context.Background(), rawEmail, destinationURL, apiKey, maxRetryAttempts, false)
}

// PostEmailToDestinationWithContext sends the raw email content to the destination with retry logic and context support.
// It implements exponential backoff between retries and respects context cancellation.
// The isJunk parameter adds an X-Junk header to help the destination system handle spam appropriately.
func PostEmailToDestinationWithContext(ctx context.Context, rawEmail string, destinationURL, apiKey string, maxRetryAttempts int, isJunk bool) error {
	var lastErr error

	// Ensure at least one attempt even if configured incorrectly
	if maxRetryAttempts < 1 {
		maxRetryAttempts = 1
	}

	// Retry loop with exponential backoff
	for attempt := 0; attempt < maxRetryAttempts; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		// Implement exponential backoff between retries to avoid overwhelming the destination
		// Backoff sequence: 0s (first attempt), 1s, 2s, 4s, 8s, etc.
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			log.Printf("Retrying HTTP post to URL (attempt %d/%d) after %v delay", attempt+1, maxRetryAttempts, backoff)

			// Sleep with context awareness - allows early cancellation
			select {
			case <-time.After(backoff):
				// Continue after backoff
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
			}
		}

		err := postEmailAttemptWithContext(ctx, rawEmail, destinationURL, apiKey, isJunk)
		if err == nil {
			// Success
			return nil
		}

		lastErr = err

		// Determine if the error warrants a retry
		// Non-retryable errors (like 4xx HTTP codes) fail immediately
		if !isRetryableError(err) {
			log.Printf("Non-retryable error posting to URL: %v", err)
			return err
		}

		if attempt < maxRetryAttempts-1 {
			log.Printf("Retryable error posting to URL (attempt %d/%d): %v", attempt+1, maxRetryAttempts, err)
		}
	}

	// All retries exhausted
	log.Printf("All retry attempts exhausted (%d/%d) posting to URL: %v", maxRetryAttempts, maxRetryAttempts, lastErr)
	return fmt.Errorf("failed after %d attempts: %w", maxRetryAttempts, lastErr)
}

// postEmailAttemptWithContext performs a single attempt to post the email with context support.
// It sends the raw email as message/rfc822 content type with API key authentication.
func postEmailAttemptWithContext(ctx context.Context, rawEmail string, destinationURL, apiKey string, isJunk bool) error {
	req, err := http.NewRequestWithContext(ctx, "POST", destinationURL, strings.NewReader(rawEmail))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set standard headers for email relay
	req.Header.Set("Content-Type", "message/rfc822") // RFC 2822 compliant email format
	req.Header.Set("X-API-Key", apiKey)              // Authentication header

	// Signal to destination that this message was classified as junk/spam
	if isJunk {
		req.Header.Set("X-Junk", "yes")
	}

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request to URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return NewHTTPStatusError(resp.StatusCode, string(bodyBytes))
	}

	log.Printf("Successfully sent email to destination URL, status: %d", resp.StatusCode)
	return nil
}

// isRetryableError determines if an error should trigger a retry.
// Returns false for permanent failures (4xx HTTP codes, context cancellation).
// Returns true for temporary failures (5xx codes, network errors, timeouts).
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's an HTTP status error with specific retry logic
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.IsRetryable() // 5xx errors are retryable, 4xx are not
	}

	// Context errors indicate intentional cancellation - don't retry
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are generally retryable as they're often temporary
	// Check for common network error patterns in the error message
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "dial tcp") {
		return true
	}

	// Default to retryable for unknown errors to err on the side of delivery
	return true
}
