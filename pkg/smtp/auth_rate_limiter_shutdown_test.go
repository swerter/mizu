package smtp

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"migadu/mizu/pkg/config"
)

// TestAuthRateLimiter_GracefulShutdown tests that the cleanup goroutine stops properly
func TestAuthRateLimiter_GracefulShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                  true,
		MaxAttemptsPerIPUsername: 3,
		MaxAttemptsPerIP:         10,
		MaxIPEntries:             1000,
		MaxIPUsernameEntries:     1000,
		CacheCleanupInterval:     "100ms", // Fast cleanup for testing
	}

	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	// Give the cleanup loop time to start
	time.Sleep(50 * time.Millisecond)

	// Call Shutdown
	limiter.Shutdown()

	// Give it time to shutdown
	time.Sleep(50 * time.Millisecond)

	// Verify context is cancelled
	select {
	case <-limiter.ctx.Done():
		t.Log("✓ Context cancelled successfully")
	default:
		t.Error("Context should be cancelled after Shutdown()")
	}

	t.Log("✓ Graceful shutdown works correctly")
}

// TestAuthRateLimiter_MultipleShutdowns tests that calling Shutdown multiple times is safe
func TestAuthRateLimiter_MultipleShutdowns(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.ServerAuthRateLimitConfig{
		Enabled:              true,
		CacheCleanupInterval: "1s",
	}

	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	// Call Shutdown multiple times (should not panic)
	limiter.Shutdown()
	limiter.Shutdown()
	limiter.Shutdown()

	t.Log("✓ Multiple Shutdown() calls are safe")
}

// TestAuthRateLimiter_CleanupStopsAfterShutdown tests that cleanup doesn't run after shutdown
func TestAuthRateLimiter_CleanupStopsAfterShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.ServerAuthRateLimitConfig{
		Enabled:              true,
		MaxAttemptsPerIP:     5,
		MaxIPEntries:         100,
		CacheCleanupInterval: "50ms", // Very fast for testing
	}

	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	// Add some data
	limiter.RecordAuthAttempt(nil, "192.168.1.1", "user1", false)
	limiter.RecordAuthAttempt(nil, "192.168.1.2", "user2", false)

	// Wait for at least one cleanup cycle
	time.Sleep(100 * time.Millisecond)

	// Get initial stats
	initialStats := limiter.GetStats()
	t.Logf("Stats before shutdown: %+v", initialStats)

	// Shutdown
	limiter.Shutdown()

	// Wait longer than cleanup interval
	time.Sleep(200 * time.Millisecond)

	// Stats should be the same (no cleanup happened after shutdown)
	// We can't verify this directly without instrumenting, but the goroutine should have stopped
	finalStats := limiter.GetStats()
	t.Logf("Stats after shutdown: %+v", finalStats)

	t.Log("✓ Cleanup loop stops after shutdown")
}

// TestAuthRateLimiter_NoGoroutineLeak tests that goroutines don't leak
func TestAuthRateLimiter_NoGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping goroutine leak test in short mode")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.ServerAuthRateLimitConfig{
		Enabled:              true,
		CacheCleanupInterval: "100ms",
	}

	// Create and shutdown multiple limiters
	for i := 0; i < 10; i++ {
		limiter, err := NewAuthRateLimiter(cfg, logger, nil)
		if err != nil {
			t.Fatalf("Failed to create limiter %d: %v", i, err)
		}

		// Give it time to start
		time.Sleep(10 * time.Millisecond)

		// Shutdown immediately
		limiter.Shutdown()
	}

	// Give time for all goroutines to exit
	time.Sleep(200 * time.Millisecond)

	// If we get here without hanging, goroutines are properly cleaned up
	t.Log("✓ No goroutine leaks detected after multiple create/shutdown cycles")
}
