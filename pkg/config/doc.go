// Package config handles configuration loading and validation for Mizu.
//
// # Overview
//
// The config package provides TOML-based configuration with environment variable
// support for sensitive values. It defines all configuration structures and
// provides validation and default values.
//
// # Configuration File Format
//
// Configuration uses TOML format with nested sections:
//
//	[smtp]
//	listen_addr = ":25"
//	domain = "mail.example.com"
//	max_message_size = 10485760  # 10MB
//
//	[smtp.arc_sign]
//	enabled = false
//	domain = "mail.example.com"
//	selector = "arc"
//	private_key_path = "/etc/mizu/arc-private.pem"
//
//	[delivery]
//	url = "https://backend.example.com/email"
//	api_key = "${DELIVERY_API_KEY}"  # From environment
//
//	[storage]
//	backend = "s3"  # or "filesystem"
//	bucket = "mizu-storage"
//
//	[cluster]
//	enabled = true
//	peers = ["node1.example.com:7946", "node2.example.com:7946"]
//
// # Environment Variables
//
// Sensitive values can be provided via environment variables:
//
//	DELIVERY_API_KEY: API key for HTTP backend
//	S3_ACCESS_KEY_ID: AWS access key ID
//	S3_SECRET_ACCESS_KEY: AWS secret access key
//	HEALTH_PASSWORD: Password for health endpoint
//	CLUSTER_SECRET_KEY: 32-byte base64-encoded encryption key for cluster
//
// Environment variables override values in the configuration file.
//
// # Loading Configuration
//
//	cfg, err := config.Load("config.toml")
//	if err != nil {
//	    log.Fatal("Failed to load config:", err)
//	}
//
// # Validation
//
// The Load function automatically validates the configuration:
//
//   - Required fields are present
//   - Numeric values are within valid ranges
//   - Domain names are valid
//   - Port numbers are valid
//   - File paths exist (when applicable)
//   - Distributed features have required dependencies
//
// # Default Values
//
// The package provides sensible defaults via DefaultConfig():
//
//	SMTP listen address: :25
//	Max message size: 10MB
//	Connection timeout: 10s
//	Max connections: 100
//	Max connections per IP: 10
//	Rate limit: 60 emails/minute per IP
//	DNS timeout: 5s
//	HTTP timeout: 30s
//	Circuit breaker: enabled with threshold of 5 failures
//
// # Storage Backend
//
// Two storage backends are supported:
//
//  1. S3 (default) - For production clusters
//     Used for TLS certificates and reputation stats sync
//
//  2. Filesystem - For single-node deployments
//     Stores data in local directory
//
// # Distributed Features
//
// Some features require cluster mode (cluster.enabled=true):
//
//	Distributed connection tracking (smtp.distributed.enabled)
//	Rate limit gossip (smtp.rate_limit.gossip_enabled)
//	TLS certificate management with leader election
//	Reputation stats sync across cluster
//
// # Example: Generate Configuration
//
//	// Generate example config file
//	if err := config.SaveExample("config.toml.example"); err != nil {
//	    log.Fatal(err)
//	}
//
// # Configuration Sections
//
// The Config struct has the following main sections:
//
//	SMTP: SMTP server settings (port, domain, limits, validation)
//	DNS: DNS resolver configuration
//	Storage: S3 or filesystem backend settings
//	Destination: HTTP backend endpoint configuration
//	TLS: Let's Encrypt and certificate management
//	Blacklists: DNS blacklist (DNSBL) configuration
//	Health: Health check endpoint settings
//	Metrics: Prometheus metrics endpoint settings
//	Stats: Reputation tracking configuration
//	Cluster: Distributed coordination settings
//
// # Thread Safety
//
// Config instances are immutable after loading and can be safely
// shared across goroutines.
package config
