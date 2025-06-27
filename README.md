# SMTP Relay Server

A high-performance SMTP relay server that listens on port 25, accepts incoming emails, and forwards them via HTTP POST to a configured endpoint (e.g., Cloudflare Worker). The server supports STARTTLS with automatic certificate issuance and management using Let's Encrypt, with certificate storage and coordination via S3.

## Features

- **SMTP Server**: Listens on port 25 for incoming email
- **STARTTLS Support**: Automatic TLS certificate issuance via Let's Encrypt
- **S3 Certificate Storage**: Distributed certificate management across multiple server instances
- **Domain Validation**: Dynamic domain whitelist fetched from S3/R2 with automatic refresh
- **HTTP Forwarding**: Converts SMTP messages to HTTP POST requests with retry support
- **SPF Validation**: Basic SPF checking for incoming emails
- **DMARC Validation**: Full DMARC policy enforcement with SPF and DKIM alignment
- **DNS Blacklists**: Support for RBL/DNSBL checking (e.g., Spamhaus)
- **Anti-Spam Features**: 
  - Reverse DNS validation
  - HELO hostname validation and resolution checking
  - Null sender rejection
  - Header validation (From, Date, Message-ID)
  - Duplicate header detection
  - Empty message rejection
  - Junk message flagging in HTTP headers
- **Modular Architecture**: Clean package structure for maintainability

## Package Structure

```
.
├── main.go                 # Main application entry point
├── pkg/
│   ├── config/            # Configuration constants
│   │   └── config.go
│   ├── smtp/              # SMTP server implementation
│   │   └── server.go
│   ├── storage/           # S3 certificate storage
│   │   └── s3.go
│   └── poster/            # HTTP destination communication
│       └── poster.go
├── go.mod
├── go.sum
└── README.md
```

### Package Descriptions

- **`pkg/config`**: Contains all configuration constants including SMTP settings, S3 configuration, and domain settings
- **`pkg/smtp`**: Implements the SMTP server backend and session handling, including anti-spam validations
- **`pkg/storage`**: Provides S3-compatible certificate storage for certmagic with distributed locking
- **`pkg/poster`**: Handles HTTP communication with retry support to the configured endpoint
- **`pkg/domains`**: Manages domain validation with dynamic whitelist fetching and caching
- **`pkg/validation`**: Implements DMARC, SPF, and DKIM validation for incoming emails
- **`pkg/blacklist`**: Provides DNS blacklist (RBL/DNSBL) checking functionality

## Configuration

The server supports multiple configuration methods with the following precedence (highest to lowest):
1. Command line flags
2. Configuration file (TOML)
3. Environment variables
4. Default values

### Configuration File

Create a `config.toml` file (or use `./smtp-relay generate-config` to create an example):

```toml
log_format = "text"  # or "json"
local = false

[smtp]
listen_addr = ":25"
domain = "mail.example.com"
max_message_size = 10485760  # 10MB
timeout_duration = "10s"
min_tls_version = "1.2"  # Minimum TLS version (1.2 or 1.3)

[s3]
endpoint = "s3.amazonaws.com"
bucket = "email-mx-certs"
prefix = "certs/"
access_key_id = "your-s3-access-key-id"
secret_access_key = "your-s3-secret-access-key"
region = "us-east-1"

[destination]
url = "https://your-worker.example.com/email"
api_key = "your-api-key-here"
max_retry_attempts = 3  # Number of retry attempts before giving up

[domains]
url = "https://your-bucket.r2.dev/valid-domains.json"  # URL or local file path
api_key = "your-domains-api-key"  # Optional API key for authentication

[blacklists]
enabled = true  # Enable DNS blacklist checking
lists = ["zen.spamhaus.org"]  # DNS blacklist servers to check
timeout = "3s"  # Timeout for blacklist queries
check_helo_resolves = false  # Whether to verify HELO hostname resolves

[tls]
email = "admin@example.com"  # For Let's Encrypt
use_production = true        # Use Let's Encrypt production (vs staging)
use_local_ca = false        # Use local CA for testing
certmagic_verbose = false   # Enable verbose certmagic logging
```

### Command Line Flags

```bash
# Show all available flags
./smtp-relay --help

# Example with flags
./smtp-relay --smtp.domain=mail.mydomain.com \
             --destination.url=https://my-worker.com/email \
             --tls.email=admin@mydomain.com \
             --log-format=json

# Local development mode
./smtp-relay --local --smtp.domain=test.local
```

### Environment Variables

For security, sensitive values should be set via environment variables:

- `S3_ACCESS_KEY_ID`: access key ID for S3 certificate storage
- `S3_SECRET_ACCESS_KEY`: secret access key for S3 certificate storage
- `DESTINATION_API_KEY`: API key for authenticating with the destination endpoint
- `VALID_DOMAINS_URL`: URL or file path to the valid domains list

## Building

```bash
go build -o smtp-relay .
```

## Running

```bash
./smtp-relay
```

## Dependencies

- **github.com/caddyserver/certmagic**: Automatic HTTPS certificate management
- **github.com/emersion/go-smtp**: SMTP server implementation
- **github.com/mileusna/spf**: SPF record validation
- **github.com/minio/minio-go/v7**: S3-compatible storage client
- **go.uber.org/zap**: Structured logging

## Domain Validation

The server validates recipient domains against a whitelist that can be loaded from:
- **HTTP/HTTPS URL**: e.g., S3/R2 bucket (`https://bucket.r2.dev/domains.json`)
- **Local file path**: e.g., `/path/to/domains.json` or `./domains.json`

### Domain List Format

The domains list should be a JSON array stored at the configured URL:

```json
[
  "example.com",
  "customer1.com",
  "customer2.org"
]
```

### Features

- **Initial Load**: Domains are fetched on startup (server won't start if fetch fails)
- **Automatic Refresh**: List is refreshed every minute in the background
- **ETag Support**: Uses HTTP ETag headers to minimize bandwidth usage
- **Case Insensitive**: Domain matching is case-insensitive
- **Graceful Failures**: If refresh fails, the server continues with the last known good list
- **Availability Protection**: Server rejects new SMTP sessions with 4xx error if domain list has never been successfully loaded
- **Stale List Handling**: If domain refresh fails, unknown domains get temporary 4xx error (not permanent rejection)
- **Local Mode**: In local mode, only accepts emails for the configured SMTP domain (no external list required)

## Architecture

1. **SMTP Reception**: The server listens on port 25 and accepts incoming SMTP connections
2. **TLS/STARTTLS**: Automatic certificate issuance and renewal via Let's Encrypt
3. **Certificate Storage**: Certificates are stored in S3 with distributed locking for multi-instance deployments
4. **Domain Validation**: Valid recipient domains are fetched from S3/R2 URL on startup and refreshed every minute
5. **Email Processing**: Incoming emails undergo SPF validation and domain whitelist checking
6. **HTTP Forwarding**: Valid emails are converted to HTTP POST requests and sent to the configured endpoint
7. **Error Handling**: Failed deliveries are logged (production systems should implement retry queues)

### Domain Validation Behavior

- **Normal Operation**: When domain list is fresh, unknown domains are permanently rejected (5xx)
- **Stale List**: When domain refresh fails, unknown domains get temporary rejection (4xx "try again later")
- **Never Loaded**: If domain list was never successfully loaded, all new sessions are rejected (4xx)
- **Local Mode**: Only accepts emails for the configured SMTP domain

## Security Features

- **Mandatory STARTTLS**: All email transmissions must use TLS encryption (no plaintext allowed)
- **SPF Validation**: Basic SPF checking for sender validation
- **Domain Whitelist**: Dynamic domain validation with S3-hosted whitelist
- **Automatic Domain Refresh**: Domain list updates every minute with ETag support
- **TLS Enforcement**: Modern TLS versions (1.2+) required
- **SMTPUTF8 Support**: Full support for international email addresses and content
- **Message Size Limits**: Configurable maximum message size (default: 10MB)

## Local Development Mode

The server supports a local development mode that's useful for testing without requiring TLS certificates or external services:

```bash
# Run with default domain (localhost)
./smtp-relay --local

# Or specify a custom domain
./smtp-relay --local --smtp.domain=test.local

# Or use a domains list in local mode
./smtp-relay --local --domains-url=/path/to/domains.json

# Or load domains from URL in local mode
./smtp-relay --local --domains-url=https://example.com/domains.json
```

In local mode:
- **No TLS/STARTTLS required**: Accepts plaintext SMTP connections
- **No certificate management**: Doesn't attempt to issue or fetch certificates
- **No S3 required**: Doesn't need S3 credentials or bucket configuration
- **No HTTP POST**: Instead of posting to a destination endpoint, dumps email content to terminal
- **Domain validation**: 
  - If `domains_url` is provided, loads domains from URL or file (same as production)
  - If not provided, only accepts emails for the configured SMTP domain
- **Default domain**: Automatically uses "localhost" if no domain is specified

Example local mode testing with telnet:
```bash
telnet localhost 25
EHLO test.local
MAIL FROM: <sender@example.com>
RCPT TO: <recipient@test.local>
DATA
Subject: Test Email

This is a test message.
.
QUIT
```
