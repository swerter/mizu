package queue

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestPersistentQueue_HybridStorage tests that large emails (>1MB) are stored on filesystem
func TestPersistentQueue_HybridStorage(t *testing.T) {
	// Create temp directory for queue data
	tmpDir, err := os.MkdirTemp("", "queue-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test HTTP server
	delivered := make(chan bool, 10)
	var receivedContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed to read request body: %v", err)
		}
		receivedContent = string(body)
		w.WriteHeader(http.StatusOK)
		delivered <- true
	}))
	defer server.Close()

	logger := zap.NewNop()

	// Create persistent queue
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 10 * time.Second,
	}

	queue, err := NewPersistentQueue(
		config,
		tmpDir,
		server.Client(),
		nil, // no circuit breaker for test
		nil, // no circuit breaker for test
		logger,
		nil, // no metrics
	)
	if err != nil {
		t.Fatalf("Failed to create persistent queue: %v", err)
	}

	if err := queue.Start(); err != nil {
		t.Fatalf("Failed to start queue: %v", err)
	}
	defer queue.Shutdown(context.Background())

	t.Run("Small email (<1MB) stored inline", func(t *testing.T) {
		smallEmail := "Subject: Test Small\r\n\r\nThis is a small email"
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: smallEmail,
			Endpoint:     server.URL,
			Recipients:   []string{"test@example.com"},
		}

		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}

		// Email should be stored inline (no storage key)
		if job.EmailStorageKey != "" {
			t.Errorf("Small email should not have storage key, got: %s", job.EmailStorageKey)
		}

		// Wait for delivery (scheduler runs every 10s, so need to wait a bit)
		select {
		case <-delivered:
			if !strings.Contains(receivedContent, "This is a small email") {
				t.Errorf("Delivered content doesn't match: %s", receivedContent)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("Delivery timeout")
		}
	})

	t.Run("Large email (>1MB) stored on filesystem", func(t *testing.T) {
		// Create email larger than 1MB
		largeBody := strings.Repeat("A", 1<<20+1000) // 1MB + 1000 bytes
		largeEmail := "Subject: Test Large\r\n\r\n" + largeBody
		job := &DeliveryJob{
			ID:           GenerateJobID(),
			EmailContent: largeEmail,
			Endpoint:     server.URL,
			Recipients:   []string{"test@example.com"},
		}

		if err := queue.Enqueue(job); err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}

		// Email should be stored on filesystem (has storage key)
		if job.EmailStorageKey == "" {
			t.Error("Large email should have storage key")
		}

		// Verify email content was cleared from job
		if job.EmailContent != "" {
			t.Error("Large email content should be cleared from job")
		}

		// Verify file exists on filesystem
		emailFile := filepath.Join(tmpDir, "emails", job.EmailStorageKey[:2], job.EmailStorageKey+".eml")
		if _, err := os.Stat(emailFile); os.IsNotExist(err) {
			t.Errorf("Email file should exist at: %s", emailFile)
		}

		// Wait for delivery (scheduler runs every 10s, so need to wait a bit)
		select {
		case <-delivered:
			if !strings.Contains(receivedContent, largeBody[:100]) {
				t.Errorf("Delivered content doesn't match large email")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("Delivery timeout")
		}

		// Give time for cleanup after successful delivery
		time.Sleep(100 * time.Millisecond)

		// Verify file was deleted after successful delivery
		if _, err := os.Stat(emailFile); !os.IsNotExist(err) {
			t.Errorf("Email file should be deleted after delivery: %s", emailFile)
		}
	})
}

// TestPersistentQueue_OrphanCleanup tests that orphaned email files are cleaned up
func TestPersistentQueue_OrphanCleanup(t *testing.T) {
	// Create temp directory for queue data
	tmpDir, err := os.MkdirTemp("", "queue-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()

	// Create persistent queue
	config := QueueConfig{
		Workers:         1,
		MaxRetryHours:   48,
		DeliveryTimeout: 10 * time.Second,
	}

	queue, err := NewPersistentQueue(
		config,
		tmpDir,
		http.DefaultClient,
		nil, // no circuit breaker
		nil, // no circuit breaker
		logger,
		nil, // no metrics
	)
	if err != nil {
		t.Fatalf("Failed to create persistent queue: %v", err)
	}

	// Create orphaned email file manually
	emailDir := filepath.Join(tmpDir, "emails", "ab")
	if err := os.MkdirAll(emailDir, 0755); err != nil {
		t.Fatalf("Failed to create email dir: %v", err)
	}
	orphanFile := filepath.Join(emailDir, "abcdef1234567890.eml")
	if err := os.WriteFile(orphanFile, []byte("orphaned email content"), 0644); err != nil {
		t.Fatalf("Failed to create orphan file: %v", err)
	}

	// Verify orphan exists
	if _, err := os.Stat(orphanFile); os.IsNotExist(err) {
		t.Fatal("Orphan file should exist before cleanup")
	}

	// Run cleanup
	cleaned, err := queue.CleanupOrphanedEmails()
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	if cleaned != 1 {
		t.Errorf("Expected 1 orphan cleaned, got: %d", cleaned)
	}

	// Verify orphan was deleted
	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Error("Orphan file should be deleted after cleanup")
	}
}
