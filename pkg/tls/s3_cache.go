package tls

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/minio/minio-go/v7"
	"golang.org/x/crypto/acme/autocert"
)

// S3Cache implements autocert.Cache using S3 for certificate storage.
// This allows certificates to be shared across multiple instances of the application.
type S3Cache struct {
	client *minio.Client
	bucket string
	prefix string // Key prefix for certificate storage (default: "autocert/")
	logger *slog.Logger
}

// NewS3Cache creates a new S3-backed autocert cache using MinIO client.
func NewS3Cache(client *minio.Client, bucket, prefix string, logger *slog.Logger) (*S3Cache, error) {
	ctx := context.Background()

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	cache := &S3Cache{
		client: client,
		bucket: bucket,
		prefix: prefix + "autocert/",
		logger: logger,
	}

	// Verify bucket access
	if err := cache.verifyBucketAccess(ctx); err != nil {
		return nil, fmt.Errorf("failed to verify S3 bucket access: %w", err)
	}

	logger.Info("S3 autocert cache initialized", "bucket", bucket, "prefix", cache.prefix)
	return cache, nil
}

// verifyBucketAccess checks if the S3 bucket exists and is accessible.
func (c *S3Cache) verifyBucketAccess(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %s does not exist", c.bucket)
	}
	return nil
}

// Get retrieves a certificate data from S3.
func (c *S3Cache) Get(ctx context.Context, key string) ([]byte, error) {
	s3Key := c.prefix + hashKey(key)

	c.logger.Debug("S3-Cache: Getting certificate", "key", key, "s3_key", s3Key)

	// Get object from S3
	obj, err := c.client.GetObject(ctx, c.bucket, s3Key, minio.GetObjectOptions{})
	if err != nil {
		c.logger.Error("S3-Cache: Failed to get object from S3", "error", err)
		return nil, autocert.ErrCacheMiss
	}
	defer obj.Close()

	// Check if object exists (404 means cache miss)
	if _, err := obj.Stat(); err != nil {
		// MinIO returns error on stat if object doesn't exist
		if minio.ToErrorResponse(err).StatusCode == 404 {
			c.logger.Debug("S3-Cache: Certificate not found (cache miss)", "key", key)
			return nil, autocert.ErrCacheMiss
		}
		c.logger.Error("S3-Cache: Failed to stat object", "error", err)
		return nil, fmt.Errorf("failed to stat object: %w", err)
	}

	// Read object data
	data, err := io.ReadAll(obj)
	if err != nil {
		c.logger.Error("S3-Cache: Failed to read object data", "error", err)
		return nil, fmt.Errorf("failed to read object: %w", err)
	}

	c.logger.Debug("S3-Cache: Successfully retrieved certificate", "key", key, "bytes", len(data))
	return data, nil
}

// Put stores certificate data in S3.
func (c *S3Cache) Put(ctx context.Context, key string, data []byte) error {
	s3Key := c.prefix + hashKey(key)

	c.logger.Debug("S3-Cache: Putting certificate", "key", key, "s3_key", s3Key, "bytes", len(data))

	// Upload to S3
	_, err := c.client.PutObject(
		ctx,
		c.bucket,
		s3Key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{
			ContentType: "application/octet-stream",
		},
	)
	if err != nil {
		c.logger.Error("S3-Cache: Failed to upload certificate to S3", "error", err)
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	c.logger.Debug("S3-Cache: Successfully stored certificate", "key", key)
	return nil
}

// Delete removes certificate data from S3.
func (c *S3Cache) Delete(ctx context.Context, key string) error {
	s3Key := c.prefix + hashKey(key)

	c.logger.Debug("S3-Cache: Deleting certificate", "key", key, "s3_key", s3Key)

	// Delete from S3
	err := c.client.RemoveObject(ctx, c.bucket, s3Key, minio.RemoveObjectOptions{})
	if err != nil {
		// Check if object doesn't exist (which is fine for Delete)
		if minio.ToErrorResponse(err).StatusCode == 404 {
			c.logger.Debug("S3-Cache: Certificate already deleted or doesn't exist", "key", key)
			return nil
		}
		c.logger.Error("S3-Cache: Failed to delete certificate from S3", "error", err)
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	c.logger.Debug("S3-Cache: Successfully deleted certificate", "key", key)
	return nil
}
