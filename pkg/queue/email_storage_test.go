package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestEmailStorage_SaveAndLoad tests basic save/load functionality
func TestEmailStorage_SaveAndLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-storage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	jobID := "test-job-123"
	emailContent := []byte("Subject: Test Email\r\n\r\nThis is a test email body")

	// Save email
	storageKey, err := storage.Save(jobID, emailContent)
	if err != nil {
		t.Fatalf("Failed to save email: %v", err)
	}

	if storageKey == "" {
		t.Error("Storage key should not be empty")
	}

	// Verify file exists
	if !storage.Exists(storageKey) {
		t.Error("Email file should exist")
	}

	// Load email back
	loaded, err := storage.Load(storageKey)
	if err != nil {
		t.Fatalf("Failed to load email: %v", err)
	}

	if string(loaded) != string(emailContent) {
		t.Errorf("Loaded content doesn't match original.\nExpected: %s\nGot: %s",
			emailContent, loaded)
	}
}

// TestEmailStorage_Delete tests email deletion
func TestEmailStorage_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-delete-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	emailContent := []byte("Test content for deletion")
	storageKey, err := storage.Save("test-job", emailContent)
	if err != nil {
		t.Fatalf("Failed to save email: %v", err)
	}

	// Verify it exists
	if !storage.Exists(storageKey) {
		t.Error("Email should exist before deletion")
	}

	// Delete
	if err := storage.Delete(storageKey); err != nil {
		t.Fatalf("Failed to delete email: %v", err)
	}

	// Verify it's gone
	if storage.Exists(storageKey) {
		t.Error("Email should not exist after deletion")
	}

	// Loading should fail
	if _, err := storage.Load(storageKey); err == nil {
		t.Error("Loading deleted email should return error")
	}
}

// TestEmailStorage_Deduplication tests content-addressable storage
func TestEmailStorage_Deduplication(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-dedup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Same content, different jobs
	emailContent := []byte("Subject: Identical Email\r\n\r\nSame content")

	key1, err := storage.Save("job-1", emailContent)
	if err != nil {
		t.Fatalf("Failed to save first email: %v", err)
	}

	key2, err := storage.Save("job-2", emailContent)
	if err != nil {
		t.Fatalf("Failed to save second email: %v", err)
	}

	// Keys should be identical (same content hash)
	if key1 != key2 {
		t.Errorf("Same content should produce same storage key.\nKey1: %s\nKey2: %s",
			key1, key2)
	}

	// Verify only one file exists on disk
	shard := key1[:2]
	shardDir := filepath.Join(tmpDir, shard)
	files, err := os.ReadDir(shardDir)
	if err != nil {
		t.Fatalf("Failed to read shard dir: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("Expected 1 file (deduplicated), got %d files", len(files))
	}
}

// TestEmailStorage_Sharding tests directory sharding (256 subdirectories)
func TestEmailStorage_Sharding(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-shard-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Create multiple emails with different content to hit different shards
	shards := make(map[string]bool)

	for i := 0; i < 100; i++ {
		content := []byte(strings.Repeat("A", i) + "content")
		key, err := storage.Save("job", content)
		if err != nil {
			t.Fatalf("Failed to save email: %v", err)
		}

		// Extract shard (first 2 chars of hash)
		shard := key[:2]
		shards[shard] = true

		// Verify file is in correct shard directory
		expectedPath := filepath.Join(tmpDir, shard, key+".eml")
		if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
			t.Errorf("Email should be in shard %s: %s", shard, expectedPath)
		}
	}

	// We should have hit multiple shards (not all in one directory)
	if len(shards) < 10 {
		t.Logf("Warning: Only hit %d shards out of 100 emails (expected more distribution)",
			len(shards))
	}

	t.Logf("Created 100 emails across %d shards", len(shards))
}

// TestEmailStorage_InvalidStorageKey tests handling of invalid keys
func TestEmailStorage_InvalidStorageKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-invalid-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	invalidKeys := []string{
		"",        // Empty
		"a",       // Too short
		"ab",      // Only 2 chars (no filename)
		"invalid", // Doesn't exist
	}

	for _, key := range invalidKeys {
		t.Run("key="+key, func(t *testing.T) {
			// Exists should return false
			if storage.Exists(key) {
				t.Errorf("Exists should return false for invalid key: %s", key)
			}

			// Load should return error
			if _, err := storage.Load(key); err == nil && key != "" {
				t.Errorf("Load should return error for invalid key: %s", key)
			}

			// Delete should handle gracefully
			_ = storage.Delete(key) // Should not panic
		})
	}
}

// TestEmailStorage_CleanupOrphaned tests orphaned file cleanup
func TestEmailStorage_CleanupOrphaned(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Create 3 emails
	var keys []string
	for i := 0; i < 3; i++ {
		content := []byte(strings.Repeat("A", i) + "email")
		key, err := storage.Save("job", content)
		if err != nil {
			t.Fatalf("Failed to save email: %v", err)
		}
		keys = append(keys, key)
	}

	// Mark only first 2 as active
	activeKeys := map[string]bool{
		keys[0]: true,
		keys[1]: true,
		// keys[2] is orphaned
	}

	// Run cleanup
	cleaned, err := storage.CleanupOrphaned(activeKeys)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	if cleaned != 1 {
		t.Errorf("Expected 1 orphan cleaned, got %d", cleaned)
	}

	// Verify active files still exist
	if !storage.Exists(keys[0]) {
		t.Error("Active email 0 should still exist")
	}
	if !storage.Exists(keys[1]) {
		t.Error("Active email 1 should still exist")
	}

	// Verify orphan is gone
	if storage.Exists(keys[2]) {
		t.Error("Orphaned email should be deleted")
	}
}

// TestEmailStorage_CleanupOrphaned_EmptyActiveSet tests cleanup with no active keys
func TestEmailStorage_CleanupOrphaned_EmptyActiveSet(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-cleanup-empty-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Create 5 emails
	for i := 0; i < 5; i++ {
		content := []byte(strings.Repeat("B", i) + "email")
		_, err := storage.Save("job", content)
		if err != nil {
			t.Fatalf("Failed to save email: %v", err)
		}
	}

	// No active keys - everything is orphaned
	activeKeys := map[string]bool{}

	// Run cleanup
	cleaned, err := storage.CleanupOrphaned(activeKeys)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	if cleaned != 5 {
		t.Errorf("Expected 5 orphans cleaned, got %d", cleaned)
	}

	// Verify all files are gone
	stats, _ := storage.GetStats()
	if stats["total_files"].(int) != 0 {
		t.Errorf("Expected 0 files after cleanup, got %d", stats["total_files"])
	}
}

// TestEmailStorage_GetStats tests statistics generation
func TestEmailStorage_GetStats(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-stats-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Empty storage
	stats0, err := storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats0["total_files"].(int) != 0 {
		t.Error("Empty storage should have 0 files")
	}
	if stats0["total_bytes"].(int64) != 0 {
		t.Error("Empty storage should have 0 bytes")
	}

	// Add some emails of known sizes
	email1 := []byte(strings.Repeat("A", 1000)) // 1KB
	email2 := []byte(strings.Repeat("B", 2000)) // 2KB
	email3 := []byte(strings.Repeat("C", 3000)) // 3KB

	for i, content := range [][]byte{email1, email2, email3} {
		_, err := storage.Save("job", content)
		if err != nil {
			t.Fatalf("Failed to save email %d: %v", i, err)
		}
	}

	// Get stats
	stats, err := storage.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	// Should have 3 files
	if stats["total_files"].(int) != 3 {
		t.Errorf("Expected 3 files, got %d", stats["total_files"])
	}

	// Total bytes should be sum of content sizes
	expectedBytes := int64(len(email1) + len(email2) + len(email3))
	if stats["total_bytes"].(int64) != expectedBytes {
		t.Errorf("Expected %d bytes, got %d", expectedBytes, stats["total_bytes"])
	}

	// Check MB calculation
	expectedMB := float64(expectedBytes) / (1024 * 1024)
	actualMB := stats["total_mb"].(float64)
	if actualMB != expectedMB {
		t.Errorf("Expected %.6f MB, got %.6f", expectedMB, actualMB)
	}

	t.Logf("Stats: %d files, %d bytes, %.2f MB",
		stats["total_files"], stats["total_bytes"], stats["total_mb"])
}

// TestEmailStorage_ConcurrentSave tests concurrent save operations
func TestEmailStorage_ConcurrentSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-concurrent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Launch multiple goroutines to save emails concurrently
	numWorkers := 10
	emailsPerWorker := 5
	done := make(chan bool, numWorkers)
	keys := make(chan string, numWorkers*emailsPerWorker)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			for i := 0; i < emailsPerWorker; i++ {
				content := []byte(strings.Repeat("X", workerID*100+i) + "content")
				key, err := storage.Save("job", content)
				if err != nil {
					t.Errorf("Worker %d: failed to save email: %v", workerID, err)
				} else {
					keys <- key
				}
			}
			done <- true
		}(w)
	}

	// Wait for all workers
	for w := 0; w < numWorkers; w++ {
		<-done
	}
	close(keys)

	// Verify all keys are valid
	keyCount := 0
	for key := range keys {
		if !storage.Exists(key) {
			t.Errorf("Saved email should exist: %s", key)
		}
		keyCount++
	}

	expectedCount := numWorkers * emailsPerWorker
	if keyCount != expectedCount {
		t.Errorf("Expected %d emails, got %d", expectedCount, keyCount)
	}
}

// TestEmailStorage_LargeEmail tests storing very large email
func TestEmailStorage_LargeEmail(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-large-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	// Create 10MB email
	largeContent := []byte(strings.Repeat("LARGE", 2*1024*1024)) // 10MB
	t.Logf("Creating email of size: %d bytes (%.2f MB)",
		len(largeContent), float64(len(largeContent))/(1024*1024))

	key, err := storage.Save("large-job", largeContent)
	if err != nil {
		t.Fatalf("Failed to save large email: %v", err)
	}

	// Verify it exists
	if !storage.Exists(key) {
		t.Error("Large email should exist")
	}

	// Load it back
	loaded, err := storage.Load(key)
	if err != nil {
		t.Fatalf("Failed to load large email: %v", err)
	}

	if len(loaded) != len(largeContent) {
		t.Errorf("Size mismatch: expected %d, got %d", len(largeContent), len(loaded))
	}

	// Verify content matches
	if string(loaded) != string(largeContent) {
		t.Error("Large email content doesn't match")
	}

	// Clean up
	if err := storage.Delete(key); err != nil {
		t.Errorf("Failed to delete large email: %v", err)
	}
}

// TestEmailStorage_DirectoryCreation tests automatic directory creation
func TestEmailStorage_DirectoryCreation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-dir-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create storage in nested directory that doesn't exist yet
	nestedDir := filepath.Join(tmpDir, "nested", "deep", "storage")

	storage, err := NewEmailStorage(nestedDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage in nested dir: %v", err)
	}

	// Directory should have been created
	if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
		t.Error("Nested directory should have been created")
	}

	// Should be able to save email
	content := []byte("Test email in nested directory")
	key, err := storage.Save("job", content)
	if err != nil {
		t.Fatalf("Failed to save email in nested dir: %v", err)
	}

	if !storage.Exists(key) {
		t.Error("Email should exist in nested directory")
	}
}

// TestEmailStorage_DoubleDelete tests deleting same file twice
func TestEmailStorage_DoubleDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "email-double-delete-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewEmailStorage(tmpDir, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create email storage: %v", err)
	}

	content := []byte("Test email for double delete")
	key, err := storage.Save("job", content)
	if err != nil {
		t.Fatalf("Failed to save email: %v", err)
	}

	// First delete - should succeed
	if err := storage.Delete(key); err != nil {
		t.Fatalf("First delete failed: %v", err)
	}

	// Second delete - should not error (file already gone)
	if err := storage.Delete(key); err != nil {
		t.Errorf("Second delete should not error, got: %v", err)
	}
}
