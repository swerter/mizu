package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/pflag"
)

// LoadConfig loads configuration from file and command line flags
func LoadConfig(args []string) (*Config, error) {
	// Start with default configuration
	defaultCfg := DefaultConfig()
	cfg := &defaultCfg

	// Define command line flags
	fs := pflag.NewFlagSet("smtp-relay", pflag.ContinueOnError)

	// Config file flag
	configFile := fs.StringP("config", "c", "config.toml", "Path to configuration file")

	// SMTP flags
	fs.StringVar(&cfg.SMTP.ListenAddr, "smtp.listen", cfg.SMTP.ListenAddr, "SMTP listen address")
	fs.StringVar(&cfg.SMTP.Domain, "smtp.domain", cfg.SMTP.Domain, "SMTP domain")
	fs.IntVar(&cfg.SMTP.MaxMessageSize, "smtp.max-message-size", cfg.SMTP.MaxMessageSize, "Maximum message size in bytes")
	fs.IntVar(&cfg.SMTP.TimeoutSeconds, "smtp.timeout-seconds", cfg.SMTP.TimeoutSeconds, "SMTP timeout in seconds")
	fs.BoolVar(&cfg.SMTP.CheckXSpamFlag, "smtp.check-x-spam-flag", cfg.SMTP.CheckXSpamFlag, "Enable check for X-Spam-Flag header")
	fs.BoolVar(&cfg.SMTP.DMARCQuarantineAsJunk, "smtp.dmarc-quarantine-as-junk", cfg.SMTP.DMARCQuarantineAsJunk, "Treat DMARC quarantine policy as junk")

	// S3 flags
	fs.StringVar(&cfg.Storage.Endpoint, "s3.endpoint", cfg.Storage.Endpoint, "S3 endpoint")
	fs.StringVar(&cfg.Storage.Bucket, "s3.bucket", cfg.Storage.Bucket, "S3 bucket name")
	fs.StringVar(&cfg.Storage.Prefix, "s3.prefix", cfg.Storage.Prefix, "S3 key prefix")
	fs.StringVar(&cfg.Storage.Region, "s3.region", cfg.Storage.Region, "S3 region")
	fs.StringVar(&cfg.Storage.AccessKeyID, "s3.access-key-id", cfg.Storage.AccessKeyID, "S3 access key ID (prefer env var for security)")
	fs.StringVar(&cfg.Storage.SecretAccessKey, "s3.secret-access-key", cfg.Storage.SecretAccessKey, "S3 secret access key (prefer env var for security)")

	// Destination flags
	fs.StringVar(&cfg.Delivery.URL, "destination.url", cfg.Delivery.URL, "Destination URL")
	fs.StringVar(&cfg.Delivery.APIKey, "destination.api-key", cfg.Delivery.APIKey, "Destination API key, given in X-API-Key")

	// Health check flags
	fs.BoolVar(&cfg.Health.Enabled, "health.enabled", cfg.Health.Enabled, "Enable health check HTTP endpoint")
	fs.StringVar(&cfg.Health.ListenAddr, "health.listen-addr", cfg.Health.ListenAddr, "Health check listen address")

	// TLS flags
	fs.StringVar(&cfg.TLS.Email, "tls.email", cfg.TLS.Email, "Email for Let's Encrypt")
	fs.BoolVar(&cfg.TLS.UseProduction, "tls.production", cfg.TLS.UseProduction, "Use Let's Encrypt production")
	fs.BoolVar(&cfg.TLS.UseLocalCA, "tls.local-ca", cfg.TLS.UseLocalCA, "Use local CA for testing")
	fs.BoolVar(&cfg.TLS.CertMagicVerbose, "tls.verbose", cfg.TLS.CertMagicVerbose, "Enable verbose certmagic logging")

	// Logging
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "Log format (json or text)")

	// Local development mode
	fs.BoolVar(&cfg.Local, "local", cfg.Local, "Local development mode (no TLS, no certificates, dump to terminal)")

	// Help flag
	help := fs.BoolP("help", "h", false, "Show help message")

	// Parse command line flags
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if *help {
		fmt.Println("Mizu SMTP Relay Server")
		fmt.Println()
		fs.PrintDefaults()
		os.Exit(0)
	}

	// Load configuration from file if it exists
	if *configFile != "" {
		if _, err := os.Stat(*configFile); err == nil {
			if err := loadTOMLConfig(*configFile, cfg); err != nil {
				return nil, fmt.Errorf("failed to load config file: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to check config file: %w", err)
		}
	}

	// Re-parse flags to override config file values
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Load environment variables for sensitive data
	loadEnvVars(cfg)

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadTOMLConfig loads configuration from a TOML file
// LoadConfigFromFile loads configuration from a TOML file (for mizu-admin)
func LoadConfigFromFile(filename string) (*Config, error) {
	cfg := DefaultConfig()
	if err := loadTOMLConfig(filename, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadTOMLConfig(filename string, cfg *Config) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return err
	}

	return nil
}

// loadEnvVars loads configuration from environment variables
func loadEnvVars(cfg *Config) {
	// S3 credentials
	if v := os.Getenv("S3_ACCESS_KEY_ID"); v != "" {
		cfg.Storage.AccessKeyID = v
	}
	if v := os.Getenv("S3_SECRET_ACCESS_KEY"); v != "" {
		cfg.Storage.SecretAccessKey = v
	}
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		cfg.Storage.Endpoint = v
	}

	// Delivery credentials
	if v := os.Getenv("DELIVERY_URL"); v != "" {
		cfg.Delivery.URL = v
	}
	if v := os.Getenv("DELIVERY_API_KEY"); v != "" {
		cfg.Delivery.APIKey = v
	}
}

// validateConfig validates the configuration
func validateConfig(cfg *Config) error {
	// In local mode, allow the default domain or set a sensible default
	if cfg.Local {
		if cfg.SMTP.Domain == "" || cfg.SMTP.Domain == "mail.yourdomain.com" {
			cfg.SMTP.Domain = "localhost"
		}
		return nil
	}

	// In production mode, require a real domain (reject placeholder)
	if cfg.SMTP.Domain == "" || cfg.SMTP.Domain == "mail.yourdomain.com" {
		return fmt.Errorf("SMTP domain must be configured")
	}

	if cfg.Storage.Bucket == "" {
		return fmt.Errorf("S3 bucket must be configured")
	}

	// Check S3 credentials
	if cfg.Storage.AccessKeyID == "" {
		return fmt.Errorf("S3 access key ID must be configured (via config file, flag, or S3_ACCESS_KEY_ID env var)")
	}

	if cfg.Storage.SecretAccessKey == "" {
		return fmt.Errorf("S3 secret access key must be configured (via config file, flag, or S3_SECRET_ACCESS_KEY env var)")
	}

	if cfg.Delivery.URL == "" {
		return fmt.Errorf("destination URL must be configured")
	}

	if cfg.Delivery.APIKey == "" {
		return fmt.Errorf("delivery API key must be configured (via config file, flag, or DELIVERY_API_KEY env var)")
	}

	return nil
}

// SaveExample saves an example configuration file
func SaveExample(filename string) error {
	defaultCfg := DefaultConfig()
	cfg := &defaultCfg
	cfg.SMTP.Domain = "mail.example.com"
	cfg.TLS.Email = "admin@example.com"
	cfg.Delivery.URL = "https://your-worker.example.com/email"
	cfg.Delivery.APIKey = "your-api-key-here"
	cfg.Storage.AccessKeyID = "your-s3-access-key-id"
	cfg.Storage.SecretAccessKey = "your-s3-secret-access-key"

	// Create directory if needed
	dir := filepath.Dir(filename)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	encoder.Indent = ""
	return encoder.Encode(cfg)
}
