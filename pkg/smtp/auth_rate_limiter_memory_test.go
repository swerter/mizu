package smtp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/logging"
)

// TestIPUsernameSizeLimit tests that max_ip_username_entries enforces size limit with LRU eviction
func TestIPUsernameSizeLimit(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                  true,
		MaxAttemptsPerIPUsername: 3,
		IPUsernameBlockDuration:  "5m",
		IPUsernameWindowDuration: "10m",
		CacheCleanupInterval:     "1h", // Don't cleanup during test
		MaxIPUsernameEntries:     5,    // Small limit for testing
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Add 5 entries (at limit)
	for i := 1; i <= 5; i++ {
		username := fmt.Sprintf("user%d@example.com", i)
		// First failure - creates tracking entry
		limiter.RecordAuthAttempt(ctx, ip, username, false)
		time.Sleep(10 * time.Millisecond) // Ensure FirstFailure times are different
	}

	// Verify we have exactly 5 entries
	limiter.ipUsernameMu.RLock()
	entriesCount := len(limiter.blockedIPUsernames)
	limiter.ipUsernameMu.RUnlock()

	if entriesCount != 5 {
		t.Fatalf("Expected 5 entries, got %d", entriesCount)
	}

	// Add 6th entry - should evict oldest (user1)
	time.Sleep(10 * time.Millisecond)
	limiter.RecordAuthAttempt(ctx, ip, "user6@example.com", false)

	// Still should have 5 entries
	limiter.ipUsernameMu.RLock()
	entriesCount = len(limiter.blockedIPUsernames)
	user1Key := ip + "|user1@example.com"
	user6Key := ip + "|user6@example.com"
	_, user1Exists := limiter.blockedIPUsernames[user1Key]
	_, user6Exists := limiter.blockedIPUsernames[user6Key]
	limiter.ipUsernameMu.RUnlock()

	if entriesCount != 5 {
		t.Errorf("Expected 5 entries after eviction, got %d", entriesCount)
	}

	if user1Exists {
		t.Errorf("user1 should have been evicted (oldest entry)")
	}

	if !user6Exists {
		t.Errorf("user6 should exist (newest entry)")
	}

	// Verify user2-user5 still exist
	limiter.ipUsernameMu.RLock()
	for i := 2; i <= 5; i++ {
		key := fmt.Sprintf("%s|user%d@example.com", ip, i)
		if _, exists := limiter.blockedIPUsernames[key]; !exists {
			t.Errorf("user%d should still exist", i)
		}
	}
	limiter.ipUsernameMu.RUnlock()

	t.Logf("✓ IP+username LRU eviction working correctly")
}

// TestIPSizeLimit tests that max_ip_entries enforces size limit with LRU eviction
func TestIPSizeLimit(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:              true,
		MaxAttemptsPerIP:     10,
		IPBlockDuration:      "30m",
		IPWindowDuration:     "30m",
		CacheCleanupInterval: "1h", // Don't cleanup during test
		MaxIPEntries:         3,    // Small limit for testing
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()

	// Add 3 IPs (at limit)
	for i := 1; i <= 3; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		limiter.RecordAuthAttempt(ctx, ip, "user@example.com", false)
		time.Sleep(10 * time.Millisecond) // Ensure FirstFailure times are different
	}

	// Verify we have exactly 3 entries
	limiter.ipMu.RLock()
	entriesCount := len(limiter.ipFailureCounts)
	limiter.ipMu.RUnlock()

	if entriesCount != 3 {
		t.Fatalf("Expected 3 IP entries, got %d", entriesCount)
	}

	// Add 4th IP - should evict oldest (192.168.1.1)
	time.Sleep(10 * time.Millisecond)
	limiter.RecordAuthAttempt(ctx, "192.168.1.4", "user@example.com", false)

	// Still should have 3 entries
	limiter.ipMu.RLock()
	entriesCount = len(limiter.ipFailureCounts)
	_, ip1Exists := limiter.ipFailureCounts["192.168.1.1"]
	_, ip4Exists := limiter.ipFailureCounts["192.168.1.4"]
	limiter.ipMu.RUnlock()

	if entriesCount != 3 {
		t.Errorf("Expected 3 entries after eviction, got %d", entriesCount)
	}

	if ip1Exists {
		t.Errorf("192.168.1.1 should have been evicted (oldest entry)")
	}

	if !ip4Exists {
		t.Errorf("192.168.1.4 should exist (newest entry)")
	}

	// Verify 192.168.1.2 and 192.168.1.3 still exist
	limiter.ipMu.RLock()
	if _, exists := limiter.ipFailureCounts["192.168.1.2"]; !exists {
		t.Errorf("192.168.1.2 should still exist")
	}
	if _, exists := limiter.ipFailureCounts["192.168.1.3"]; !exists {
		t.Errorf("192.168.1.3 should still exist")
	}
	limiter.ipMu.RUnlock()

	t.Logf("✓ IP LRU eviction working correctly")
}

// TestUsernameSizeLimit tests that max_username_entries enforces size limit with LRU eviction
func TestUsernameSizeLimit(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                true,
		MaxAttemptsPerUsername: 10,
		UsernameWindowDuration: "60m",
		CacheCleanupInterval:   "1h", // Don't cleanup during test
		MaxUsernameEntries:     4,    // Small limit for testing
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()

	// Add 3 usernames (below limit)
	for i := 1; i <= 3; i++ {
		username := fmt.Sprintf("user%d@example.com", i)
		ip := fmt.Sprintf("192.168.1.%d", i)
		limiter.RecordAuthAttempt(ctx, ip, username, false)
		time.Sleep(10 * time.Millisecond) // Ensure FirstFailure times are different
	}

	// Verify we have exactly 3 entries (below limit, no eviction yet)
	limiter.usernameMu.RLock()
	entriesCount := len(limiter.usernameFailureCounts)
	limiter.usernameMu.RUnlock()

	if entriesCount != 3 {
		t.Fatalf("Expected 3 username entries, got %d", entriesCount)
	}

	// Add 4th username (exactly at limit) - no eviction yet
	time.Sleep(10 * time.Millisecond)
	limiter.RecordAuthAttempt(ctx, "192.168.1.4", "user4@example.com", false)

	// Should have 4 entries now (at capacity, but no eviction yet)
	limiter.usernameMu.RLock()
	entriesCount = len(limiter.usernameFailureCounts)
	limiter.usernameMu.RUnlock()

	if entriesCount != 4 {
		t.Fatalf("Expected 4 username entries at capacity, got %d", entriesCount)
	}

	// Add 5th username - should trigger proactive eviction (20% = 1 entry)
	time.Sleep(10 * time.Millisecond)
	limiter.RecordAuthAttempt(ctx, "192.168.1.5", "user5@example.com", false)

	// Should have 4 entries after proactive eviction (evicted 1, added 1)
	limiter.usernameMu.RLock()
	entriesCount = len(limiter.usernameFailureCounts)
	_, user1Exists := limiter.usernameFailureCounts["user1@example.com"]
	_, user5Exists := limiter.usernameFailureCounts["user5@example.com"]
	limiter.usernameMu.RUnlock()

	if entriesCount != 4 {
		t.Errorf("Expected 4 entries after proactive eviction, got %d", entriesCount)
	}

	if user1Exists {
		t.Errorf("user1@example.com should have been evicted (oldest entry)")
	}

	if !user5Exists {
		t.Errorf("user5@example.com should exist (newest entry)")
	}

	// Verify user2-user4 still exist
	limiter.usernameMu.RLock()
	for i := 2; i <= 4; i++ {
		username := fmt.Sprintf("user%d@example.com", i)
		if _, exists := limiter.usernameFailureCounts[username]; !exists {
			t.Errorf("%s should still exist", username)
		}
	}
	limiter.usernameMu.RUnlock()

	t.Logf("✓ Username proactive eviction working correctly (20%% eviction)")
}

// TestSizeLimitDisabled tests that setting limits to 0 disables size-based eviction
func TestSizeLimitDisabled(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                  true,
		MaxAttemptsPerIPUsername: 3,
		IPUsernameBlockDuration:  "5m",
		IPUsernameWindowDuration: "10m",
		CacheCleanupInterval:     "1h",
		MaxIPUsernameEntries:     0, // Disabled - unlimited
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	ip := "192.168.1.100"

	// Add 100 entries (should not be limited)
	for i := 1; i <= 100; i++ {
		username := fmt.Sprintf("user%d@example.com", i)
		limiter.RecordAuthAttempt(ctx, ip, username, false)
	}

	// Verify we have all 100 entries
	limiter.ipUsernameMu.RLock()
	entriesCount := len(limiter.blockedIPUsernames)
	limiter.ipUsernameMu.RUnlock()

	if entriesCount != 100 {
		t.Errorf("Expected 100 entries (unlimited), got %d", entriesCount)
	}

	t.Logf("✓ Unlimited mode (0) working correctly - no eviction")
}

// TestAuthRateLimiter_DoSPrevention verifies that username tracking does not block
// legitimate users with correct passwords, preventing DoS attacks
func TestAuthRateLimiter_DoSPrevention(t *testing.T) {
	cfg := config.ServerAuthRateLimitConfig{
		Enabled:                true,
		MaxAttemptsPerUsername: 3, // Track failures but don't block
		UsernameWindowDuration: "30m",
		MaxAttemptsPerIP:       10, // High threshold so IP doesn't block
		IPWindowDuration:       "15m",
	}

	logger := logging.NewTestLogger()
	limiter, err := NewAuthRateLimiter(cfg, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()
	username := "victim@example.com"

	// Simulate attacker attempting wrong passwords from different IPs
	attacker1 := "1.2.3.4"
	attacker2 := "5.6.7.8"
	attacker3 := "9.10.11.12"

	limiter.RecordAuthAttempt(ctx, attacker1, username, false)
	limiter.RecordAuthAttempt(ctx, attacker2, username, false)
	limiter.RecordAuthAttempt(ctx, attacker3, username, false)

	// CRITICAL: Legitimate user with CORRECT password should NOT be blocked
	// even though username has 3 failures from attackers
	legitimateUser := "192.168.1.100"
	err = limiter.CanAttemptAuth(ctx, legitimateUser, username)
	if err != nil {
		t.Errorf("Legitimate user should NOT be blocked by username failures: %v", err)
		t.Error("This would allow DoS attacks where attacker locks out legitimate users")
	}

	// Simulate successful authentication with correct password
	limiter.RecordAuthAttempt(ctx, legitimateUser, username, true)

	t.Logf("✓ DoS prevention working: legitimate users not blocked by username tracking")
}
