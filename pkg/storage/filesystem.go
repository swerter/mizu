package storage

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"
)

// FilesystemBackend implements the Backend interface using local filesystem
type FilesystemBackend struct {
	basePath string
	logger   *slog.Logger
}

// NewFilesystemBackend creates a new filesystem storage backend
func NewFilesystemBackend(basePath string, logger *slog.Logger) (*FilesystemBackend, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Expand ~ to home directory if present
	if strings.HasPrefix(basePath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		basePath = filepath.Join(home, basePath[2:])
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	logger.Info("Initializing filesystem storage backend", "path", absPath)

	return &FilesystemBackend{
		basePath: absPath,
		logger:   logger,
	}, nil
}

// getFullPath converts a storage key to a full filesystem path
func (f *FilesystemBackend) getFullPath(key string) string {
	// Sanitize key to prevent directory traversal attacks
	key = filepath.Clean(key)
	key = strings.TrimPrefix(key, "/")
	return filepath.Join(f.basePath, key)
}

// PutObject uploads an object to the filesystem
func (f *FilesystemBackend) PutObject(ctx context.Context, key string, reader io.Reader, size int64, opts PutOptions) error {
	fullPath := f.getFullPath(key)

	// Check for conditional put (IfNoneMatch: "*" means only create if not exists)
	if opts.IfNoneMatch == "*" {
		if _, err := os.Stat(fullPath); err == nil {
			f.logger.Debug("Conditional put failed - file already exists", "key", key)
			return &ConditionalPutError{Key: key}
		}
	}

	// Create parent directories
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Create temporary file for atomic write
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // Clean up on error

	// Copy data to temp file
	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write data: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename to final destination
	if err := os.Rename(tmpPath, fullPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	f.logger.Debug("Stored object to filesystem",
		"key", key,
		"size", written)

	return nil
}

// GetObject retrieves an object from the filesystem
func (f *FilesystemBackend) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	fullPath := f.getFullPath(key)

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			f.logger.Debug("Object not found", "key", key)
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	f.logger.Debug("Retrieved object from filesystem", "key", key)
	return file, nil
}

// StatObject returns metadata about an object
func (f *FilesystemBackend) StatObject(ctx context.Context, key string) (ObjectInfo, error) {
	fullPath := f.getFullPath(key)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectInfo{}, os.ErrNotExist
		}
		return ObjectInfo{}, fmt.Errorf("failed to stat file: %w", err)
	}

	// Generate ETag as MD5 hash of filename + mtime (similar to S3)
	etag := generateETag(key, info.ModTime())

	return ObjectInfo{
		Key:          key,
		Size:         info.Size(),
		LastModified: info.ModTime(),
		ETag:         etag,
	}, nil
}

// RemoveObject deletes an object from the filesystem
func (f *FilesystemBackend) RemoveObject(ctx context.Context, key string) error {
	fullPath := f.getFullPath(key)

	err := os.Remove(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			f.logger.Debug("Object already removed", "key", key)
			return nil // Consider already removed as success
		}
		return fmt.Errorf("failed to remove file: %w", err)
	}

	f.logger.Debug("Removed object from filesystem", "key", key)
	return nil
}

// ListObjects lists objects with a given prefix
func (f *FilesystemBackend) ListObjects(ctx context.Context, prefix string, recursive bool) ([]ObjectInfo, error) {
	searchPath := f.getFullPath(prefix)
	var objects []ObjectInfo

	// If searchPath doesn't exist, return empty list
	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		return objects, nil
	}

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			f.logger.Warn("Error walking path", "path", path, "error", err)
			return nil // Continue on error
		}

		// Skip directories
		if info.IsDir() {
			// If not recursive and this is a subdirectory of searchPath, skip it
			if !recursive && path != searchPath {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative key from base path
		relPath, err := filepath.Rel(f.basePath, path)
		if err != nil {
			f.logger.Warn("Failed to get relative path", "path", path, "error", err)
			return nil
		}

		// Convert to forward slashes for consistency (like S3)
		key := filepath.ToSlash(relPath)

		objects = append(objects, ObjectInfo{
			Key:          key,
			Size:         info.Size(),
			LastModified: info.ModTime(),
			ETag:         generateETag(key, info.ModTime()),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	f.logger.Debug("Listed objects from filesystem",
		"prefix", prefix,
		"recursive", recursive,
		"count", len(objects))

	return objects, nil
}

// BucketExists checks if the storage directory exists
func (f *FilesystemBackend) BucketExists(ctx context.Context) (bool, error) {
	info, err := os.Stat(f.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check directory: %w", err)
	}

	if !info.IsDir() {
		return false, fmt.Errorf("path exists but is not a directory: %s", f.basePath)
	}

	return true, nil
}

// MakeBucket creates the storage directory if it doesn't exist
func (f *FilesystemBackend) MakeBucket(ctx context.Context) error {
	err := os.MkdirAll(f.basePath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	f.logger.Info("Created storage directory", "path", f.basePath)
	return nil
}

// generateETag generates a simple ETag for filesystem objects
func generateETag(key string, modTime time.Time) string {
	h := md5.New()
	h.Write([]byte(key))
	h.Write([]byte(modTime.String()))
	return hex.EncodeToString(h.Sum(nil))
}
