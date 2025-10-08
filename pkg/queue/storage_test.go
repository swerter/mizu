package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestStorage_SaveAndLoadJob tests basic job persistence
func TestStorage_SaveAndLoadJob(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Create and save a job
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Subject: Test\r\n\r\nTest email",
		Endpoint:     "https://example.com/webhook",
		Recipients:   []string{"test@example.com"},
		APIKey:       "test-key",
		CreatedAt:    time.Now(),
		NextRetry:    time.Now().Add(5 * time.Minute),
		Attempts:     2,
		IsForwarding: false,
	}

	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Load the job back
	loaded, err := storage.GetJob(job.ID)
	if err != nil {
		t.Fatalf("Failed to load job: %v", err)
	}

	// Verify all fields
	if loaded.ID != job.ID {
		t.Errorf("ID mismatch: expected %s, got %s", job.ID, loaded.ID)
	}
	if loaded.EmailContent != job.EmailContent {
		t.Errorf("EmailContent mismatch")
	}
	if loaded.Endpoint != job.Endpoint {
		t.Errorf("Endpoint mismatch")
	}
	if len(loaded.Recipients) != len(job.Recipients) {
		t.Errorf("Recipients length mismatch")
	}
	if loaded.Attempts != job.Attempts {
		t.Errorf("Attempts mismatch: expected %d, got %d", job.Attempts, loaded.Attempts)
	}
	if loaded.IsForwarding != job.IsForwarding {
		t.Errorf("IsForwarding mismatch")
	}
}

// TestStorage_SaveJob_UpdateScheduleEntry tests that old schedule entries are deleted
func TestStorage_SaveJob_UpdateScheduleEntry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-schedule-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Test",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now(),
		NextRetry:    time.Now().Add(1 * time.Hour),
		Attempts:     1,
	}

	// Save job first time
	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Verify 1 schedule entry
	stats1, _ := storage.GetStats()
	if stats1["schedule_entries"].(int) != 1 {
		t.Errorf("Expected 1 schedule entry, got %d", stats1["schedule_entries"])
	}

	// Update job with new NextRetry time
	job.NextRetry = time.Now().Add(2 * time.Hour)
	job.Attempts = 2

	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// Still only 1 schedule entry (old one deleted)
	stats2, _ := storage.GetStats()
	if stats2["schedule_entries"].(int) != 1 {
		t.Errorf("Expected 1 schedule entry after update (old deleted), got %d",
			stats2["schedule_entries"])
	}
}

// TestStorage_DeleteJob tests job deletion with cleanup
func TestStorage_DeleteJob(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-delete-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Test",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now(),
		NextRetry:    time.Now().Add(1 * time.Hour),
	}

	// Save job
	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Verify it exists
	stats1, _ := storage.GetStats()
	if stats1["jobs"].(int) != 1 {
		t.Error("Job should exist")
	}
	if stats1["schedule_entries"].(int) != 1 {
		t.Error("Schedule entry should exist")
	}

	// Delete job
	if err := storage.DeleteJob(job); err != nil {
		t.Fatalf("Failed to delete job: %v", err)
	}

	// Verify both job and schedule entry are gone
	stats2, _ := storage.GetStats()
	if stats2["jobs"].(int) != 0 {
		t.Error("Job should be deleted")
	}
	if stats2["schedule_entries"].(int) != 0 {
		t.Error("Schedule entry should be deleted")
	}

	// Loading should fail
	if _, err := storage.GetJob(job.ID); err == nil {
		t.Error("Loading deleted job should fail")
	}
}

// TestStorage_GetDueJobs tests retrieving jobs ready for processing
func TestStorage_GetDueJobs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-due-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	now := time.Now()

	// Create jobs with different NextRetry times
	pastJob := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Past job",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test1@example.com"},
		CreatedAt:    now.Add(-1 * time.Hour),
		NextRetry:    now.Add(-10 * time.Minute), // In the past - ready
	}

	nowJob := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Now job",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test2@example.com"},
		CreatedAt:    now.Add(-30 * time.Minute),
		NextRetry:    now, // Exactly now - ready
	}

	futureJob := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Future job",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test3@example.com"},
		CreatedAt:    now.Add(-5 * time.Minute),
		NextRetry:    now.Add(10 * time.Minute), // In the future - not ready
	}

	// Save all jobs
	for _, job := range []*DeliveryJob{pastJob, nowJob, futureJob} {
		if err := storage.SaveJob(job); err != nil {
			t.Fatalf("Failed to save job: %v", err)
		}
	}

	// Get jobs due now (should get past and now, but not future)
	dueJobs, err := storage.GetDueJobs(100)
	if err != nil {
		t.Fatalf("Failed to get due jobs: %v", err)
	}

	if len(dueJobs) != 2 {
		t.Errorf("Expected 2 due jobs, got %d", len(dueJobs))
	}

	// Verify we got the right jobs
	foundPast := false
	foundNow := false
	foundFuture := false

	for _, job := range dueJobs {
		if job.ID == pastJob.ID {
			foundPast = true
		}
		if job.ID == nowJob.ID {
			foundNow = true
		}
		if job.ID == futureJob.ID {
			foundFuture = true
		}
	}

	if !foundPast {
		t.Error("Should have found past job")
	}
	if !foundNow {
		t.Error("Should have found now job")
	}
	if foundFuture {
		t.Error("Should NOT have found future job")
	}
}

// TestStorage_GetDueJobs_Limit tests the limit parameter
func TestStorage_GetDueJobs_Limit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-limit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	now := time.Now()

	// Create 10 jobs all due now
	for i := 0; i < 10; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: "Test",
			Endpoint:     "https://example.com",
			Recipients:   []string{"test@example.com"},
			CreatedAt:    now,
			NextRetry:    now.Add(-1 * time.Minute), // All ready
		}
		if err := storage.SaveJob(job); err != nil {
			t.Fatalf("Failed to save job: %v", err)
		}
	}

	// Request only 5 jobs
	dueJobs, err := storage.GetDueJobs(5)
	if err != nil {
		t.Fatalf("Failed to get due jobs: %v", err)
	}

	if len(dueJobs) != 5 {
		t.Errorf("Expected 5 jobs (limit), got %d", len(dueJobs))
	}
}

// TestStorage_MoveToDLQ tests moving failed jobs to DLQ
func TestStorage_MoveToDLQ(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-dlq-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: "Failed job",
		Endpoint:     "https://example.com",
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now().Add(-48 * time.Hour),
		NextRetry:    time.Now(),
		Attempts:     39,
	}

	// Save job
	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save job: %v", err)
	}

	// Initial state
	stats1, _ := storage.GetStats()
	if stats1["jobs"].(int) != 1 {
		t.Error("Should have 1 active job")
	}
	if stats1["dlq_entries"].(int) != 0 {
		t.Error("Should have 0 DLQ entries")
	}

	// Move to DLQ
	reason := "Exceeded max retry hours (48h)"
	if err := storage.MoveToDLQ(job, reason); err != nil {
		t.Fatalf("Failed to move to DLQ: %v", err)
	}

	// After move
	stats2, _ := storage.GetStats()
	if stats2["jobs"].(int) != 0 {
		t.Error("Should have 0 active jobs after DLQ move")
	}
	if stats2["schedule_entries"].(int) != 0 {
		t.Error("Should have 0 schedule entries after DLQ move")
	}
	if stats2["dlq_entries"].(int) != 1 {
		t.Error("Should have 1 DLQ entry")
	}

	// Loading active job should fail
	if _, err := storage.GetJob(job.ID); err == nil {
		t.Error("Loading active job should fail after DLQ move")
	}
}

// TestStorage_GetStats tests statistics generation
func TestStorage_GetStats(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-stats-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Empty storage
	stats0, err := storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats0["jobs"].(int) != 0 || stats0["schedule_entries"].(int) != 0 {
		t.Error("Empty storage should have 0 jobs and 0 schedule entries")
	}

	// Add 3 jobs
	for i := 0; i < 3; i++ {
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: "Test",
			Endpoint:     "https://example.com",
			Recipients:   []string{"test@example.com"},
			CreatedAt:    time.Now(),
			NextRetry:    time.Now().Add(1 * time.Hour),
		}
		if err := storage.SaveJob(job); err != nil {
			t.Fatalf("Failed to save job: %v", err)
		}
	}

	// Check stats
	stats1, err := storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats1["jobs"].(int) != 3 {
		t.Errorf("Expected 3 jobs, got %d", stats1["jobs"])
	}
	if stats1["schedule_entries"].(int) != 3 {
		t.Errorf("Expected 3 schedule entries, got %d", stats1["schedule_entries"])
	}
}

// TestStorage_LargeEmail tests storing job with large email on filesystem
func TestStorage_LargeEmail(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-large-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Create email storage
	emailStorageDir := filepath.Join(tmpDir, "emails")
	emailStorage, err := NewEmailStorage(emailStorageDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Create large email (>1MB)
	largeContent := "Subject: Large\r\n\r\n" + strings.Repeat("A", 1<<20+1000)
	job := &DeliveryJob{
		ID:           GenerateJobID(),
		EmailContent: largeContent,
		Endpoint:     "https://example.com",
		Recipients:   []string{"test@example.com"},
		CreatedAt:    time.Now(),
		NextRetry:    time.Now(),
	}

	// Use SetEmailContent to handle large email
	if err := job.SetEmailContent(largeContent, emailStorage); err != nil {
		t.Fatalf("Failed to set email content: %v", err)
	}

	// Email content should be moved to filesystem
	if job.EmailStorageKey == "" {
		t.Error("Large email should have storage key")
	}
	if job.EmailContent != "" {
		t.Error("EmailContent should be cleared for large email")
	}

	// Save job to storage
	if err := storage.SaveJob(job); err != nil {
		t.Fatalf("Failed to save large job: %v", err)
	}

	// Load job back
	loaded, err := storage.GetJob(job.ID)
	if err != nil {
		t.Fatalf("Failed to load job: %v", err)
	}

	// Loaded job should still have empty content (requires separate email load)
	if loaded.EmailContent != "" {
		t.Error("Loaded job should have empty EmailContent")
	}
	if loaded.EmailStorageKey == "" {
		t.Error("Loaded job should have EmailStorageKey")
	}
}

// TestStorage_ConcurrentAccess tests multiple goroutines accessing storage
func TestStorage_ConcurrentAccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-concurrent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Launch multiple goroutines to save jobs concurrently
	numWorkers := 10
	jobsPerWorker := 5
	done := make(chan bool, numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			for i := 0; i < jobsPerWorker; i++ {
				job := &DeliveryJob{
					ID:           GenerateJobID(),
					EmailContent: "Test",
					Endpoint:     "https://example.com",
					Recipients:   []string{"test@example.com"},
					CreatedAt:    time.Now(),
					NextRetry:    time.Now().Add(1 * time.Hour),
				}
				if err := storage.SaveJob(job); err != nil {
					t.Errorf("Worker %d: failed to save job: %v", workerID, err)
				}
			}
			done <- true
		}(w)
	}

	// Wait for all workers
	for w := 0; w < numWorkers; w++ {
		<-done
	}

	// Verify all jobs were saved
	stats, _ := storage.GetStats()
	expectedJobs := numWorkers * jobsPerWorker
	if stats["jobs"].(int) != expectedJobs {
		t.Errorf("Expected %d jobs, got %d", expectedJobs, stats["jobs"])
	}
}

// TestStorage_InvalidJobID tests loading non-existent job
func TestStorage_InvalidJobID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-invalid-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(StorageConfig{
		DataDir: tmpDir,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Try to load non-existent job
	_, err = storage.GetJob("non-existent-job-id")
	if err == nil {
		t.Error("Loading non-existent job should return error")
	}
}
