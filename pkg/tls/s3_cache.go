package tls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/crypto/acme/autocert"
)

// S3Cache implements autocert.Cache using S3 for certificate storage.
// This allows certificates to be shared across multiple instances of the application.
type S3Cache struct {
	client *s3.Client
	bucket string
	prefix string // Key prefix for certificate storage (default: "autocert/")
	logger *slog.Logger
}

// NewS3Cache creates a new S3-backed autocert cache using AWS SDK.
func NewS3Cache(client *s3.Client, bucket, prefix string, logger *slog.Logger) (*S3Cache, error) {
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
	_, err := c.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		var nsk *types.NoSuchBucket
		if errors.As(err, &nsk) {
			return fmt.Errorf("bucket %s does not exist", c.bucket)
		}
		return fmt.Errorf("failed to check bucket existence: %w", err)
	}
	return nil
}

// Get retrieves a certificate data from S3.
func (c *S3Cache) Get(ctx context.Context, key string) ([]byte, error) {
	s3Key := c.prefix + hashKey(key)

	c.logger.Debug("S3-Cache: Getting certificate", "key", key, "s3_key", s3Key)

	// Get object from S3
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			c.logger.Debug("S3-Cache: Certificate not found (cache miss)", "key", key)
			return nil, autocert.ErrCacheMiss
		}
		c.logger.Error("S3-Cache: Failed to get object from S3", "error", err)
		return nil, autocert.ErrCacheMiss
	}
	defer result.Body.Close()

	// Read object data
	data, err := io.ReadAll(result.Body)
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
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(s3Key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
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
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		// Check if object doesn't exist (which is fine for Delete)
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			c.logger.Debug("S3-Cache: Certificate already deleted or doesn't exist", "key", key)
			return nil
		}
		c.logger.Error("S3-Cache: Failed to delete certificate from S3", "error", err)
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	c.logger.Debug("S3-Cache: Successfully deleted certificate", "key", key)
	return nil
}
