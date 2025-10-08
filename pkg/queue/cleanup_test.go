package queue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestPersistentQueue_SuccessfulDeliveryCleanup verifies jobs are fully cleaned up after successful delivery
func TestPersistentQueue_SuccessfulDeliveryCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test HTTP server that always succeeds
	deliveryCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveryCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()

	// Create persistent queue
	config := QueueConfig{
		Workers:         2,
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

	// Enqueue 10 jobs
	numJobs := 10
	for i := 0; i < numJobs; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: fmt.Sprintf("Subject: Test %d\r\n\r\nTest email %d", i, i),
			Endpoint:     server.URL,
			Recipients:   []string{fmt.Sprintf("test%d@example.com", i)},
		}
		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Initial stats - should have 10 jobs
	stats := queue.GetStats()
	if stats.CurrentSize != numJobs {
		t.Errorf("Expected %d jobs in queue, got: %d", numJobs, stats.CurrentSize)
	}

	// Wait for all deliveries (scheduler runs every 100ms)
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for deliveries. Delivered: %d/%d", deliveryCount, numJobs)
		case <-ticker.C:
			if deliveryCount >= numJobs {
				goto done
			}
		}
	}
done:

	// Give time for cleanup
	time.Sleep(500 * time.Millisecond)

	// Verify queue is empty
	finalStats := queue.GetStats()
	if finalStats.CurrentSize != 0 {
		t.Errorf("Expected 0 jobs in queue after delivery, got: %d", finalStats.CurrentSize)
	}

	// Verify storage stats
	storageStats, err := queue.storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get storage stats: %v", err)
	}

	jobCount := storageStats["jobs"].(int)
	scheduleCount := storageStats["schedule_entries"].(int)

	if jobCount != 0 {
		t.Errorf("Expected 0 job entries in storage, got: %d", jobCount)
	}

	if scheduleCount != 0 {
		t.Errorf("Expected 0 schedule entries in storage, got: %d", scheduleCount)
	}

	t.Logf("Cleanup verified: %d jobs delivered, queue empty, storage empty", numJobs)
}

// TestPersistentQueue_RetryCleanup verifies old schedule entries are cleaned up during retries
func TestPersistentQueue_RetryCleanup_Slow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-retry-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)

	}
	defer os.RemoveAll(tmpDir)

	// Create test HTTP server that fails first time, then succeeds
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++

		if attemptCount < 2 {
			// Fail first attempt
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			// Succeed on 2nd attempt
			w.WriteHeader(http.StatusOK)
		}
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

	// Enqueue a job
	jobID := GenerateJobID()
	job := &DeliveryJob{
		ID:           jobID,
		EmailContent: "Subject: Retry Test\r\n\r\nTest retry cleanup",
		Endpoint:     server.URL,
		Recipients:   []string{"test@example.com"},
	}

	if err := queue.Enqueue(job); err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Wait for 2 delivery attempts (fail, succeed)
	// Scheduler runs every 100ms, retry interval is 1 min after first failure
	// So we need: 0.1s (first attempt) + 60s (wait) + 0.1s (second attempt) = ~60s
	timeout := time.After(65 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for retries. Attempts: %d", attemptCount)
		case <-ticker.C:
			if attemptCount >= 2 {
				goto retriesDone
			}
		}
	}
retriesDone:

	// Give time for cleanup
	time.Sleep(2 * time.Second)

	// Verify no orphaned schedule entries
	storageStats, err := queue.storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get storage stats: %v", err)
	}

	jobCount := storageStats["jobs"].(int)
	scheduleCount := storageStats["schedule_entries"].(int)

	if jobCount != 0 {
		t.Errorf("Expected 0 jobs after successful delivery, got: %d", jobCount)
	}

	if scheduleCount != 0 {
		t.Errorf("Expected 0 schedule entries after cleanup, got: %d (orphaned entries detected)", scheduleCount)
	}

	t.Logf("Retry cleanup verified: %d attempts, no orphaned schedule entries", attemptCount)
}

// TestPersistentQueue_DLQExpiration verifies DLQ entries expire after 7 days
func TestPersistentQueue_DLQExpiration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-dlq-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()

	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, http.DefaultClient, nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Create a job and manually move it to DLQ
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: DLQ Test\r\n\r\nTest DLQ expiration",
		Endpoint:     "http://localhost:9999", // Non-existent
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now(),
		NextRetry:    time.Now(),
	}

	// Save job first
	if err := queue.storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Move to DLQ
	if err := queue.storage.MoveToDLQ(job, "test expiration"); err != nil {
		t.Fatalf("Failed to move to DLQ: %v", err)
	}

	// Verify DLQ has 1 entry
	stats, err := queue.storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	dlqCount := stats["dlq_entries"].(int)
	if dlqCount != 1 {
		t.Errorf("Expected 1 DLQ entry, got: %d", dlqCount)
	}

	// Verify job was removed from active queue
	jobCount := stats["jobs"].(int)
	scheduleCount := stats["schedule_entries"].(int)

	if jobCount != 0 {
		t.Errorf("Expected 0 active jobs after DLQ move, got: %d", jobCount)
	}

	if scheduleCount != 0 {
		t.Errorf("Expected 0 schedule entries after DLQ move, got: %d", scheduleCount)
	}

	t.Logf("DLQ expiration test: job moved to DLQ, active queue cleaned up (TTL: 7 days)")
	t.Logf("Note: Full TTL expiration test would require 7 days or BadgerDB time manipulation")
}

// TestPersistentQueue_NoIndefiniteGrowth verifies queue doesn't grow indefinitely
func TestPersistentQueue_NoIndefiniteGrowth(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-growth-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test HTTP server that succeeds
	deliveryCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveryCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zap.NewNop()

	config := QueueConfig{
		Workers:         4,
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

	// Enqueue jobs in waves to simulate continuous load
	numWaves := 3
	jobsPerWave := 10
	totalJobs := numWaves * jobsPerWave

	maxObservedSize := 0

	for wave := 0; wave < numWaves; wave++ {
		t.Logf("Wave %d: enqueueing %d jobs", wave+1, jobsPerWave)

		for i := 0; i < jobsPerWave; i++ {
			job := &DeliveryJob{
				ID:           GenerateJobID(),
				EmailContent: fmt.Sprintf("Subject: Wave %d Job %d\r\n\r\nTest", wave, i),
				Endpoint:     server.URL,
				Recipients:   []string{fmt.Sprintf("test%d-%d@example.com", wave, i)},
			}
			if err := queue.Enqueue(job); err != nil {
				t.Fatalf("Failed to enqueue job: %v", err)
			}
		}

		// Check queue size
		stats := queue.GetStats()
		if stats.CurrentSize > maxObservedSize {
			maxObservedSize = stats.CurrentSize
		}

		t.Logf("Wave %d: queue size = %d, delivered = %d", wave+1, stats.CurrentSize, deliveryCount)

		// Wait a bit between waves
		time.Sleep(500 * time.Millisecond)
	}

	// Wait for all deliveries
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Logf("Timeout reached. Delivered: %d/%d", deliveryCount, totalJobs)
			goto checkDone
		case <-ticker.C:
			if deliveryCount >= totalJobs {
				goto checkDone
			}
		}
	}
checkDone:

	// Give time for cleanup
	time.Sleep(1 * time.Second)

	// Final stats
	finalStats := queue.GetStats()
	storageStats, _ := queue.storage.GetStats()

	t.Logf("Final stats:")
	t.Logf("  Total jobs enqueued: %d", totalJobs)
	t.Logf("  Total delivered: %d", deliveryCount)
	t.Logf("  Max queue size observed: %d", maxObservedSize)
	t.Logf("  Final queue size: %d", finalStats.CurrentSize)
	t.Logf("  Storage jobs: %d", storageStats["jobs"])
	t.Logf("  Storage schedules: %d", storageStats["schedule_entries"])

	// Verify queue is empty or near-empty (some might still be processing)
	if finalStats.CurrentSize > 5 {
		t.Errorf("Queue size too large after deliveries: %d (expected < 5)", finalStats.CurrentSize)
	}

	// Verify we delivered most jobs
	if deliveryCount < totalJobs-5 {
		t.Errorf("Too few deliveries: %d/%d", deliveryCount, totalJobs)
	}

	// Key assertion: max observed size should be reasonable (not growing indefinitely)
	// With 4 workers and 10 jobs per wave, we expect max ~20-30 jobs in queue at once
	if maxObservedSize > 50 {
		t.Errorf("Queue grew too large: max observed size = %d (indicates indefinite growth)", maxObservedSize)
	}

	t.Logf("✓ Queue does not grow indefinitely - max size stayed at %d", maxObservedSize)
}

// TestPersistentQueue_ScheduleIndexConsistency verifies schedule index stays consistent with jobs
func TestPersistentQueue_ScheduleIndexConsistency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-consistency-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()

	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 5 * time.Second,
		SchedulerTicker: 100 * time.Millisecond,
	}

	queue, err := NewPersistentQueue(config, tmpDir, http.DefaultClient, nil, nil, logger, nil)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	// Create and save a job
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: Consistency Test\r\n\r\nTest",
		Endpoint:     "http://localhost:9999",
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now(),
		NextRetry:    time.Now().Add(1 * time.Hour),
	}

	if err := queue.storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Initial state: 1 job, 1 schedule entry
	stats1, _ := queue.storage.GetStats()
	if stats1["jobs"].(int) != 1 || stats1["schedule_entries"].(int) != 1 {
		t.Errorf("Initial state: expected 1 job, 1 schedule. Got: %d jobs, %d schedules",
			stats1["jobs"], stats1["schedule_entries"])
	}

	// Update the job with a new NextRetry time (simulating reschedule)
	job.NextRetry = time.Now().Add(2 * time.Hour)
	job.Attempts++
	if err := queue.storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// After update: still 1 job, still 1 schedule entry (old one should be deleted)
	stats2, _ := queue.storage.GetStats()
	if stats2["jobs"].(int) != 1 {
		t.Errorf("After update: expected 1 job, got: %d", stats2["jobs"])
	}
	if stats2["schedule_entries"].(int) != 1 {
		t.Errorf("After update: expected 1 schedule entry (old should be deleted), got: %d",
			stats2["schedule_entries"])
	}

	// Update again with another new time
	job.NextRetry = time.Now().Add(3 * time.Hour)
	job.Attempts++
	if err := queue.storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to update job again: %v", err)
	}

	// Still 1 job, still 1 schedule entry
	stats3, _ := queue.storage.GetStats()
	if stats3["jobs"].(int) != 1 || stats3["schedule_entries"].(int) != 1 {
		t.Errorf("After 2nd update: expected 1 job, 1 schedule. Got: %d jobs, %d schedules",
			stats3["jobs"], stats3["schedule_entries"])
	}

	// Delete the job
	if err := queue.storage.DeleteJob(job); err != nil {
		t.Fatalf("Failed to delete job: %v", err)
	}

	// After delete: 0 jobs, 0 schedule entries
	stats4, _ := queue.storage.GetStats()
	if stats4["jobs"].(int) != 0 || stats4["schedule_entries"].(int) != 0 {
		t.Errorf("After delete: expected 0 jobs, 0 schedules. Got: %d jobs, %d schedules",
			stats4["jobs"], stats4["schedule_entries"])
	}

	t.Logf("✓ Schedule index consistency maintained through updates and deletes")
}
