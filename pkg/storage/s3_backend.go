package storage

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"log/slog"
)

// S3Backend implements the Backend interface using S3/MinIO
type S3Backend struct {
	client *minio.Client
	bucket string
	logger *slog.Logger
}

// NewS3Backend creates a new S3 storage backend
func NewS3Backend(client *minio.Client, bucket string, logger *slog.Logger) *S3Backend {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &S3Backend{
		client: client,
		bucket: bucket,
		logger: logger,
	}
}

// PutObject uploads an object to S3
func (s *S3Backend) PutObject(ctx context.Context, key string, reader io.Reader, size int64, opts PutOptions) error {
	minioOpts := minio.PutObjectOptions{
		ContentType: opts.ContentType,
	}

	if opts.ContentEncoding != "" {
		minioOpts.ContentEncoding = opts.ContentEncoding
	}

	if opts.Metadata != nil {
		minioOpts.UserMetadata = opts.Metadata
	}

	// Handle conditional put (IfNoneMatch: "*" means only create if not exists)
	if opts.IfNoneMatch == "*" {
		minioOpts.SetMatchETagExcept("*")
	}

	_, err := s.client.PutObject(ctx, s.bucket, key, reader, size, minioOpts)
	if err != nil {
		// Check for conditional put failure
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "PreconditionFailed" {
			s.logger.Debug("Conditional put failed - object already exists", "key", key)
			return &ConditionalPutError{Key: key}
		}
		return fmt.Errorf("failed to put object to S3: %w", err)
	}

	s.logger.Debug("Stored object to S3", "key", key, "size", size)
	return nil
}

// GetObject retrieves an object from S3
func (s *S3Backend) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			s.logger.Debug("Object not found in S3", "key", key)
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}

	s.logger.Debug("Retrieved object from S3", "key", key)
	return obj, nil
}

// StatObject returns metadata about an object in S3
func (s *S3Backend) StatObject(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			return ObjectInfo{}, os.ErrNotExist
		}
		return ObjectInfo{}, fmt.Errorf("failed to stat object in S3: %w", err)
	}

	return ObjectInfo{
		Key:          key,
		Size:         info.Size,
		LastModified: info.LastModified,
		ETag:         info.ETag,
	}, nil
}

// RemoveObject deletes an object from S3
func (s *S3Backend) RemoveObject(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			s.logger.Debug("Object already removed from S3", "key", key)
			return nil // Consider already removed as success
		}
		return fmt.Errorf("failed to remove object from S3: %w", err)
	}

	s.logger.Debug("Removed object from S3", "key", key)
	return nil
}

// ListObjects lists objects with a given prefix in S3
func (s *S3Backend) ListObjects(ctx context.Context, prefix string, recursive bool) ([]ObjectInfo, error) {
	var objects []ObjectInfo

	objectCh := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: recursive,
	})

	for object := range objectCh {
		if object.Err != nil {
			s.logger.Error("Error listing objects from S3", "error", object.Err)
			return nil, object.Err
		}

		objects = append(objects, ObjectInfo{
			Key:          object.Key,
			Size:         object.Size,
			LastModified: object.LastModified,
			ETag:         object.ETag,
		})
	}

	s.logger.Debug("Listed objects from S3",
		"prefix", prefix,
		"recursive", recursive,
		"count", len(objects))

	return objects, nil
}

// BucketExists checks if the S3 bucket exists
func (s *S3Backend) BucketExists(ctx context.Context) (bool, error) {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return false, fmt.Errorf("failed to check S3 bucket: %w", err)
	}
	return exists, nil
}

// MakeBucket creates the S3 bucket if it doesn't exist
func (s *S3Backend) MakeBucket(ctx context.Context) error {
	err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
	if err != nil {
		// Check if bucket already exists
		exists, existsErr := s.client.BucketExists(ctx, s.bucket)
		if existsErr == nil && exists {
			s.logger.Info("S3 bucket already exists", "bucket", s.bucket)
			return nil
		}
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	s.logger.Info("Created S3 bucket", "bucket", s.bucket)
	return nil
}
