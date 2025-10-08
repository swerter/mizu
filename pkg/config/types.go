package config

import "time"

// Config holds all configuration for the SMTP relay server
type Config struct {
	SMTP       SMTPConfig       `toml:"smtp"`
	DNS        DNSConfig        `toml:"dns"`
	Storage    StorageConfig    `toml:"storage"`
	Delivery   DeliveryConfig   `toml:"delivery"`
	Forwarding ForwardingConfig `toml:"forwarding"` // Optional forwarding endpoint (used with routing)
	Routing    RoutingConfig    `toml:"routing"`    // Optional routing/aliasing system
	Queue      QueueConfig      `toml:"queue"`      // Optional async delivery queue (used with routing)
	TLS        TLSConfig        `toml:"tls"`
	Blacklists BlacklistsConfig `toml:"blacklists"`
	Health     HealthConfig     `toml:"health"`
	Metrics    MetricsConfig    `toml:"metrics"`
	Stats      StatsConfig      `toml:"stats"`
	Cluster    ClusterConfig    `toml:"cluster"`    // Global cluster/peering configuration
	LogFormat  string           `toml:"log_format"` // "json" or "text"
	Local      bool             `toml:"local"`      // Local development mode
}

// SMTPConfig holds SMTP server configuration
type SMTPConfig struct {
	ListenAddr             string                  `toml:"listen_addr"`
	Domain                 string                  `toml:"domain"`
	MaxMessageSize         int                     `toml:"max_message_size"`
	TimeoutSeconds         int                     `toml:"timeout_seconds"`          // SMTP command timeout in seconds (default: 10)
	MinTLSVersion          string                  `toml:"min_tls_version"`          // Minimum TLS version: "1.2" or "1.3" (TLS 1.0/1.1 not supported)
	CheckXSpamFlag         bool                    `toml:"check_x_spam_flag"`        // Enable check for X-Spam-Flag header
	DMARCQuarantineAsJunk  bool                    `toml:"dmarc_quarantine_as_junk"` // Treat DMARC quarantine policy as junk
	ARCEnabled             bool                    `toml:"arc_enabled"`              // Enable ARC (Authenticated Received Chain) validation (default: true)
	ARCSign                ARCSignConfig           `toml:"arc_sign"`                 // ARC signing configuration
	RequireSenderMX        bool                    `toml:"require_sender_mx"`        // Require sender domain to have MX records (default: true)
	ShutdownTimeoutSeconds int                     `toml:"shutdown_timeout_seconds"` // Maximum time to wait for graceful shutdown in seconds (default: 60)
	MaxConnections         int                     `toml:"max_connections"`          // Maximum total concurrent connections (0 = unlimited, default: 100)
	MaxConnectionsPerIP    int                     `toml:"max_connections_per_ip"`   // Maximum concurrent connections per IP (0 = unlimited, default: 10)
	RateLimit              RateLimitConfig         `toml:"rate_limit"`               // Rate limiting configuration
	Distributed            DistributedLimitsConfig `toml:"distributed"`              // Distributed connection tracking
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	Enabled               bool                 `toml:"enabled"`                 // Enable rate limiting (default: true)
	GossipEnabled         bool                 `toml:"gossip_enabled"`          // Share rate limit state across cluster via gossip (default: false)
	GossipIntervalSeconds int                  `toml:"gossip_interval_seconds"` // How often to gossip rate limit data in seconds (default: 5)
	Dimensions            []RateLimitDimension `toml:"dimensions"`              // Rate limit dimensions (e.g., IP, FROM, FROM_DOMAIN, etc.)
}

// RateLimitDimension defines a single rate limit dimension with configurable key combination
type RateLimitDimension struct {
	Name          string   `toml:"name"`           // Human-readable name for this dimension
	Keys          []string `toml:"keys"`           // Dimension keys to combine (IP, FROM, FROM_DOMAIN, TO, TO_DOMAIN)
	Limit         int      `toml:"limit"`          // Max connections/emails per window (0 = unlimited)
	WindowSeconds int      `toml:"window_seconds"` // Time window for rate limiting in seconds (default: 60)
}

// ARCSignConfig holds configuration for ARC signing
type ARCSignConfig struct {
	Enabled        bool   `toml:"enabled"`          // Enable ARC signing (default: false)
	Domain         string `toml:"domain"`           // Domain to sign with (e.g., "mail.example.com")
	Selector       string `toml:"selector"`         // DKIM selector for ARC signatures (e.g., "arc")
	PrivateKeyPath string `toml:"private_key_path"` // Path to RSA private key for signing (PEM format)
}

// DNSConfig holds DNS resolver configuration (global for all DNS operations)
type DNSConfig struct {
	Resolvers        []string `toml:"resolvers"`          // Custom DNS resolvers (e.g., ["1.1.1.1:53", "1.0.0.1:53"]), empty = use system default
	TimeoutSeconds   int      `toml:"timeout_seconds"`    // DNS query timeout in seconds (default: 5)
	CacheTTLSeconds  int      `toml:"cache_ttl_seconds"`  // DNS cache TTL in seconds for successful lookups (default: 300 = 5m)
	CacheNegativeTTL int      `toml:"cache_negative_ttl"` // DNS cache TTL in seconds for failed lookups (default: 60 = 1m)
}

// ClusterConfig holds global cluster/peering configuration (using memberlist)
type ClusterConfig struct {
	Enabled   bool     `toml:"enabled"`    // Enable cluster mode (memberlist)
	NodeName  string   `toml:"node_name"`  // This node's name (defaults to hostname)
	BindAddr  string   `toml:"bind_addr"`  // Address to bind memberlist to (e.g., "0.0.0.0")
	BindPort  int      `toml:"bind_port"`  // Port for memberlist (default: 7946)
	Peers     []string `toml:"peers"`      // Other cluster nodes to connect to (e.g., ["node1.example.com:7946"])
	SecretKey string   `toml:"secret_key"` // 32-byte base64-encoded encryption key (use CLUSTER_SECRET_KEY env var)
}

// DistributedLimitsConfig holds configuration for distributed connection tracking
type DistributedLimitsConfig struct {
	Enabled                  bool `toml:"enabled"`                     // Enable distributed tracking (requires cluster.enabled=true)
	GlobalMaxPerIP           int  `toml:"global_max_per_ip"`           // Global max connections per IP across cluster (0 = use local limit only)
	GossipIntervalSeconds    int  `toml:"gossip_interval_seconds"`     // How often to broadcast in seconds (default: 5)
	S3SyncIntervalSeconds    int  `toml:"s3_sync_interval_seconds"`    // How often to sync with S3 in seconds (default: 30)
	RecipientCacheTTLSeconds int  `toml:"recipient_cache_ttl_seconds"` // How long to cache recipient validation results in seconds (default: 900 = 15m)
}

// StorageConfig holds configuration for object storage (S3 or filesystem)
type StorageConfig struct {
	Backend         string `toml:"backend"`           // Storage backend: "s3" or "filesystem" (default: "s3")
	FilesystemPath  string `toml:"filesystem_path"`   // Path for filesystem backend (e.g., "/var/lib/mizu/storage")
	Endpoint        string `toml:"endpoint"`          // S3 endpoint
	Bucket          string `toml:"bucket"`            // S3 bucket name
	Prefix          string `toml:"prefix"`            // S3 key prefix
	AccessKeyID     string `toml:"access_key_id"`     // S3 access key
	SecretAccessKey string `toml:"secret_access_key"` // S3 secret key
	Region          string `toml:"region"`            // S3 region
}

// DeliveryConfig holds configuration for the HTTP delivery endpoint
type DeliveryConfig struct {
	URL                string               `toml:"url"`
	APIKey             string               `toml:"api_key"`
	MaxRetryAttempts   int                  `toml:"max_retry_attempts"`
	HTTPTimeoutSeconds int                  `toml:"http_timeout_seconds"` // HTTP client timeout in seconds (default: 30)
	CircuitBreaker     CircuitBreakerConfig `toml:"circuit_breaker"`
}

// CircuitBreakerConfig holds configuration for the circuit breaker
type CircuitBreakerConfig struct {
	Enabled             bool `toml:"enabled"`
	FailureThreshold    int  `toml:"failure_threshold"`     // failures before opening (default: 5)
	SuccessThreshold    int  `toml:"success_threshold"`     // successes in half-open to close (default: 2)
	TimeoutSeconds      int  `toml:"timeout_seconds"`       // time to wait before half-open in seconds (default: 30)
	HalfOpenMaxCalls    int  `toml:"half_open_max_calls"`   // max concurrent calls in half-open (default: 1)
	ResetTimeoutSeconds int  `toml:"reset_timeout_seconds"` // time before resetting counters in seconds (default: 60)
}

// ForwardingConfig holds configuration for the forwarding endpoint (used with routing)
type ForwardingConfig struct {
	Enabled            bool                 `toml:"enabled"`              // Enable forwarding functionality
	URL                string               `toml:"url"`                  // HTTP endpoint for forwarding
	APIKey             string               `toml:"api_key"`              // API key for authentication (or use FORWARDING_API_KEY env var)
	MaxRetryAttempts   int                  `toml:"max_retry_attempts"`   // Max retries (used by queue, default: 5)
	HTTPTimeoutSeconds int                  `toml:"http_timeout_seconds"` // HTTP client timeout in seconds (default: 30)
	CircuitBreaker     CircuitBreakerConfig `toml:"circuit_breaker"`      // Circuit breaker for forwarding endpoint
}

// QueueConfig holds configuration for async delivery queue (used with routing)
type QueueConfig struct {
	Enabled                bool          `toml:"enabled"`                  // Enable async queue (only when routing.enabled=true)
	DataDir                string        `toml:"data_dir"`                 // Directory for persistent queue storage (default: ./data/queue)
	Workers                int           `toml:"workers"`                  // Number of concurrent workers (default: 10)
	MaxRetryHours          int           `toml:"max_retry_hours"`          // Maximum hours to retry before giving up (default: 48)
	ShutdownTimeoutSeconds int           `toml:"shutdown_timeout_seconds"` // Max time to wait for graceful shutdown (default: 30)
	DeliveryTimeout        time.Duration // HTTP delivery timeout (set from http_timeout_seconds)
}

// HealthConfig holds configuration for the health check endpoint.
type HealthConfig struct {
	Enabled    bool   `toml:"enabled"`
	ListenAddr string `toml:"listen_addr"`
	Username   string `toml:"username"` // HTTP Basic Auth username (empty = no auth)
	Password   string `toml:"password"` // HTTP Basic Auth password
}

// MetricsConfig holds configuration for Prometheus metrics endpoint
type MetricsConfig struct {
	Enabled  bool   `toml:"enabled"`  // Enable Prometheus metrics endpoint
	Path     string `toml:"path"`     // Metrics endpoint path (default: "/metrics")
	Username string `toml:"username"` // HTTP Basic Auth username (optional, empty = no auth)
	Password string `toml:"password"` // HTTP Basic Auth password
}

// BlacklistsConfig holds configuration for DNS blacklists
type BlacklistsConfig struct {
	Enabled           bool     `toml:"enabled"`             // Whether to enable blacklist checking
	Lists             []string `toml:"lists"`               // DNS blacklist servers to check
	TimeoutSeconds    int      `toml:"timeout_seconds"`     // Timeout for blacklist queries in seconds (default: 3)
	CheckHELOResolves bool     `toml:"check_helo_resolves"` // Whether to check if HELO hostname resolves
}

// TLSConfig holds TLS/certificate configuration
type TLSConfig struct {
	Email            string   `toml:"email"`             // Email for Let's Encrypt
	Domains          []string `toml:"domains"`           // Domains to obtain certificates for
	UseProduction    bool     `toml:"use_production"`    // Use Let's Encrypt production (vs staging)
	UseLocalCA       bool     `toml:"use_local_ca"`      // Use local CA for testing
	CertMagicVerbose bool     `toml:"certmagic_verbose"` // Enable verbose certmagic logging
	EnableAutocert   bool     `toml:"enable_autocert"`   // Enable autocert for automatic certificate management
}

// RoutingConfig holds configuration for the optional routing/aliasing system
type RoutingConfig struct {
	Enabled                 bool                 `toml:"enabled"`                    // Enable routing lookups (default: false)
	Endpoint                string               `toml:"endpoint"`                   // HTTP endpoint for routing lookups (e.g., Cloudflare Worker)
	APIKey                  string               `toml:"api_key"`                    // API key for authentication (or use ROUTING_API_KEY env var)
	TimeoutMS               int                  `toml:"timeout_ms"`                 // Timeout for routing queries in milliseconds (default: 100)
	RetryAttempts           int                  `toml:"retry_attempts"`             // Number of retry attempts (default: 2)
	CacheTTLSeconds         int                  `toml:"cache_ttl_seconds"`          // Cache TTL for successful lookups (default: 300 = 5min)
	CacheNegativeTTLSeconds int                  `toml:"cache_negative_ttl_seconds"` // Cache TTL for failures (default: 60 = 1min)
	CacheMaxEntries         int                  `toml:"cache_max_entries"`          // Maximum cache entries (default: 50000)
	FallbackOnError         string               `toml:"fallback_on_error"`          // Behavior on routing error: "tempfail" (451) or "reject" (550)
	ValidateDuringRcpt      bool                 `toml:"validate_during_rcpt"`       // Validate recipient during RCPT TO (vs. DATA)
	CircuitBreaker          CircuitBreakerConfig `toml:"circuit_breaker"`            // Circuit breaker for routing endpoint
}

// StatsConfig holds configuration for IP and domain reputation tracking
// Note: Peer and hostname configuration is now in ClusterConfig (cluster.peers + cluster.hostname)
type StatsConfig struct {
	Enabled             bool `toml:"enabled"`               // Enable stats tracking
	RetentionSeconds    int  `toml:"retention_seconds"`     // How long to keep stats in seconds (default: 86400 = 24h)
	SyncEnabled         bool `toml:"sync_enabled"`          // Enable distributed stats sync (uses cluster.peers)
	SyncIntervalSeconds int  `toml:"sync_interval_seconds"` // How often to sync/export stats in seconds (default: 60)
	MaxIPEntries        int  `toml:"max_ip_entries"`        // Maximum number of IP entries to track (0 = unlimited, default: 100000)
	MaxDomainEntries    int  `toml:"max_domain_entries"`    // Maximum number of domain entries to track (0 = unlimited, default: 50000)
}

// DefaultConfig returns a Config with sensible default values
func DefaultConfig() Config {
	return Config{
		SMTP: SMTPConfig{
			ListenAddr:            ":25",              // Standard SMTP port
			Domain:                "mail.example.com", // Default domain
			MaxMessageSize:        10 * 1024 * 1024,   // Default 10MB max message size
			TimeoutSeconds:        10,                 // Default 10s SMTP timeout
			MinTLSVersion:         "1.2",              // Default to TLS 1.2 minimum
			CheckXSpamFlag:        true,               // Default to checking X-Spam-Flag header
			DMARCQuarantineAsJunk: true,               // Default to treating quarantine as junk
			ARCEnabled:            true,               // Default to ARC validation enabled
			ARCSign: ARCSignConfig{
				Enabled:        false,                       // Disabled by default
				Domain:         "",                          // Must be configured
				Selector:       "arc",                       // Default selector
				PrivateKeyPath: "/etc/mizu/arc-private.pem", // Default key path
			},
			RequireSenderMX:        true, // Default to requiring MX records
			ShutdownTimeoutSeconds: 60,   // Default 60s graceful shutdown
			MaxConnections:         100,  // Default max 100 concurrent connections
			MaxConnectionsPerIP:    10,   // Default max 10 connections per IP
			RateLimit: RateLimitConfig{
				Enabled:               true,  // Enabled by default
				GossipEnabled:         false, // Disabled by default (requires cluster mode)
				GossipIntervalSeconds: 5,     // Gossip every 5 seconds
				Dimensions: []RateLimitDimension{
					{
						Name:          "per_ip",
						Keys:          []string{"IP"},
						Limit:         60,
						WindowSeconds: 60,
					},
				},
			},
			Distributed: DistributedLimitsConfig{
				Enabled:                  false, // Disabled by default
				GlobalMaxPerIP:           0,     // Disabled by default
				GossipIntervalSeconds:    5,     // Gossip every 5 seconds
				S3SyncIntervalSeconds:    30,    // Sync with S3 every 30 seconds
				RecipientCacheTTLSeconds: 900,   // Cache recipient results for 15 minutes
			},
		},
		DNS: DNSConfig{
			Resolvers:        []string{}, // Empty = use system DNS
			TimeoutSeconds:   5,          // Default 5s DNS timeout
			CacheTTLSeconds:  300,        // Default 5m (300s) DNS cache TTL
			CacheNegativeTTL: 60,         // Default 1m (60s) negative cache TTL
		},
		Storage: StorageConfig{
			Backend:        "s3",                    // Default to S3
			FilesystemPath: "/var/lib/mizu/storage", // Default filesystem path
			Endpoint:       "s3.amazonaws.com",
			Bucket:         "email-mx-certs",
			Prefix:         "certs/",
			Region:         "us-east-1",
		},
		Delivery: DeliveryConfig{
			URL:                "https://your-worker.example.com/email",
			APIKey:             "your-api-key-here",
			MaxRetryAttempts:   3,
			HTTPTimeoutSeconds: 30, // Default 30s HTTP timeout
			CircuitBreaker: CircuitBreakerConfig{
				Enabled:             true,
				FailureThreshold:    5,
				SuccessThreshold:    2,
				TimeoutSeconds:      30,
				HalfOpenMaxCalls:    1,
				ResetTimeoutSeconds: 60,
			},
		},
		Forwarding: ForwardingConfig{
			Enabled:            false,                                      // Disabled by default
			URL:                "https://forward-worker.example.com/relay", // Example forwarding endpoint
			APIKey:             "",                                         // No API key by default
			MaxRetryAttempts:   5,                                          // 5 retries (handled by queue)
			HTTPTimeoutSeconds: 30,                                         // 30s timeout
			CircuitBreaker: CircuitBreakerConfig{
				Enabled:             true,
				FailureThreshold:    5,
				SuccessThreshold:    2,
				TimeoutSeconds:      30,
				HalfOpenMaxCalls:    1,
				ResetTimeoutSeconds: 60,
			},
		},
		Routing: RoutingConfig{
			Enabled:                 false,                                         // Disabled by default
			Endpoint:                "https://routing.example.workers.dev/resolve", // Example endpoint
			APIKey:                  "",                                            // No API key by default
			TimeoutMS:               100,                                           // 100ms timeout
			RetryAttempts:           2,                                             // 2 retries
			CacheTTLSeconds:         300,                                           // 5min cache for hits
			CacheNegativeTTLSeconds: 60,                                            // 1min cache for misses
			CacheMaxEntries:         50000,                                         // 50k cache entries
			FallbackOnError:         "tempfail",                                    // Temp fail on routing errors
			ValidateDuringRcpt:      true,                                          // Validate during RCPT TO
			CircuitBreaker: CircuitBreakerConfig{
				Enabled:             true, // Enabled by default to protect routing endpoint
				FailureThreshold:    10,   // 10 failures before opening (routing should be fast)
				SuccessThreshold:    3,    // 3 successes to close
				TimeoutSeconds:      5,    // Try again after 5s (routing is critical)
				HalfOpenMaxCalls:    2,    // Allow 2 concurrent test calls
				ResetTimeoutSeconds: 30,   // Reset counters after 30s
			},
		},
		Queue: QueueConfig{
			Enabled:                false,          // Disabled by default (only used with routing)
			DataDir:                "./data/queue", // Default data directory
			Workers:                10,             // 10 concurrent workers
			MaxRetryHours:          48,             // 48 hours retry window
			ShutdownTimeoutSeconds: 30,             // 30s shutdown timeout
		},
		TLS: TLSConfig{
			Email:            "admin@example.com",
			Domains:          []string{},
			UseProduction:    true,
			UseLocalCA:       false,
			CertMagicVerbose: false,
			EnableAutocert:   false,
		},
		Blacklists: BlacklistsConfig{
			Enabled:           true,
			Lists:             []string{"zen.spamhaus.org"},
			TimeoutSeconds:    3,     // Default 3s blacklist timeout
			CheckHELOResolves: false, // Default to not checking HELO resolves
		},
		Health: HealthConfig{
			Enabled:    true,
			ListenAddr: ":8080",
			Username:   "",
			Password:   "",
		},
		Metrics: MetricsConfig{
			Enabled:  true,
			Path:     "/metrics",
			Username: "",
			Password: "",
		},
		Stats: StatsConfig{
			Enabled:             true,
			RetentionSeconds:    86400,  // Default 24h (86400s) retention
			SyncEnabled:         false,  // Disabled by default
			SyncIntervalSeconds: 60,     // Default 1m (60s) sync interval
			MaxIPEntries:        100000, // 100k IP entries
			MaxDomainEntries:    50000,  // 50k domain entries
		},
		Cluster: ClusterConfig{
			Enabled:  false,
			NodeName: "",         // Auto-detected if empty
			BindAddr: "0.0.0.0",  // Bind to all interfaces
			BindPort: 7946,       // Standard memberlist port
			Peers:    []string{}, // No peers by default
		},
		LogFormat: "text",
		Local:     false,
	}
}
