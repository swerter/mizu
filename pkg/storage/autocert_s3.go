package storage

import (
	"bytes"
	"context"
	"io"

	"github.com/minio/minio-go/v7"
	"golang.org/x/crypto/acme/autocert"
	"log/slog"
)

// AutocertS3Cache implements autocert.Cache using S3 storage
// Only allows writes (Put/Delete) if the current node is the cluster leader
type AutocertS3Cache struct {
	client    *minio.Client
	bucket    string
	prefix    string // S3 key prefix for autocert certificates
	isLeaderF func() bool
	logger    *slog.Logger
}

// NewAutocertS3Cache creates a new autocert.Cache implementation backed by S3
func NewAutocertS3Cache(client *minio.Client, bucket, prefix string, isLeaderF func() bool, logger *slog.Logger) *AutocertS3Cache {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	return &AutocertS3Cache{
		client:    client,
		bucket:    bucket,
		prefix:    prefix + "autocert/",
		isLeaderF: isLeaderF,
		logger:    logger,
	}
}

// Get reads certificate data from S3 (all nodes can read)
func (c *AutocertS3Cache) Get(ctx context.Context, key string) ([]byte, error) {
	objKey := c.prefix + key
	c.logger.Debug("Autocert: Getting certificate from S3", "key", key, "s3Path", objKey)

	obj, err := c.client.GetObject(ctx, c.bucket, objKey, minio.GetObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			c.logger.Debug("Autocert: Certificate not found in S3", "key", key)
			return nil, autocert.ErrCacheMiss
		}
		c.logger.Error("Autocert: Failed to get certificate from S3",
			"key", key, "error", err)
		return nil, err
	}
	defer obj.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, obj)
	if err != nil {
		c.logger.Error("Autocert: Failed to read certificate data",
			"key", key, "error", err)
		return nil, err
	}

	data := buf.Bytes()
	c.logger.Info("Autocert: Certificate retrieved from S3",
		"key", key, "size", len(data))
	return data, nil
}

// Put writes certificate data to S3 (only leader writes)
func (c *AutocertS3Cache) Put(ctx context.Context, key string, data []byte) error {
	if !c.isLeaderF() {
		c.logger.Debug("Autocert: Skipping certificate write (not leader)",
			"key", key)
		return nil // Silent success - prevents non-leaders from writing
	}

	objKey := c.prefix + key
	c.logger.Debug("Autocert: Storing certificate to S3", "key", key, "s3Path", objKey)

	_, err := c.client.PutObject(ctx, c.bucket, objKey, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		c.logger.Error("Autocert: Failed to put certificate to S3",
			"key", key, "error", err)
		return err
	}

	c.logger.Info("Autocert: Certificate stored in S3",
		"key", key, "size", len(data))
	return nil
}

// Delete removes certificate data from S3 (only leader deletes)
func (c *AutocertS3Cache) Delete(ctx context.Context, key string) error {
	if !c.isLeaderF() {
		c.logger.Debug("Autocert: Skipping certificate delete (not leader)",
			"key", key)
		return nil // Silent success - prevents non-leaders from deleting
	}

	objKey := c.prefix + key
	c.logger.Debug("Autocert: Deleting certificate from S3", "key", key, "s3Path", objKey)

	err := c.client.RemoveObject(ctx, c.bucket, objKey, minio.RemoveObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			// Already gone, consider it success
			c.logger.Debug("Autocert: Certificate already deleted", "key", key)
			return nil
		}
		c.logger.Error("Autocert: Failed to delete certificate from S3",
			"key", key, "error", err)
		return err
	}

	c.logger.Info("Autocert: Certificate deleted from S3", "key", key)
	return nil
}
