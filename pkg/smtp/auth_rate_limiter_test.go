package smtp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/logging"
)

// TestAuthRateLimiterBasicIPBlocking tests that IPs are blocked after exceeding max_attempts_per_ip (Tier 2)
func TestAuthRateLimiterBasicIPBlocking(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:          true,
		MaxAttemptsPerIP: 3, // Tier 2: Block after 3 failures
		IPBlockDuration:  "5m",
		IPWindowDuration: "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Record 2 failures - should not block yet
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)

	err = limiter.CanAttemptAuth(ctx, ip, "user@example.com")
	if err != nil {
		t.Errorf("Should not block after 2 failures (threshold 3): %v", err)
	}

	// Record 3rd failure - should trigger Tier 2 IP block
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)

	err = limiter.CanAttemptAuth(ctx, ip, "user@example.com")
	if err == nil {
		t.Error("Should block IP after 3 failures (threshold 3)")
	}

	// Different IP should still work
	ip2 := "192.168.1.101"
	err = limiter.CanAttemptAuth(ctx, ip2, "user@example.com")
	if err != nil {
		t.Errorf("Different IP should not be blocked: %v", err)
	}
}

// TestAuthRateLimiterProgressiveDelays tests that delays increase exponentially
func TestAuthRateLimiterProgressiveDelays(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:             true,
		DelayStartThreshold: 2, // Start delays after 2 failures
		InitialDelay:        "100ms",
		MaxDelay:            "1s",
		DelayMultiplier:     2.0, // Double each time
		MaxAttemptsPerIP:    10,  // High threshold so we don't block
		IPWindowDuration:    "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.200"

	// Record first failure - no delay yet
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay1 := limiter.GetAuthenticationDelay(ip)
	if delay1 != 0 {
		t.Errorf("First failure should have no delay, got %v", delay1)
	}

	// Record second failure - still no delay (threshold is 2)
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay2 := limiter.GetAuthenticationDelay(ip)
	if delay2 != 100*time.Millisecond {
		t.Errorf("After 2 failures should have initial delay (100ms), got %v", delay2)
	}

	// Record third failure - delay should double
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay3 := limiter.GetAuthenticationDelay(ip)
	if delay3 != 200*time.Millisecond {
		t.Errorf("After 3 failures should have 200ms delay, got %v", delay3)
	}

	// Record fourth failure - delay should double again
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay4 := limiter.GetAuthenticationDelay(ip)
	if delay4 != 400*time.Millisecond {
		t.Errorf("After 4 failures should have 400ms delay, got %v", delay4)
	}
}

// TestAuthRateLimiterUsernameTracking tests username failure tracking (for statistics only, not blocking)
func TestAuthRateLimiterUsernameTracking(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                true,
		MaxAttemptsPerUsername: 3, // Track failures but don't block
		UsernameWindowDuration: "30m",
		MaxAttemptsPerIP:       10, // High threshold so IP doesn't block first
		IPWindowDuration:       "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	username := "target@example.com"

	// Record failures from different IPs
	ip1 := "192.168.1.100"
	ip2 := "192.168.1.101"
	ip3 := "192.168.1.102"

	limiter.RecordAuthAttempt(ctx, ip1, username, false)
	limiter.RecordAuthAttempt(ctx, ip2, username, false)
	limiter.RecordAuthAttempt(ctx, ip3, username, false)

	// Username tracking is for statistics only - should NOT block authentication
	// This prevents DoS attacks where attacker locks out legitimate users
	err = limiter.CanAttemptAuth(ctx, ip1, username)
	if err != nil {
		t.Errorf("Should not block based on username failures (prevents DoS): %v", err)
	}

	// Should allow from any IP (username blocking removed to prevent DoS)
	err = limiter.CanAttemptAuth(ctx, ip3, username)
	if err != nil {
		t.Errorf("Should not block username from any IP (prevents DoS): %v", err)
	}

	// Successful auth should clear username failures
	limiter.RecordAuthAttempt(ctx, ip1, username, true)
}

// TestAuthRateLimiterSuccessResetsFailures tests that successful auth clears failures
func TestAuthRateLimiterSuccessResetsFailures(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                true,
		MaxAttemptsPerIP:       3,
		IPBlockDuration:        "5m",
		MaxAttemptsPerUsername: 5,
		IPWindowDuration:       "15m",
		UsernameWindowDuration: "30m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"
	username := "user@example.com"

	// Record 2 failures
	limiter.RecordAuthAttempt(ctx, ip, username, false)
	limiter.RecordAuthAttempt(ctx, ip, username, false)

	// Record success - should clear failures
	limiter.RecordAuthAttempt(ctx, ip, username, true)

	// Should not have any delays or blocks now
	start := time.Now()
	err = limiter.CanAttemptAuth(ctx, ip, username)
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Should not block after success: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Should have no delay after success, got %v", elapsed)
	}
}

// TestAuthRateLimiterDisabled tests that disabled limiter allows all attempts
func TestAuthRateLimiterDisabled(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:          false, // Disabled
		MaxAttemptsPerIP: 1,     // Would block after 1 failure if enabled
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Record many failures
	for i := 0; i < 10; i++ {
		limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	}

	// Should still allow attempts since disabled
	err = limiter.CanAttemptAuth(ctx, ip, "user@example.com")
	if err != nil {
		t.Errorf("Disabled limiter should allow all attempts: %v", err)
	}
}

// TestAuthRateLimiterMaxDelayCapEnforced tests that delays don't exceed max_delay
func TestAuthRateLimiterMaxDelayCapEnforced(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:             true,
		DelayStartThreshold: 1,
		InitialDelay:        "100ms",
		MaxDelay:            "300ms", // Cap at 300ms
		DelayMultiplier:     2.0,
		MaxAttemptsPerIP:    10,
		IPWindowDuration:    "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Record failures and check delay increases then caps
	// After 1st: 100ms
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay1 := limiter.GetAuthenticationDelay(ip)
	if delay1 != 100*time.Millisecond {
		t.Errorf("After 1 failure should have 100ms delay, got %v", delay1)
	}

	// After 2nd: 200ms
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay2 := limiter.GetAuthenticationDelay(ip)
	if delay2 != 200*time.Millisecond {
		t.Errorf("After 2 failures should have 200ms delay, got %v", delay2)
	}

	// After 3rd: would be 400ms but capped to 300ms
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay3 := limiter.GetAuthenticationDelay(ip)
	if delay3 != 300*time.Millisecond {
		t.Errorf("After 3 failures should be capped at 300ms, got %v", delay3)
	}

	// After 4th: would be 600ms but still capped to 300ms
	limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	delay4 := limiter.GetAuthenticationDelay(ip)
	if delay4 != 300*time.Millisecond {
		t.Errorf("After 4 failures should still be capped at 300ms, got %v", delay4)
	}
}

// TestAuthRateLimiterGetStats tests that stats are returned correctly
func TestAuthRateLimiterGetStats(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                true,
		MaxAttemptsPerIP:       3,
		IPBlockDuration:        "5m",
		MaxAttemptsPerUsername: 5,
		IPWindowDuration:       "15m",
		UsernameWindowDuration: "30m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip1 := "192.168.1.100"
	ip2 := "192.168.1.101"

	// Record some failures
	limiter.RecordAuthAttempt(ctx, ip1, "user1@example.com", false)
	limiter.RecordAuthAttempt(ctx, ip1, "user1@example.com", false)
	limiter.RecordAuthAttempt(ctx, ip2, "user2@example.com", false)

	// Block one IP
	limiter.RecordAuthAttempt(ctx, ip1, "user1@example.com", false)

	stats := limiter.GetStats()

	// Check required fields
	if enabled, ok := stats["enabled"].(bool); !ok || !enabled {
		t.Error("Stats should show enabled=true")
	}

	if blockedIPs, ok := stats["blocked_ips"].(int); !ok || blockedIPs != 1 {
		t.Errorf("Stats should show 1 blocked IP, got %v", stats["blocked_ips"])
	}

	if trackedIPs, ok := stats["ip_failure_tracking"].(int); !ok || trackedIPs < 1 {
		t.Errorf("Stats should show at least 1 tracked IP, got %v", stats["ip_failure_tracking"])
	}
}

// TestAuthRateLimiterCleanupExpiredEntries tests that cleanup removes old entries
func TestAuthRateLimiterCleanupExpiredEntries(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:              true,
		MaxAttemptsPerIP:     3,
		IPBlockDuration:      "100ms", // Very short for testing
		IPWindowDuration:     "100ms",
		CacheCleanupInterval: "50ms", // Frequent cleanup
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Block an IP
	for i := 0; i < 3; i++ {
		limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
	}

	// Verify it's blocked
	err = limiter.CanAttemptAuth(ctx, ip, "user@example.com")
	if err == nil {
		t.Error("IP should be blocked")
	}

	// Wait for block to expire and cleanup to run
	time.Sleep(200 * time.Millisecond)

	// Should not be blocked anymore
	err = limiter.CanAttemptAuth(ctx, ip, "user@example.com")
	if err != nil {
		t.Errorf("IP should not be blocked after expiry: %v", err)
	}

	// Stats should show 0 blocked IPs after cleanup
	stats := limiter.GetStats()
	if blockedIPs, ok := stats["blocked_ips"].(int); ok && blockedIPs > 0 {
		t.Errorf("Stats should show 0 blocked IPs after cleanup, got %d", blockedIPs)
	}
}

// TestAuthRateLimiterConcurrentAccess tests thread safety
func TestAuthRateLimiterConcurrentAccess(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:          true,
		MaxAttemptsPerIP: 10,
		IPWindowDuration: "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()

	// Run concurrent auth attempts from multiple goroutines
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			ip := fmt.Sprintf("192.168.1.%d", 100+id)
			for j := 0; j < 5; j++ {
				limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
				limiter.CanAttemptAuth(ctx, ip, "user@example.com")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not crash - just verify we can get stats
	stats := limiter.GetStats()
	if stats == nil {
		t.Error("Should be able to get stats after concurrent access")
	}
}

// ========== IP+USERNAME BLOCKING TESTS (TIER 1) ==========

// TestAuthRateLimiter_IPUsernameBlocking_Basic tests basic IP+username blocking (Tier 1)
func TestAuthRateLimiter_IPUsernameBlocking_Basic(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                  true,
		MaxAttemptsPerIPUsername: 3,
		IPUsernameBlockDuration:  "5m",
		IPUsernameWindowDuration: "5m",
		MaxAttemptsPerIP:         100,
		IPBlockDuration:          "15m",
		IPWindowDuration:         "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "1.2.3.4"
	username := "test@example.com"

	// Record 3 failures
	limiter.RecordAuthAttempt(ctx, ip, username, false)
	limiter.RecordAuthAttempt(ctx, ip, username, false)
	limiter.RecordAuthAttempt(ctx, ip, username, false)

	err = limiter.CanAttemptAuth(ctx, ip, username)
	if err == nil {
		t.Error("Should block IP+username after 3 failures")
	} else {
		t.Logf("Correctly blocked: %v", err)
	}
}

// TestAuthRateLimiter_IPUsernameBlocking_IsolatesUsers tests that IP+username blocking
// only blocks specific user from specific IP, not other users from same IP
func TestAuthRateLimiter_IPUsernameBlocking_IsolatesUsers(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled: true,

		// Tier 1: IP+username blocking (fast)
		MaxAttemptsPerIPUsername: 3,
		IPUsernameBlockDuration:  "5m",
		IPUsernameWindowDuration: "5m",

		// Tier 2: High threshold
		MaxAttemptsPerIP: 100,
		IPBlockDuration:  "15m",
		IPWindowDuration: "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	sharedIP := "10.20.30.40"

	user1 := "alice@example.com"
	user2 := "bob@example.com"
	user3 := "charlie@example.com"

	// User1 fails 3 times - should be blocked
	for i := 0; i < 3; i++ {
		limiter.RecordAuthAttempt(ctx, sharedIP, user1, false)
	}

	// User1 should be blocked
	err = limiter.CanAttemptAuth(ctx, sharedIP, user1)
	if err == nil {
		t.Error("User1 should be blocked after 3 failures")
	}

	// User2 and User3 should NOT be blocked (different usernames)
	err = limiter.CanAttemptAuth(ctx, sharedIP, user2)
	if err != nil {
		t.Errorf("User2 should NOT be blocked (different username): %v", err)
	}

	err = limiter.CanAttemptAuth(ctx, sharedIP, user3)
	if err != nil {
		t.Errorf("User3 should NOT be blocked (different username): %v", err)
	}

	// User2 can successfully authenticate
	limiter.RecordAuthAttempt(ctx, sharedIP, user2, true)
	err = limiter.CanAttemptAuth(ctx, sharedIP, user2)
	if err != nil {
		t.Errorf("User2 should still work after successful auth: %v", err)
	}

	t.Logf("✓ IP+username blocking correctly isolates users on shared IP")
}

// TestAuthRateLimiter_TwoTierBlocking_DistributedAttack tests Tier 2 blocking
// catches attacks trying many usernames from same IP
func TestAuthRateLimiter_TwoTierBlocking_DistributedAttack(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled: true,

		// Tier 1: Fast IP+username blocking
		MaxAttemptsPerIPUsername: 3,
		IPUsernameBlockDuration:  "5m",
		IPUsernameWindowDuration: "5m",

		// Tier 2: Slow IP-only blocking (low threshold for testing)
		MaxAttemptsPerIP: 10, // Low threshold for testing
		IPBlockDuration:  "15m",
		IPWindowDuration: "15m",

		MaxAttemptsPerUsername: 5,
		UsernameWindowDuration: "30m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	attackerIP := "6.6.6.6"

	// Attacker tries 4 different users, 3 attempts each (= 12 total failures)
	users := []string{"user1@example.com", "user2@example.com", "user3@example.com", "user4@example.com"}
	for _, user := range users {
		for i := 0; i < 3; i++ {
			limiter.RecordAuthAttempt(ctx, attackerIP, user, false)
		}
	}

	// Each individual user should be blocked by Tier 1
	for _, user := range users {
		err := limiter.CanAttemptAuth(ctx, attackerIP, user)
		if err == nil {
			t.Errorf("User %s should be blocked by Tier 1 (IP+username)", user)
		}
	}

	// Entire IP should also be blocked by Tier 2 (exceeded 10 total failures)
	// Try with a NEW username that hasn't been tried before
	newUser := "newuser@example.com"
	err = limiter.CanAttemptAuth(ctx, attackerIP, newUser)
	if err == nil {
		t.Error("Entire IP should be blocked by Tier 2 after 12 total failures (threshold: 10)")
	}

	t.Logf("✓ Tier 2 (IP-only blocking) catches distributed attacks trying many users from same IP")
}
