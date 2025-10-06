# Mizu Configuration Documentation

This document provides detailed documentation for all configuration options in Mizu.

## Table of Contents

- [General Configuration](#general-configuration)
- [SMTP Configuration](#smtp-configuration)
- [Rate Limiting](#rate-limiting)
- [Distributed Tracking](#distributed-tracking)
- [DNS Configuration](#dns-configuration)
- [S3 Storage](#s3-storage)
- [Destination Configuration](#destination-configuration)
- [TLS Configuration](#tls-configuration)
- [Blacklist Configuration](#blacklist-configuration)
- [Health Check Configuration](#health-check-configuration)
- [Metrics Configuration](#metrics-configuration)
- [Statistics Configuration](#statistics-configuration)
- [Cluster Configuration](#cluster-configuration)

## General Configuration

### log_format
Controls the output format for application logs.
- **Options**: `"text"` or `"json"`
- **Default**: `"text"`
- **Recommendation**: Use `"json"` in production for structured logging and easier log aggregation.

### local
Enables local development mode which disables certain production checks and validations.
- **Type**: Boolean
- **Default**: `false`
- **Warning**: Never enable this in production as it bypasses important security checks.

## SMTP Configuration

The `[smtp]` section configures the SMTP server that receives incoming emails.

### listen_addr
The network address and port where the SMTP server listens for connections.
- **Format**: `"host:port"` or `":port"`
- **Default**: `":25"`
- **Examples**:
  - `":25"` - Listen on all interfaces, port 25
  - `"127.0.0.1:2525"` - Listen on localhost only, port 2525
  - `"0.0.0.0:25"` - Explicitly listen on all interfaces

### domain
The primary domain name of your mail server. This is used in SMTP responses and must match your DNS MX records.
- **Type**: String
- **Required**: Yes
- **Example**: `"mail.example.com"`

### max_message_size
Maximum allowed size for incoming email messages in bytes.
- **Type**: Integer
- **Default**: `10485760` (10 MB)
- **Recommendation**: Adjust based on your needs. Common values are 10-25 MB.

### timeout_seconds
Connection timeout for SMTP operations. If a connection is idle for this duration, it will be closed.
- **Type**: Integer
- **Default**: `10`
- **Range**: 5-60 seconds recommended

### min_tls_version
Minimum TLS version required for encrypted connections.
- **Options**: `"1.0"`, `"1.1"`, `"1.2"`, `"1.3"`
- **Default**: `"1.2"`
- **Recommendation**: Use `"1.2"` or higher for security compliance.

### check_x_spam_flag
When enabled, emails with the `X-Spam-Flag: YES` header will be rejected.
- **Type**: Boolean
- **Default**: `true`
- **Use case**: Reject emails already marked as spam by upstream filters.

### dmarc_quarantine_as_junk
Treats emails with DMARC policy of "quarantine" as junk/spam.
- **Type**: Boolean
- **Default**: `true`
- **Details**: DMARC policies include none, quarantine, and reject. This option handles the quarantine case.

### require_sender_mx
Requires the sender's domain to have valid MX records. Helps prevent spoofing.
- **Type**: Boolean
- **Default**: `true`
- **Security**: Recommended to keep enabled in production.

### shutdown_timeout_seconds
Maximum time to wait for graceful shutdown of the SMTP server.
- **Type**: Integer
- **Default**: `60`
- **Details**: Allows in-flight connections to complete before forcing shutdown.

### max_connections
Maximum number of concurrent SMTP connections allowed server-wide.
- **Type**: Integer
- **Default**: `100`
- **Recommendation**: Adjust based on your server's capacity and expected load.

### max_connections_per_ip
Maximum number of concurrent connections allowed from a single IP address.
- **Type**: Integer
- **Default**: `10`
- **Security**: Prevents resource exhaustion from a single source.

## Rate Limiting

The `[smtp.rate_limit]` section configures rate limiting to prevent abuse and spam.

### enabled
Master switch to enable or disable rate limiting.
- **Type**: Boolean
- **Default**: `true`

### gossip_enabled
Enables gossip protocol for sharing rate limit state across cluster nodes.
- **Type**: Boolean
- **Default**: `false`
- **Requirement**: Requires `[cluster]` configuration to be enabled.

### gossip_interval_seconds
How frequently rate limit state is synchronized via gossip protocol.
- **Type**: Integer
- **Default**: `5`
- **Range**: 1-60 seconds recommended

### Rate Limit Dimensions

Rate limit dimensions allow you to define multi-dimensional rate limiting rules. Each dimension tracks different aspects of incoming traffic.

#### name
A descriptive name for the rate limit dimension.
- **Type**: String
- **Examples**: `"per_ip"`, `"per_domain"`, `"per_recipient"`

#### keys
The attributes to track for this dimension.
- **Type**: Array of strings
- **Available keys**: `"IP"`, `"Domain"`, `"Recipient"`, `"Sender"`
- **Examples**:
  - `["IP"]` - Track by source IP
  - `["Domain"]` - Track by sender domain
  - `["IP", "Recipient"]` - Track combination of IP and recipient

#### limit
Maximum number of requests allowed within the time window.
- **Type**: Integer
- **Example**: `60` - Allow 60 emails

#### window_seconds
Time window in seconds for the rate limit.
- **Type**: Integer
- **Example**: `60` - Per 60-second window

**Example configuration for multiple dimensions:**
```toml
[[smtp.rate_limit.dimensions]]
name = "per_ip"
keys = ["IP"]
limit = 60
window_seconds = 60

[[smtp.rate_limit.dimensions]]
name = "per_domain"
keys = ["Domain"]
limit = 1000
window_seconds = 3600
```

## Distributed Tracking

The `[smtp.distributed]` section enables tracking across multiple server nodes using gossip protocol and S3.

### enabled
Enables distributed tracking for multi-node deployments.
- **Type**: Boolean
- **Default**: `false`
- **Requirement**: Requires S3 and cluster configuration.

### global_max_per_ip
Global maximum number of emails allowed per IP address across all nodes.
- **Type**: Integer
- **Default**: `0` (unlimited)
- **Use case**: Enforce stricter global limits when running multiple servers.

### gossip_interval_seconds
How frequently to gossip distributed tracking state between nodes.
- **Type**: Integer
- **Default**: `5`

### s3_sync_interval_seconds
How frequently to synchronize tracking state to S3.
- **Type**: Integer
- **Default**: `30`
- **Purpose**: Provides persistence and state recovery.

### recipient_cache_ttl_seconds
Time-to-live for recipient tracking cache entries.
- **Type**: Integer
- **Default**: `900` (15 minutes)

## DNS Configuration

The `[dns]` section configures DNS resolution for MX, SPF, DKIM, and DMARC lookups.

### resolvers
List of custom DNS resolver addresses. If empty, uses system default resolvers.
- **Type**: Array of strings
- **Default**: `[]` (use system default)
- **Format**: `"host:port"`
- **Example**: `["8.8.8.8:53", "1.1.1.1:53"]`

### timeout_seconds
Maximum time to wait for DNS query responses.
- **Type**: Integer
- **Default**: `5`
- **Range**: 2-10 seconds recommended

### cache_ttl_seconds
Time-to-live for positive DNS responses in the cache.
- **Type**: Integer
- **Default**: `300` (5 minutes)

### cache_negative_ttl
Time-to-live for negative DNS responses (NXDOMAIN) in the cache.
- **Type**: Integer
- **Default**: `60` (1 minute)
- **Purpose**: Reduces repeated queries for non-existent domains.

## S3 Storage

The `[s3]` section configures S3-compatible storage for TLS certificates and distributed state.

### endpoint
S3-compatible endpoint URL.
- **Type**: String
- **Default**: `"s3.amazonaws.com"`
- **Other options**: MinIO, DigitalOcean Spaces, Backblaze B2, etc.

### bucket
S3 bucket name where data is stored.
- **Type**: String
- **Required**: Yes

### prefix
Prefix (folder path) for stored objects within the bucket.
- **Type**: String
- **Default**: `"certs/"`
- **Purpose**: Organizes objects and allows bucket sharing.

### access_key_id
AWS/S3 access key ID for authentication.
- **Type**: String
- **Required**: Yes
- **Security**: Use IAM roles or secure secret management in production.

### secret_access_key
AWS/S3 secret access key for authentication.
- **Type**: String
- **Required**: Yes
- **Security**: Never commit this to version control.

### region
AWS region where the bucket is located.
- **Type**: String
- **Default**: `"us-east-1"`

## Destination Configuration

The `[destination]` section configures where processed emails are forwarded.

### url
Webhook URL where processed emails are sent via HTTP POST.
- **Type**: String (URL)
- **Required**: Yes
- **Example**: `"https://your-worker.example.com/email"`

### api_key
API key for authenticating requests to the destination webhook.
- **Type**: String
- **Required**: Yes
- **Security**: Use a strong, randomly generated key.

### max_retry_attempts
Number of times to retry failed deliveries.
- **Type**: Integer
- **Default**: `3`
- **Behavior**: Uses exponential backoff between retries.

### http_timeout_seconds
Maximum time to wait for HTTP requests to the destination.
- **Type**: Integer
- **Default**: `30`

### Circuit Breaker

The `[destination.circuit_breaker]` subsection prevents cascading failures.

#### enabled
Enables circuit breaker pattern for destination requests.
- **Type**: Boolean
- **Default**: `true`

#### failure_threshold
Number of consecutive failures before opening the circuit.
- **Type**: Integer
- **Default**: `5`

#### success_threshold
Number of consecutive successes needed to close the circuit.
- **Type**: Integer
- **Default**: `2`

#### timeout_seconds
Request timeout when the circuit is closed or half-open.
- **Type**: Integer
- **Default**: `30`

#### half_open_max_calls
Maximum concurrent requests allowed when circuit is half-open.
- **Type**: Integer
- **Default**: `1`
- **Purpose**: Tests if the service has recovered without overwhelming it.

#### reset_timeout_seconds
Time to wait before attempting to close an open circuit.
- **Type**: Integer
- **Default**: `60`

## TLS Configuration

The `[tls]` section configures automatic TLS certificate management via Let's Encrypt.

### email
Email address for Let's Encrypt notifications and account registration.
- **Type**: String (email)
- **Required**: Yes for Let's Encrypt

### domains
List of domains to automatically obtain TLS certificates for.
- **Type**: Array of strings
- **Example**: `["mail.example.com", "smtp.example.com"]`

### use_production
Use Let's Encrypt production servers vs. staging servers.
- **Type**: Boolean
- **Default**: `true`
- **Note**: Staging has higher rate limits but issues untrusted certificates (for testing).

### use_local_ca
Use a local certificate authority instead of Let's Encrypt.
- **Type**: Boolean
- **Default**: `false`
- **Use case**: Development and testing environments.

### certmagic_verbose
Enable verbose logging for the CertMagic certificate manager.
- **Type**: Boolean
- **Default**: `false`
- **Use case**: Troubleshooting certificate issues.

### enable_autocert
Enable automatic certificate provisioning and renewal.
- **Type**: Boolean
- **Default**: `false`
- **Requirement**: Requires S3 configuration for certificate storage.

## Blacklist Configuration

The `[blacklists]` section configures DNS-based blacklist (DNSBL) checking.

### enabled
Enables DNSBL checking for incoming connections.
- **Type**: Boolean
- **Default**: `true`

### lists
Array of DNSBL servers to query.
- **Type**: Array of strings
- **Default**: `["zen.spamhaus.org"]`
- **Common DNSBLs**:
  - `zen.spamhaus.org` - Spamhaus composite list
  - `bl.spamcop.net` - SpamCop
  - `b.barracudacentral.org` - Barracuda

### timeout_seconds
Maximum time to wait for DNSBL query responses.
- **Type**: Integer
- **Default**: `3`
- **Recommendation**: Keep low to avoid blocking legitimate mail.

### check_helo_resolves
Verify that the HELO/EHLO hostname provided by the client resolves via DNS.
- **Type**: Boolean
- **Default**: `false`
- **Use case**: Additional validation to detect misconfigured or spoofed servers.

## Health Check Configuration

The `[health]` section configures the health check and admin API endpoint.

### enabled
Enables the health check HTTP server.
- **Type**: Boolean
- **Default**: `true`

### listen_addr
Address and port for the health check and admin API.
- **Type**: String
- **Default**: `":8080"`
- **Endpoints available**:
  - `GET /health` - Health check
  - `GET /stats` - Statistics
  - Admin API endpoints (used by mizu-admin tool)

### username
HTTP Basic Authentication username.
- **Type**: String
- **Default**: `""` (no authentication)
- **Recommendation**: Set in production environments.

### password
HTTP Basic Authentication password.
- **Type**: String
- **Default**: `""` (no authentication)
- **Security**: Use a strong password in production.

## Metrics Configuration

The `[metrics]` section configures the Prometheus metrics endpoint.

### enabled
Enables the Prometheus metrics endpoint.
- **Type**: Boolean
- **Default**: `true`
- **Recommendation**: Enable in production for monitoring.

### path
URL path for the metrics endpoint.
- **Type**: String
- **Default**: `"/metrics"`

### username
HTTP Basic Authentication username for metrics endpoint.
- **Type**: String
- **Default**: `""` (no authentication)

### password
HTTP Basic Authentication password for metrics endpoint.
- **Type**: String
- **Default**: `""` (no authentication)
- **Recommendation**: Protect metrics endpoint in production.

## Statistics Configuration

The `[stats]` section configures real-time statistics collection.

### enabled
Enables statistics collection for emails, IPs, and domains.
- **Type**: Boolean
- **Default**: `true`

### retention_seconds
How long to retain statistics in memory.
- **Type**: Integer
- **Default**: `86400` (24 hours)

### sync_enabled
Enable synchronization of statistics to S3.
- **Type**: Boolean
- **Default**: `false`
- **Requirement**: Requires S3 configuration.

### sync_interval_seconds
How frequently to sync statistics to S3.
- **Type**: Integer
- **Default**: `60`

### max_ip_entries
Maximum number of IP address entries to track in statistics.
- **Type**: Integer
- **Default**: `100000`
- **Purpose**: Prevents unbounded memory growth.

### max_domain_entries
Maximum number of domain entries to track in statistics.
- **Type**: Integer
- **Default**: `50000`

## Cluster Configuration

The `[cluster]` section configures multi-node clustering using gossip protocol.

### enabled
Enables cluster mode for multi-node deployments.
- **Type**: Boolean
- **Default**: `false`

### node_name
Unique identifier for this node in the cluster.
- **Type**: String
- **Default**: `""` (auto-generated from hostname)

### bind_addr
IP address to bind the gossip protocol listener.
- **Type**: String
- **Default**: `"0.0.0.0"`

### bind_port
Port for gossip protocol communication.
- **Type**: Integer
- **Default**: `7946`
- **Protocol**: Uses memberlist/SWIM protocol.

### peers
List of peer node addresses to join when starting.
- **Type**: Array of strings
- **Format**: `"host:port"`
- **Example**: `["node1.example.com:7946", "node2.example.com:7946"]`

### secret_key
Encryption key for securing cluster communication.
- **Type**: String
- **Required**: Yes for production
- **Security**: Use a strong, randomly generated key shared across all nodes.
- **Recommendation**: Generate with `openssl rand -base64 32`
