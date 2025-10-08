package config

import (
	"errors"
	"fmt"
)

// Validate checks the configuration for required fields and placeholder values.
func (c *Config) Validate() error {
	// Validate retry attempts (prevent infinite loops and excessive delays)
	if c.Delivery.MaxRetryAttempts > 5 {
		return fmt.Errorf("destination.max_retry_attempts must be <= 5 (got %d) to prevent excessive delays", c.Delivery.MaxRetryAttempts)
	}
	if c.Delivery.MaxRetryAttempts < 1 {
		return fmt.Errorf("destination.max_retry_attempts must be >= 1 (got %d)", c.Delivery.MaxRetryAttempts)
	}

	// Validate HTTP timeout
	if c.Delivery.HTTPTimeoutSeconds < 1 {
		return fmt.Errorf("destination.http_timeout_seconds must be >= 1 (got %d)", c.Delivery.HTTPTimeoutSeconds)
	}
	if c.Delivery.HTTPTimeoutSeconds > 300 {
		return fmt.Errorf("destination.http_timeout_seconds must be <= 300 (5m) (got %d) to prevent blocking SMTP sessions", c.Delivery.HTTPTimeoutSeconds)
	}

	// Validate distributed tracking settings
	if c.SMTP.Distributed.Enabled {
		if !c.Cluster.Enabled {
			return errors.New("smtp.distributed.enabled requires cluster.enabled=true")
		}
		if c.SMTP.Distributed.RecipientCacheTTLSeconds < 60 {
			return fmt.Errorf("smtp.distributed.recipient_cache_ttl_seconds must be >= 60 (1m) (got %d)", c.SMTP.Distributed.RecipientCacheTTLSeconds)
		}
	}

	// Production mode validations
	if c.Local {
		return nil // In local mode, remaining checks are skipped.
	}

	if c.SMTP.Domain == "" || c.SMTP.Domain == "mail.example.com" {
		return errors.New("smtp.domain must be set")
	}
	if c.Delivery.URL == "" {
		return errors.New("destination.url must be set")
	}
	if c.Delivery.APIKey == "" || c.Delivery.APIKey == "your-api-key-here" {
		return errors.New("destination.api_key must be set")
	}

	// Validate storage configuration based on backend
	if c.Storage.Backend == "" {
		c.Storage.Backend = "s3" // Default to S3
	}

	switch c.Storage.Backend {
	case "s3":
		if c.Storage.AccessKeyID == "" || c.Storage.AccessKeyID == "your-s3-access-key-id" {
			return errors.New("storage.access_key_id must be set when using S3 backend")
		}
		if c.Storage.SecretAccessKey == "" || c.Storage.SecretAccessKey == "your-s3-secret-access-key" {
			return errors.New("storage.secret_access_key must be set when using S3 backend")
		}
		if c.Storage.Bucket == "" {
			return errors.New("storage.bucket must be set when using S3 backend")
		}
	case "filesystem":
		if c.Storage.FilesystemPath == "" {
			return errors.New("storage.filesystem_path must be set when using filesystem backend")
		}
	default:
		return fmt.Errorf("invalid storage backend: %s (must be 's3' or 'filesystem')", c.Storage.Backend)
	}
	if c.TLS.Email == "" || c.TLS.Email == "admin@example.com" {
		return errors.New("tls.email must be set for Let's Encrypt certificate management")
	}

	// Validate autocert settings
	if c.TLS.EnableAutocert {
		if len(c.TLS.Domains) == 0 {
			return errors.New("tls.domains must be set when tls.enable_autocert=true")
		}
		if !c.Cluster.Enabled {
			return errors.New("tls.enable_autocert requires cluster.enabled=true for leader election")
		}
	}

	return nil
}
