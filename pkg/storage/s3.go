package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/minio/minio-go/v7"
	"log/slog"
)

// CertMeta stores metadata about the TLS certificate for S3 storage.
// NOTE: This struct is currently unused in this file.
type CertMeta struct {
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	BeingIssuedBy string    `json:"being_issued_by"` // IP address of the instance that issued/renewed the cert
	PEM           []byte    `json:"pem"`             // Certificate in PEM format
	Key           []byte    `json:"key"`             // Private key in PEM format
}

// S3CertStorage implements certmagic.Storage using MinIO client (S3 compatible).
type S3CertStorage struct {
	client *minio.Client // MinIO client for S3 operations
	bucket string        // S3 bucket name
	prefix string        // S3 key prefix for certificate files
	logger *slog.Logger  // Structured logger
}

// NewS3CertStorage creates a new S3 certificate storage instance.
func NewS3CertStorage(client *minio.Client, bucket, prefix string, logger *slog.Logger) *S3CertStorage {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &S3CertStorage{
		client: client,
		bucket: bucket,
		prefix: prefix,
		logger: logger,
	}
}

// Lock acquires a lock for a given key.
// This implementation uses S3's conditional put (If-None-Match: *) to ensure
// that the lock file is created only if it does not already exist, providing
// an atomic operation for distributed locking.
func (s *S3CertStorage) Lock(ctx context.Context, key string) error {
	lockKey := s.prefix + key + ".lock"

	// Generate a unique identifier for this lock attempt, including hostname and username for debugging
	hostname, _ := os.Hostname()
	currentUser, _ := user.Current()
	lockContent := []byte(fmt.Sprintf("%s@%s-%d", currentUser.Username, hostname, time.Now().UnixNano()))

	s.logger.Debug("Certmagic: Attempting to acquire lock", "key", key, "lockFile", lockKey)

	opts := minio.PutObjectOptions{
		ContentType: "text/plain",
	}
	// Crucial: Only create if object does not exist. This makes it an atomic operation.
	// Use SetMatchETagExcept for If-None-Match header.
	opts.SetMatchETagExcept("*")

	_, err := s.client.PutObject(ctx, s.bucket, lockKey, bytes.NewReader(lockContent), int64(len(lockContent)), opts)
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "PreconditionFailed" {
			// This means the object already exists, so the lock is held by someone else.
			s.logger.Warn("Certmagic: Lock already held by another instance", "key", key)
			return fmt.Errorf("lock for %s already held", key) // Certmagic expects an error if lock cannot be acquired
		}
		s.logger.Error("Certmagic: Failed to acquire lock", "key", key, "error", err)
		return fmt.Errorf("failed to acquire lock for %s: %w", key, err)
	}

	s.logger.Info("Certmagic: Successfully acquired lock", "key", key, "lockId", string(lockContent))
	return nil
}

// Unlock releases a lock for a given key.
func (s *S3CertStorage) Unlock(ctx context.Context, key string) error {
	lockKey := s.prefix + key + ".lock"
	s.logger.Debug("Certmagic: Releasing lock", "key", key, "lockFile", lockKey)
	err := s.client.RemoveObject(ctx, s.bucket, lockKey, minio.RemoveObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			// Lock file already gone, perhaps another instance cleaned it up or it expired.
			s.logger.Warn("Certmagic: Lock file already gone, nothing to unlock", "key", key)
			return nil // Consider it successfully unlocked if it's already gone
		}
		s.logger.Error("Certmagic: Failed to release lock", "key", key, "error", err)
		return fmt.Errorf("failed to release lock for %s: %w", key, err)
	}
	s.logger.Info("Certmagic: Successfully released lock", "key", key)
	return nil
}

// Store saves data to S3.
func (s *S3CertStorage) Store(ctx context.Context, key string, value []byte) error {
	objKey := s.prefix + key
	s.logger.Debug("Certmagic: Storing object", "key", key, "s3Path", objKey)
	_, err := s.client.PutObject(ctx, s.bucket, objKey, bytes.NewReader(value), int64(len(value)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		s.logger.Error("Certmagic: Failed to store object", "key", key, "error", err)
	}
	return err
}

// Load loads data from S3.
func (s *S3CertStorage) Load(ctx context.Context, key string) ([]byte, error) {
	objKey := s.prefix + key
	s.logger.Debug("Certmagic: Loading object", "key", key, "s3Path", objKey)
	obj, err := s.client.GetObject(ctx, s.bucket, objKey, minio.GetObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			s.logger.Debug("Certmagic: Object not found", "key", key)
			return nil, os.ErrNotExist // Certmagic expects this error for non-existent keys
		}
		s.logger.Error("Certmagic: Failed to load object", "key", key, "error", err)
		// For other errors (network, permissions, etc.), return the original error
		return nil, err
	}
	defer obj.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, obj)
	if err != nil {
		s.logger.Error("Certmagic: Failed to read object content", "key", key, "error", err)
		return nil, fmt.Errorf("failed to read object %s from S3: %w", objKey, err)
	}
	s.logger.Debug("Certmagic: Successfully loaded object", "key", key)
	return buf.Bytes(), nil
}

// Delete removes data from S3.
func (s *S3CertStorage) Delete(ctx context.Context, key string) error {
	objKey := s.prefix + key
	s.logger.Debug("Certmagic: Deleting object", "key", key, "s3Path", objKey)
	err := s.client.RemoveObject(ctx, s.bucket, objKey, minio.RemoveObjectOptions{})
	if err != nil {
		s.logger.Error("Certmagic: Failed to delete object", "key", key, "error", err)
	}
	return err
}

// Exists checks if data exists in S3.
func (s *S3CertStorage) Exists(ctx context.Context, key string) bool {
	objKey := s.prefix + key
	_, err := s.client.StatObject(ctx, s.bucket, objKey, minio.StatObjectOptions{})
	exists := err == nil
	if !exists {
		s.logger.Debug("Certmagic: Object does not exist", "key", key)
	}
	return exists
}

// List lists objects in S3 with a given prefix.
func (s *S3CertStorage) List(ctx context.Context, prefix string, recursive bool) ([]string, error) {
	s.logger.Debug("Certmagic: Listing objects", "prefix", s.prefix+prefix, "recursive", recursive)
	var keys []string
	objectCh := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.prefix + prefix,
		Recursive: recursive,
	})
	for object := range objectCh {
		if object.Err != nil {
			s.logger.Error("Certmagic: Error listing objects", "error", object.Err)
			return nil, object.Err
		}
		// Certmagic expects keys relative to its own prefix, so trim it
		keys = append(keys, strings.TrimPrefix(object.Key, s.prefix))
	}
	s.logger.Debug("Certmagic: Finished listing objects", "count", len(keys))
	return keys, nil
}

// Stat returns information about a key in S3, as required by certmagic.Storage.
func (s *S3CertStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	objKey := s.prefix + key
	s.logger.Debug("Certmagic: Getting object stat", "key", key, "s3Path", objKey)
	info, err := s.client.StatObject(ctx, s.bucket, objKey, minio.StatObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			s.logger.Debug("Certmagic: Object not found for stat", "key", key)
			return certmagic.KeyInfo{}, os.ErrNotExist
		}
		s.logger.Error("Certmagic: Failed to get object stat", "key", key, "error", err)
		return certmagic.KeyInfo{}, err
	}
	return certmagic.KeyInfo{
		Key:        key,
		Modified:   info.LastModified,
		Size:       info.Size,
		IsTerminal: false,
	}, nil
}
