package config

import "time"

// Config holds all configuration for the SMTP server(s)
type Config struct {
	Servers    []ServerConfig   `toml:"server"`   // Multiple SMTP server instances
	Defaults   DefaultsConfig   `toml:"defaults"` // Default values for all servers
	Logging    LoggingConfig    `toml:"logging"`  // Logging configuration
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
	Cluster    ClusterConfig    `toml:"cluster"` // Global cluster/peering configuration
	Local      bool             `toml:"local"`   // Local development mode (disables TLS and validation)
}

// DefaultsConfig provides default values that can be overridden per-server
type DefaultsConfig struct {
	Domain                 string `toml:"domain"`                   // Default domain for all servers
	MaxMessageSize         int    `toml:"max_message_size"`         // Default max message size in bytes
	TimeoutSeconds         int    `toml:"timeout_seconds"`          // Default SMTP command timeout
	ShutdownTimeoutSeconds int    `toml:"shutdown_timeout_seconds"` // Default graceful shutdown timeout
	MaxConnections         int    `toml:"max_connections"`          // Default max total connections per server
}

// ServerConfig defines a single SMTP server instance
type ServerConfig struct {
	// === Identity ===
	Name string `toml:"name"` // Human-readable name (e.g., "mx-primary", "submission-tls")
	Type string `toml:"type"` // "relay" (MX server) or "submission" (MSA server)

	// === Network ===
	ListenAddr string `toml:"listen_addr"` // Address to bind (e.g., ":25", ":465", ":587", "127.0.0.1:2525")
	Domain     string `toml:"domain"`      // Server domain (overrides defaults.domain if set)

	// === Message Processing ===
	MaxMessageSize int `toml:"max_message_size"` // Maximum message size in bytes (overrides default)

	// === Connection Limits ===
	Limits ServerLimitsConfig `toml:"limits"` // Connection limits

	// === TLS Configuration ===
	TLS ServerTLSConfig `toml:"tls"` // TLS configuration (if section present, TLS is enabled)

	// === Timeouts ===
	TimeoutSeconds         int `toml:"timeout_seconds"`          // SMTP command timeout (overrides default)
	ShutdownTimeoutSeconds int `toml:"shutdown_timeout_seconds"` // Graceful shutdown timeout (overrides default)

	// === Nested Configuration Sections ===
	Submission ServerSubmissionConfig `toml:"submission"` // Submission-specific settings (for type="submission")
	Auth       ServerAuthConfig       `toml:"auth"`       // Authentication configuration (use auth.required=true to require auth)
	DNSChecks  ServerDNSChecksConfig  `toml:"dns_checks"` // DNS validation checks (rDNS, MX)
	Junk       ServerJunkConfig       `toml:"junk"`       // Junk/spam detection configuration
	SPF        ServerSPFConfig        `toml:"spf"`        // SPF validation configuration
	DKIM       ServerDKIMConfig       `toml:"dkim"`       // DKIM validation and signing configuration
	DMARC      ServerDMARCConfig      `toml:"dmarc"`      // DMARC validation configuration
	ARC        ServerARCConfig        `toml:"arc"`        // ARC validation and signing configuration
	DNSBL      ServerDNSBLConfig      `toml:"dnsbl"`      // DNS blacklist checking configuration

	// === Rate Limiting (per-server) ===
	RateLimit   RateLimitConfig         `toml:"rate_limit"`  // Rate limiting configuration
	Distributed DistributedLimitsConfig `toml:"distributed"` // Distributed connection tracking
}

// ServerLimitsConfig holds connection limits
type ServerLimitsConfig struct {
	MaxConnections        int `toml:"max_connections"`          // Total connections for this server
	MaxConnectionsPerIP   int `toml:"max_connections_per_ip"`   // Per-IP connection limit (for DoS protection)
	MaxConnectionsPerUser int `toml:"max_connections_per_user"` // Per-user connection limit (for submission servers)
}

// ServerSubmissionConfig holds settings specific to submission servers (type="submission")
type ServerSubmissionConfig struct {
	AllowNullSender   bool `toml:"allow_null_sender"`   // Allow bounce messages with null sender (<>)
	FixMissingHeaders bool `toml:"fix_missing_headers"` // Add missing Message-ID and Date headers
}

// ServerAuthConfig holds authentication configuration for submission servers
// Authentication is performed via HTTPS POST to the configured endpoint
type ServerAuthConfig struct {
	Required bool   `toml:"required"` // Require authentication (typically true for submission servers)
	Endpoint string `toml:"endpoint"` // HTTPS endpoint for authentication (must use https://)
	APIKey   string `toml:"api_key"`  // API key for authentication (sent as Bearer token, can use env var: ${AUTH_API_KEY})
}

// ServerSPFConfig holds SPF validation configuration
type ServerSPFConfig struct {
	Enabled bool `toml:"enabled"` // Enable SPF validation
}

// ServerDKIMConfig holds DKIM validation and signing configuration
type ServerDKIMConfig struct {
	Enabled        bool   `toml:"enabled"`          // Enable DKIM (validation or signing)
	Mode           string `toml:"mode"`             // Mode: "check" (validate incoming) or "sign" (sign outgoing)
	Domain         string `toml:"domain"`           // Domain for DKIM signature (d=) - required for mode=sign
	Selector       string `toml:"selector"`         // DKIM selector (s=) - required for mode=sign
	PrivateKeyPath string `toml:"private_key_path"` // Path to RSA private key (PEM format) - required for mode=sign
}

// ServerDMARCConfig holds DMARC validation configuration
type ServerDMARCConfig struct {
	Enabled                bool   `toml:"enabled"`                  // Enable DMARC validation
	RejectPolicyAction     string `toml:"reject_policy_action"`     // Action for policy=reject: "none", "reject", "junk" (default: "reject")
	QuarantinePolicyAction string `toml:"quarantine_policy_action"` // Action for policy=quarantine: "none", "reject", "junk" (default: "junk")
}

// ServerARCConfig holds ARC validation and signing configuration
type ServerARCConfig struct {
	Enabled        bool   `toml:"enabled"`          // Enable ARC (validation or signing)
	Mode           string `toml:"mode"`             // Mode: "check" (validate incoming) or "sign" (sign outgoing/forward)
	Domain         string `toml:"domain"`           // Domain for ARC signature - required for mode=sign
	Selector       string `toml:"selector"`         // ARC selector - required for mode=sign
	PrivateKeyPath string `toml:"private_key_path"` // Path to RSA private key for ARC - required for mode=sign
}

// ServerDNSBLConfig holds DNS blacklist checking configuration
type ServerDNSBLConfig struct {
	Enabled           bool     `toml:"enabled"`             // Enable DNS blacklist (RBL) checking
	Lists             []string `toml:"lists"`               // DNS blacklist servers to check
	TimeoutSeconds    int      `toml:"timeout_seconds"`     // Timeout for blacklist queries in seconds (default: 3)
	CheckHELOResolves bool     `toml:"check_helo_resolves"` // Whether to check if HELO hostname resolves
	Action            string   `toml:"action"`              // Action when blacklisted: "reject", "junk", "none" (default: "reject")
}

// ServerDNSChecksConfig holds DNS validation checks
type ServerDNSChecksConfig struct {
	RequireRDNS     bool `toml:"require_rdns"`      // Require reverse DNS for sender IP
	RequireSenderMX bool `toml:"require_sender_mx"` // Require sender domain to have MX records
}

// ServerJunkConfig holds junk/spam detection configuration
type ServerJunkConfig struct {
	RejectNullSender bool     `toml:"reject_null_sender"` // Reject bounce messages (<>)
	CheckHeaders     []string `toml:"check_headers"`      // Headers that mark email as junk
	ApplyAction      string   `toml:"apply_action"`       // Action when junk detected: "header", "reject", "warn", "subject"
	SubjectPattern   string   `toml:"subject_pattern"`    // Subject pattern for "subject" action (e.g., "[spam] %s")
	Header           string   `toml:"header"`             // Header to add for "header" action (e.g., "X-Spam")
}

// ServerTLSConfig holds TLS configuration for a server
type ServerTLSConfig struct {
	Enabled              bool   `toml:"enabled"`                // Enable TLS for this server
	Mode                 string `toml:"mode"`                   // TLS mode: "starttls" or "implicit"
	Required             bool   `toml:"required"`               // Enforce TLS (reject unencrypted connections)
	MinTLSVersion        string `toml:"min_tls_version"`        // Minimum TLS version: "1.2" or "1.3"
	DeferredHandshake    bool   `toml:"deferred_handshake"`     // Defer TLS handshake to prevent head-of-line blocking (implicit TLS only)
	HandshakeTimeoutSecs int    `toml:"handshake_timeout_secs"` // Timeout for TLS handshake in seconds (default: 10, 0 = no timeout)
}

// IsRelay returns true if this is a relay (MX) server
func (s *ServerConfig) IsRelay() bool {
	return s.Type == "relay"
}

// IsSubmission returns true if this is a submission (MSA) server
func (s *ServerConfig) IsSubmission() bool {
	return s.Type == "submission"
}

// IsTLSEnabled returns true if TLS is explicitly enabled
func (s *ServerConfig) IsTLSEnabled() bool {
	return s.TLS.Enabled
}

// UsesSTARTTLS returns true if TLS mode is "starttls"
func (s *ServerConfig) UsesSTARTTLS() bool {
	return s.TLS.Mode == "starttls"
}

// UsesImplicitTLS returns true if TLS mode is "implicit"
func (s *ServerConfig) UsesImplicitTLS() bool {
	return s.TLS.Mode == "implicit"
}

// ApplyDefaults fills in missing values from defaults
func (s *ServerConfig) ApplyDefaults(defaults DefaultsConfig) {
	if s.Domain == "" {
		s.Domain = defaults.Domain
	}
	if s.MaxMessageSize == 0 {
		s.MaxMessageSize = defaults.MaxMessageSize
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if s.ShutdownTimeoutSeconds == 0 {
		s.ShutdownTimeoutSeconds = defaults.ShutdownTimeoutSeconds
	}
	if s.Limits.MaxConnections == 0 {
		s.Limits.MaxConnections = defaults.MaxConnections
	}
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
	SRS                SRSConfig            `toml:"srs"`                  // Sender Rewriting Scheme configuration
}

// SRSConfig holds configuration for Sender Rewriting Scheme (SRS)
type SRSConfig struct {
	Enabled bool   `toml:"enabled"` // Enable SRS for forwarded emails
	Domain  string `toml:"domain"`  // Domain to use for SRS addresses (e.g., "relay.mizu.com")
	Secret  string `toml:"secret"`  // Secret key for SRS HMAC (or use SRS_SECRET env var)
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

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `toml:"level"`  // Log level: debug, info, warn, error (default: info)
	Format string `toml:"format"` // Output format: console (human-readable) or json (structured) (default: console)
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
		Defaults: DefaultsConfig{
			Domain:                 "mail.example.com",
			MaxMessageSize:         10 * 1024 * 1024, // 10MB
			TimeoutSeconds:         10,
			ShutdownTimeoutSeconds: 60,
			MaxConnections:         100,
		},
		Servers: []ServerConfig{
			// Default MX relay server
			{
				Name:       "default-relay",
				Type:       "relay",
				ListenAddr: ":25",
				TLS: ServerTLSConfig{
					Enabled:  true,
					Mode:     "starttls",
					Required: false,
				},
				SPF: ServerSPFConfig{
					Enabled: true,
				},
				DKIM: ServerDKIMConfig{
					Enabled: true,
					Mode:    "check",
				},
				DMARC: ServerDMARCConfig{
					Enabled:                true,
					RejectPolicyAction:     "reject",
					QuarantinePolicyAction: "junk",
				},
				ARC: ServerARCConfig{
					Enabled: true,
					Mode:    "check",
				},
				DNSBL: ServerDNSBLConfig{
					Enabled: true,
				},
				DNSChecks: ServerDNSChecksConfig{
					RequireRDNS:     true,
					RequireSenderMX: true,
				},
				Junk: ServerJunkConfig{
					RejectNullSender: true,
					CheckHeaders:     []string{"X-Spam-Flag", "X-Junk"},
					ApplyAction:      "header",
					SubjectPattern:   "[spam] %s",
					Header:           "X-Spam",
				},
				MaxMessageSize: 10 * 1024 * 1024, // 10MB
				Limits: ServerLimitsConfig{
					MaxConnections:      100,
					MaxConnectionsPerIP: 10,
				},
				RateLimit: RateLimitConfig{
					Enabled:               true,
					GossipEnabled:         false,
					GossipIntervalSeconds: 5,
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
					Enabled:                  false,
					GlobalMaxPerIP:           0,
					GossipIntervalSeconds:    5,
					S3SyncIntervalSeconds:    30,
					RecipientCacheTTLSeconds: 900,
				},
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
		Logging: LoggingConfig{
			Level:  "info",
			Format: "console",
		},
		Local: false,
	}
}
