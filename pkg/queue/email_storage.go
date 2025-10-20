package queue

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"log/slog"
)

// EmailStorage handles large email content storage on filesystem
// This is more efficient than storing large blobs in BadgerDB
type EmailStorage struct {
	baseDir string
	logger  *slog.Logger
}

// NewEmailStorage creates a new email content storage
func NewEmailStorage(baseDir string, logger *slog.Logger) (*EmailStorage, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("base directory is required")
	}

	// Create directory structure
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create email storage directory: %w", err)
	}

	return &EmailStorage{
		baseDir: baseDir,
		logger:  logger,
	}, nil
}

// Save stores email content to filesystem and returns a storage key
func (es *EmailStorage) Save(jobID string, emailContent []byte) (string, error) {
	// Generate hash-based filename for content-addressable storage
	hash := sha256.Sum256(emailContent)
	hashStr := hex.EncodeToString(hash[:])

	// Use first 2 chars of hash for sharding (256 subdirectories)
	// This prevents too many files in a single directory
	shard := hashStr[:2]
	shardDir := filepath.Join(es.baseDir, shard)

	// Create shard directory if needed
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create shard directory: %w", err)
	}

	// Filename: {hash}.eml
	filename := hashStr + ".eml"
	filePath := filepath.Join(shardDir, filename)

	// Check if file already exists (deduplication)
	if _, err := os.Stat(filePath); err == nil {
		es.logger.Debug("Email content already exists (deduplicated)",
			"job_id", jobID,
			"hash", hashStr,
			"size_bytes", len(emailContent))
		return hashStr, nil
	}

	// Write atomically: write to temp file, then rename
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, emailContent, 0644); err != nil {
		return "", fmt.Errorf("failed to write email content: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return "", fmt.Errorf("failed to rename email file: %w", err)
	}

	// Sync directory to ensure rename is durable
	dir, err := os.Open(shardDir)
	if err == nil {
		dir.Sync()
		dir.Close()
	}

	es.logger.Debug("Email content saved to filesystem",
		"job_id", jobID,
		"hash", hashStr,
		"size_bytes", len(emailContent))

	return hashStr, nil
}

// Load retrieves email content from filesystem by storage key
func (es *EmailStorage) Load(storageKey string) ([]byte, error) {
	if len(storageKey) < 2 {
		return nil, fmt.Errorf("invalid storage key: %s", storageKey)
	}

	// Reconstruct path from hash
	shard := storageKey[:2]
	filename := storageKey + ".eml"
	filePath := filepath.Join(es.baseDir, shard, filename)

	// Read file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read email content: %w", err)
	}

	return content, nil
}

// Delete removes email content from filesystem
func (es *EmailStorage) Delete(storageKey string) error {
	if len(storageKey) < 2 {
		return fmt.Errorf("invalid storage key: %s", storageKey)
	}

	shard := storageKey[:2]
	filename := storageKey + ".eml"
	filePath := filepath.Join(es.baseDir, shard, filename)

	// Remove file (ignore if doesn't exist)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete email content: %w", err)
	}

	return nil
}

// Exists checks if email content exists
func (es *EmailStorage) Exists(storageKey string) bool {
	if len(storageKey) < 2 {
		return false
	}

	shard := storageKey[:2]
	filename := storageKey + ".eml"
	filePath := filepath.Join(es.baseDir, shard, filename)

	_, err := os.Stat(filePath)
	return err == nil
}

// CleanupOrphaned removes email files that don't have corresponding jobs
// This should be called periodically to clean up after crashes
func (es *EmailStorage) CleanupOrphaned(activeKeys map[string]bool) (int, error) {
	cleaned := 0

	// Walk through all shard directories
	shardDirs, err := os.ReadDir(es.baseDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read storage directory: %w", err)
	}

	for _, shardDir := range shardDirs {
		if !shardDir.IsDir() {
			continue
		}

		shardPath := filepath.Join(es.baseDir, shardDir.Name())
		files, err := os.ReadDir(shardPath)
		if err != nil {
			es.logger.Warn("Failed to read shard directory",
				"shard", shardDir.Name(),
				"error", err)
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			// Extract storage key from filename
			filename := file.Name()
			if len(filename) < 4 || filename[len(filename)-4:] != ".eml" {
				continue
			}

			storageKey := filename[:len(filename)-4]

			// Check if this key is still active
			if !activeKeys[storageKey] {
				filePath := filepath.Join(shardPath, filename)
				if err := os.Remove(filePath); err != nil {
					es.logger.Warn("Failed to remove orphaned email",
						"file", filePath,
						"error", err)
				} else {
					cleaned++
					es.logger.Debug("Removed orphaned email file",
						"storage_key", storageKey)
				}
			}
		}
	}

	return cleaned, nil
}

// GetStats returns storage statistics
func (es *EmailStorage) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var totalFiles int
	var totalBytes int64

	// Walk through all shard directories
	shardDirs, err := os.ReadDir(es.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage directory: %w", err)
	}

	for _, shardDir := range shardDirs {
		if !shardDir.IsDir() {
			continue
		}

		shardPath := filepath.Join(es.baseDir, shardDir.Name())
		files, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			info, err := file.Info()
			if err != nil {
				continue
			}

			totalFiles++
			totalBytes += info.Size()
		}
	}

	stats["total_files"] = totalFiles
	stats["total_bytes"] = totalBytes
	stats["total_mb"] = float64(totalBytes) / (1024 * 1024)

	return stats, nil
}
