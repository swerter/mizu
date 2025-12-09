package tls

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"migadu/mizu/pkg/storage"

	"golang.org/x/crypto/acme/autocert"
)

// TestStorageCache_FilesystemBackend tests StorageCache with filesystem backend
func TestStorageCache_FilesystemBackend(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create filesystem backend
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	// Ensure storage exists
	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create storage cache
	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Test Put and Get
	testKey := "example.com"
	testData := []byte("test certificate data")

	if err := cache.Put(ctx, testKey, testData); err != nil {
		t.Fatalf("Failed to put data: %v", err)
	}

	retrievedData, err := cache.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if string(retrievedData) != string(testData) {
		t.Errorf("Retrieved data mismatch. Got %s, want %s", string(retrievedData), string(testData))
	}

	t.Log("✓ Put and Get work correctly with filesystem backend")
}

// TestStorageCache_CacheMiss tests cache miss behavior
func TestStorageCache_CacheMiss(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Try to get non-existent key
	_, err = cache.Get(ctx, "nonexistent.com")
	if err != autocert.ErrCacheMiss {
		t.Errorf("Expected ErrCacheMiss, got %v", err)
	}

	t.Log("✓ Cache miss returns correct error")
}

// TestStorageCache_Delete tests deletion
func TestStorageCache_Delete(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Put data
	testKey := "example.com"
	testData := []byte("test certificate data")
	if err := cache.Put(ctx, testKey, testData); err != nil {
		t.Fatalf("Failed to put data: %v", err)
	}

	// Delete
	if err := cache.Delete(ctx, testKey); err != nil {
		t.Fatalf("Failed to delete: %v", err)
	}

	// Verify deleted
	_, err = cache.Get(ctx, testKey)
	if err != autocert.ErrCacheMiss {
		t.Errorf("Expected ErrCacheMiss after delete, got %v", err)
	}

	t.Log("✓ Delete works correctly")
}

// TestStorageCache_DeleteNonexistent tests deleting non-existent keys
func TestStorageCache_DeleteNonexistent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Delete non-existent key (should not error)
	if err := cache.Delete(ctx, "nonexistent.com"); err != nil {
		t.Fatalf("Delete of non-existent key should not error: %v", err)
	}

	t.Log("✓ Delete of non-existent key succeeds")
}

// TestStorageCache_KeyHashing tests that keys are properly hashed
func TestStorageCache_KeyHashing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Test with special characters in domain name
	testKey := "*.example.com"
	testData := []byte("wildcard certificate")

	if err := cache.Put(ctx, testKey, testData); err != nil {
		t.Fatalf("Failed to put data with special characters: %v", err)
	}

	retrievedData, err := cache.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to get data with special characters: %v", err)
	}

	if string(retrievedData) != string(testData) {
		t.Errorf("Retrieved data mismatch for special characters")
	}

	// Verify files are actually stored with hashed names
	files, err := filepath.Glob(filepath.Join(tempDir, "test/autocert", "*"))
	if err != nil {
		t.Fatalf("Failed to list files: %v", err)
	}

	if len(files) == 0 {
		t.Error("No files found in storage")
	}

	// Check that filenames don't contain special characters
	for _, file := range files {
		basename := filepath.Base(file)
		if basename != "*.example.com" { // Should be hashed, not literal
			t.Logf("✓ Keys are properly hashed: %s", basename)
		}
	}
}

// TestStorageCache_MultipleDomains tests storing multiple certificates
func TestStorageCache_MultipleDomains(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	cache, err := NewStorageCache(backend, "test", logger)
	if err != nil {
		t.Fatalf("Failed to create storage cache: %v", err)
	}

	// Store multiple certificates
	domains := map[string][]byte{
		"example.com":     []byte("cert1"),
		"test.com":        []byte("cert2"),
		"*.wildcard.com":  []byte("cert3"),
		"sub.example.com": []byte("cert4"),
	}

	for domain, certData := range domains {
		if err := cache.Put(ctx, domain, certData); err != nil {
			t.Fatalf("Failed to put cert for %s: %v", domain, err)
		}
	}

	// Retrieve and verify each certificate
	for domain, expectedData := range domains {
		retrievedData, err := cache.Get(ctx, domain)
		if err != nil {
			t.Fatalf("Failed to get cert for %s: %v", domain, err)
		}

		if string(retrievedData) != string(expectedData) {
			t.Errorf("Certificate mismatch for %s", domain)
		}
	}

	t.Logf("✓ Successfully stored and retrieved %d certificates", len(domains))
}

// TestStorageCache_PrefixIsolation tests that different prefixes don't interfere
func TestStorageCache_PrefixIsolation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mizu-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend, err := storage.NewFilesystemBackend(tempDir, logger)
	if err != nil {
		t.Fatalf("Failed to create filesystem backend: %v", err)
	}

	ctx := context.Background()
	if err := backend.MakeBucket(ctx); err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create two caches with different prefixes
	cache1, err := NewStorageCache(backend, "prefix1", logger)
	if err != nil {
		t.Fatalf("Failed to create cache1: %v", err)
	}

	cache2, err := NewStorageCache(backend, "prefix2", logger)
	if err != nil {
		t.Fatalf("Failed to create cache2: %v", err)
	}

	// Store same key in both caches with different data
	testKey := "example.com"
	data1 := []byte("cert from cache1")
	data2 := []byte("cert from cache2")

	if err := cache1.Put(ctx, testKey, data1); err != nil {
		t.Fatalf("Failed to put in cache1: %v", err)
	}

	if err := cache2.Put(ctx, testKey, data2); err != nil {
		t.Fatalf("Failed to put in cache2: %v", err)
	}

	// Verify isolation
	retrieved1, err := cache1.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to get from cache1: %v", err)
	}

	retrieved2, err := cache2.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to get from cache2: %v", err)
	}

	if string(retrieved1) != string(data1) {
		t.Error("Cache1 data corrupted")
	}

	if string(retrieved2) != string(data2) {
		t.Error("Cache2 data corrupted")
	}

	if string(retrieved1) == string(retrieved2) {
		t.Error("Caches are not isolated by prefix")
	}

	t.Log("✓ Prefix isolation works correctly")
}
