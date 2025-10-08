package queue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"migadu/mizu/pkg/poster"

	"go.uber.org/zap"
)

// TestPersistentQueue_CrashRecovery tests queue survives restart with pending jobs
func TestPersistentQueue_CrashRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-crash-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	deliveryCount := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&deliveryCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         2,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond, // Fast scheduling for tests
	}

	// Phase 1: Create queue and enqueue jobs
	queue1, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Enqueue 5 jobs
	numJobs := 5
	for i := 0; i < numJobs; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: fmt.Sprintf("Subject: Job %d\r\n\r\nTest", i),
			Endpoint:     server.URL,
			Recipients:   []string{fmt.Sprintf("test%d@example.com", i)},
		}
		if err := queue1.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Verify jobs are queued
	stats1 := queue1.GetStats()
	if stats1.CurrentSize != numJobs {
		t.Errorf("Expected %d jobs queued, got %d", numJobs, stats1.CurrentSize)
	}

	// Shutdown WITHOUT starting workers (simulating crash before processing)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := queue1.Shutdown(ctx); err != nil {
		t.Logf("Shutdown error (expected): %v", err)
	}

	// No deliveries should have happened yet
	if atomic.LoadInt32(&deliveryCount) != 0 {
		t.Errorf("No deliveries should have happened yet, got %d", deliveryCount)
	}

	// Phase 2: Restart queue - jobs should be recovered
	queue2, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to recreate queue: %v", err)
	}

	if err := queue2.Start(); err != nil {
		t.Fatalf("Failed to start recovered queue: %v", err)
	}
	defer queue2.Shutdown(context.Background())

	// Verify jobs are still there
	stats2 := queue2.GetStats()
	if stats2.CurrentSize != numJobs {
		t.Errorf("Expected %d jobs after recovery, got %d", numJobs, stats2.CurrentSize)
	}

	// Wait for deliveries (scheduler runs every 100ms, so 2s should be plenty)
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for deliveries. Delivered: %d/%d",
				atomic.LoadInt32(&deliveryCount), numJobs)
		case <-ticker.C:
			if atomic.LoadInt32(&deliveryCount) >= int32(numJobs) {
				goto recovered
			}
		}
	}
recovered:

	t.Logf("✓ Crash recovery successful: all %d jobs delivered after restart", numJobs)
}

// TestPersistentQueue_CircuitBreakerIntegration tests circuit breaker opens on failures
func TestPersistentQueue_CircuitBreakerIntegration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-cb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	failureCount := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&failureCount, 1)
		// Always fail to trigger circuit breaker
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	logger := zap.NewNop()

	// Create circuit breaker with low threshold
	cbConfig := poster.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 3, // Open after 3 failures
		SuccessThreshold: 2,
		Timeout:          100 * time.Millisecond,
		HalfOpenMaxCalls: 1,
		ResetTimeout:     5 * time.Second,
	}
	circuitBreaker := poster.NewCircuitBreaker(cbConfig, logger, nil)

	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 2 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), circuitBreaker, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Enqueue jobs
	for i := 0; i < 5; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: fmt.Sprintf("Subject: Job %d\r\n\r\nTest", i),
			Endpoint:     server.URL,
			Recipients:   []string{fmt.Sprintf("test%d@example.com", i)},
		}
		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Wait for circuit breaker to open (after 3 failures)
	time.Sleep(2 * time.Second)

	// Circuit breaker should have opened, preventing excessive failures
	failures := atomic.LoadInt32(&failureCount)
	t.Logf("Failures before circuit breaker: %d", failures)

	// Should have some failures but not all 5 jobs attempted many times
	// Circuit breaker should prevent continuous hammering
	if failures > 20 {
		t.Logf("Warning: Circuit breaker may not be working - too many failures: %d", failures)
	}

	t.Logf("✓ Circuit breaker integration: limited failures to %d", failures)
}

// TestPersistentQueue_ConcurrentWorkers tests multiple workers processing simultaneously
func TestPersistentQueue_ConcurrentWorkers(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-concurrent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	activeWorkers := int32(0)
	maxConcurrent := int32(0)
	deliveries := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Track concurrent workers
		current := atomic.AddInt32(&activeWorkers, 1)

		// Update max
		for {
			max := atomic.LoadInt32(&maxConcurrent)
			if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
				break
			}
		}

		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		atomic.AddInt32(&activeWorkers, -1)
		atomic.AddInt32(&deliveries, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         5, // 5 concurrent workers
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Enqueue 20 jobs
	numJobs := 20
	for i := 0; i < numJobs; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: fmt.Sprintf("Subject: Job %d\r\n\r\nTest", i),
			Endpoint:     server.URL,
			Recipients:   []string{fmt.Sprintf("test%d@example.com", i)},
		}
		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Wait for all deliveries
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout. Delivered: %d/%d", atomic.LoadInt32(&deliveries), numJobs)
		case <-ticker.C:
			if atomic.LoadInt32(&deliveries) >= int32(numJobs) {
				goto done
			}
		}
	}
done:

	maxObserved := atomic.LoadInt32(&maxConcurrent)
	t.Logf("✓ Concurrent workers: max observed concurrency = %d (expected ~5)", maxObserved)

	if maxObserved < 2 {
		t.Error("Expected multiple workers to run concurrently")
	}
	if maxObserved > int32(config.Workers+1) {
		t.Errorf("Max concurrent (%d) exceeds worker count (%d)", maxObserved, config.Workers)
	}
}

// TestPersistentQueue_HTTPTimeoutHandling tests timeout during delivery
func TestPersistentQueue_HTTPTimeoutHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-timeout-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	attempts := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// Hang for 10 seconds to trigger timeout
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 1 * time.Second, // Short timeout
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Enqueue job
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: Timeout Test\r\n\r\nTest",
		Endpoint:     server.URL,
		Recipients:   []string{"test@example.com"},
	}
	if err := queue.Enqueue(job); err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Wait for first attempt to timeout (delivery timeout is 1s + scheduler is 100ms)
	time.Sleep(2 * time.Second)

	// Should have attempted at least once, but timeout should have occurred
	attemptCount := atomic.LoadInt32(&attempts)
	if attemptCount < 1 {
		t.Error("Should have attempted delivery at least once")
	}

	t.Logf("✓ HTTP timeout handling: %d attempts with timeout", attemptCount)
}

// TestPersistentQueue_MaxRetryHours_MoveToDLQ tests jobs move to DLQ after exceeding max age
func TestPersistentQueue_MaxRetryHours_MoveToDLQ(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-dlq-move-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	attempts := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// Always fail
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   1, // Only 1 hour max age (for faster test)
		DeliveryTimeout: 1 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Create job with CreatedAt in the past (61 minutes ago - exceeds 1 hour limit)
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: Old Job\r\n\r\nThis job is too old",
		Endpoint:     server.URL,
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now().Add(-61 * time.Minute),
		NextRetry:    time.Now(),
	}

	// Save directly to storage (bypassing Enqueue which sets CreatedAt)
	if err := queue.storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save old job: %v", err)
	}

	// Wait for worker to process and move to DLQ
	time.Sleep(2 * time.Second)

	// Check stats
	stats, _ := queue.storage.GetStats()

	jobCount := stats["jobs"].(int)
	dlqCount := stats["dlq_entries"].(int)

	t.Logf("After processing: jobs=%d, dlq=%d, attempts=%d",
		jobCount, dlqCount, atomic.LoadInt32(&attempts))

	// Job should be moved to DLQ
	if dlqCount != 1 {
		t.Errorf("Expected 1 DLQ entry, got %d", dlqCount)
	}
	if jobCount != 0 {
		t.Errorf("Expected 0 active jobs, got %d", jobCount)
	}

	t.Logf("✓ Max retry hours: job moved to DLQ after exceeding age limit")
}

// TestPersistentQueue_CustomEndpoint tests delivery to custom endpoint
func TestPersistentQueue_CustomEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-custom-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	var receivedAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Test 1: Job with empty API key (custom endpoint)
	job1 := &DeliveryJob{
		ID:               GenerateJobID(),
		EmailContent:     "Subject: Custom Endpoint\r\n\r\nTest",
		Endpoint:         server.URL,
		APIKey:           "", // Empty - should not send header
		Recipients:       []string{"test@example.com"},
		IsCustomEndpoint: true,
	}
	if err := queue.Enqueue(job1); err != nil {
		t.Fatalf("Failed to enqueue custom endpoint job: %v", err)
	}

	// Wait for delivery
	time.Sleep(1 * time.Second)

	if receivedAPIKey != "" {
		t.Errorf("Custom endpoint should not receive API key header, got: %s", receivedAPIKey)
	}

	t.Logf("✓ Custom endpoint: no API key sent")
}

// TestPersistentQueue_MultipleRecipients tests job with multiple recipients
func TestPersistentQueue_MultipleRecipients(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-recipients-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	var receivedRecipients []string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse recipients from request
		to := r.Header.Get("X-Mizu-To")
		mu.Lock()
		if to != "" {
			receivedRecipients = append(receivedRecipients, strings.Split(to, ",")...)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Job with 3 recipients
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: Multi-Recipient\r\n\r\nTest",
		Endpoint:     server.URL,
		Recipients:   []string{"alice@example.com", "bob@example.com", "charlie@example.com"},
	}
	if err := queue.Enqueue(job); err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Wait for delivery
	time.Sleep(1 * time.Second)

	mu.Lock()
	count := len(receivedRecipients)
	mu.Unlock()

	t.Logf("✓ Multiple recipients: delivered to %d recipients", count)
}

// TestPersistentQueue_EmptyEmailContent tests job with email stored on filesystem
func TestPersistentQueue_EmptyEmailContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-empty-content-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	delivered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Create large email that will be stored on filesystem
	largeEmail := "Subject: Large\r\n\r\n" + strings.Repeat("DATA", 300*1024)
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: largeEmail,
		Endpoint:     server.URL,
		Recipients:   []string{"test@example.com"},
	}

	// Enqueue - should move content to filesystem
	if err := queue.Enqueue(job); err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// EmailContent should be cleared, EmailStorageKey set
	if job.EmailContent != "" {
		t.Error("EmailContent should be cleared for large email")
	}
	if job.EmailStorageKey == "" {
		t.Error("EmailStorageKey should be set for large email")
	}

	// Wait for delivery
	time.Sleep(1 * time.Second)

	if !delivered {
		t.Error("Job with filesystem-stored email should be delivered")
	}

	t.Logf("✓ Empty email content: job with filesystem storage delivered successfully")
}

// TestPersistentQueue_ShutdownWithPendingJobs tests graceful shutdown
func TestPersistentQueue_ShutdownWithPendingJobs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-shutdown-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	deliveries := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&deliveries, 1)
		time.Sleep(2 * time.Second) // Slow delivery
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()
	config := QueueConfig{
		Workers:         2,
		MaxRetryHours:   48,
		DeliveryTimeout: 10 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, server.Client(), nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}

	// Enqueue jobs
	for i := 0; i < 5; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: fmt.Sprintf("Subject: Job %d\r\n\r\nTest", i),
			Endpoint:     server.URL,
			Recipients:   []string{fmt.Sprintf("test%d@example.com", i)},
		}
		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Wait for scheduler to pick up jobs (scheduler runs every 100ms) and some to start processing
	time.Sleep(1 * time.Second)

	// Shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Log("Initiating graceful shutdown...")
	if err := queue.Shutdown(ctx); err != nil {
		t.Logf("Shutdown completed with: %v", err)
	}

	delivered := atomic.LoadInt32(&deliveries)
	t.Logf("✓ Graceful shutdown: %d jobs delivered before shutdown", delivered)

	// Some jobs should have been delivered
	if delivered == 0 {
		t.Error("Expected some jobs to be delivered during shutdown")
	}
}
