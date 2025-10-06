# Mizu - High-Performance, Distributed SMTP Relay Server

Mizu is a production-ready, high-performance SMTP relay server designed for reliability and security. It accepts incoming emails via SMTP and synchronously forwards them to a configured backend via HTTP POST. Mizu guarantees zero message loss by confirming successful delivery to your backend before acknowledging receipt to the sender.

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## ✨ Key Features

### 🔒 Security & Anti-Spam

- **Mandatory STARTTLS** with automatic Let's Encrypt certificate management.
- **SPF, DKIM & DMARC Validation** with alignment checking.
- **DNS Blacklists (RBL/DNSBL)** support (e.g., Spamhaus).
- **Reverse DNS and Sender MX Validation**.
- **Header Validation** requiring `From`, `Date`, and `Message-ID`.
- **Null Sender Rejection** to block empty envelope senders.
- **Custom DNS Resolvers** to globally use providers like Cloudflare or Google.

### 🛡️ DoS Protection & Rate Limiting

- **Connection Limiting**: Global and per-IP concurrent connection limits.
- **Rate Limiting**: Sliding window algorithm (connections/minute per IP).
- **Distributed State**: Rate limiting and connection tracking state is shared across the cluster using a P2P gossip protocol for cluster-wide enforcement.
- **Circuit Breaker**: Protects your backend from being overwhelmed during failures and automatically recovers.

### 📊 Reputation & Intelligence

- **Real-time Reputation Tracking**: Scores IPs and domains to identify bad actors.
- **Distributed Stats Sync**: Reputation data is shared across the cluster via S3.
- **Automatic Blocking**: Malicious actors are automatically blocked based on reputation.
- **Recipient Caching**: Caches backend responses (e.g., 404/403) cluster-wide to reduce redundant checks.

### 🔄 High Availability & Resilience

- **Distributed by Design**: Built for multi-instance deployments with P2P coordination.
- **Graceful Shutdown**: Waits for active sessions to complete before shutting down.
- **Health Monitoring**: HTTP health check endpoints provide detailed component status.
- **Panic Recovery**: Prevents server crashes and WaitGroup leaks, with stack trace logging.

### 🔧 Operational Excellence

- **Structured Logging**: JSON or text format with trace IDs for easy correlation.
- **Admin CLI**: A dedicated tool (`mizu-admin`) for health checks, stats, and operational tasks.
- **HTTP Basic Auth**: Protects health and API endpoints.
- **Comprehensive Testing**: Over 100 tests, including integration and E2E tests.

## 🚀 Quick Start

### 1. Installation

```bash
# Clone the repository
git clone https://github.com/[organization]/mizu.git
cd mizu

# Build the server and admin CLI
go build -o ./bin/mizu ./cmd/mizu-server
go build -o ./bin/mizu-admin ./cmd/mizu-admin

# The binaries will be in the ./bin directory
```

### 2. Configuration

Generate an example configuration file:
```bash
./bin/mizu generate-config > config.toml
```

Edit `config.toml` with your settings. A minimal configuration requires:
```toml
[smtp]
domain = "mail.example.com"

[destination]
url = "https://your-backend.example.com/email"
api_key = "your-api-key" # Use env var in production

[tls]
email = "admin@example.com" # For Let's Encrypt

[s3]
bucket = "your-s3-bucket-for-certs-and-stats"
access_key_id = "YOUR_KEY"      # Or use env: S3_ACCESS_KEY_ID
secret_access_key = "YOUR_SECRET"  # Or use env: S3_SECRET_ACCESS_KEY
```

### 3. Run Mizu

```bash
# Run in production mode
./bin/mizu --config ./config.toml

# Run in local development mode (disables TLS, dumps emails to terminal)
./bin/mizu --local
```

## 📋 Configuration Reference

For a complete list of all configuration options and their descriptions, please see the fully documented [config.toml.example](config.toml.example) file.

### Environment Variables

For enhanced security, critical secrets like API keys and S3 credentials can be provided via environment variables.

- `DESTINATION_API_KEY`: API key for the backend endpoint.
- `S3_ACCESS_KEY_ID`: AWS access key ID.
- `S3_SECRET_ACCESS_KEY`: AWS secret access key.
- `HEALTH_PASSWORD`: Password for the health/API endpoints if basic auth is enabled.

## 🚀 Production Deployment

For production, we recommend running a cluster of at least 3 Mizu instances for high availability.

- **Load Balancing**: Use DNS round-robin to distribute SMTP traffic across your instances.
- **Cluster Mode**: Enable cluster mode in the configuration to allow instances to coordinate via P2P gossip.
- **Shared Storage**: Configure all instances to use the same S3 bucket for distributed certificate management and reputation stats.
- **Systemd**: Run Mizu as a `systemd` service for automatic restarts.

See the **Production Checklist** in the [Architecture](#-architecture) section for more details.

## 📊 Monitoring & Observability

### Health Check Endpoint

When enabled, Mizu exposes an HTTP server for health checks and operational data.
```bash
# Check application health (add -u user:pass if auth is enabled)
curl http://localhost:8080/health

# View real-time server statistics
curl http://localhost:8080/api/stats

# Flush the recipient cache across the cluster
curl -X POST http://localhost:8080/api/flush-cache
```

### Logging

Mizu supports structured `json` or human-readable `text` logging. JSON logs include a `trace_id` for each session, making it easy to correlate events.

### Key Metrics to Monitor
- `mizu_active_sessions`: Number of active SMTP sessions.
- `mizu_circuit_breaker_state`: State of the circuit breaker (Closed, Open, HalfOpen).
- `mizu_rate_limit_exceeded`: Number of connections blocked by rate limiting.
- `mizu_reputation_blocked`: Number of connections blocked due to poor reputation.
- DMARC/SPF/DKIM validation failures.

## 🔧 Admin CLI

Mizu includes a command-line tool, `mizu-admin`, for operational tasks.

```bash
# Check health of a remote Mizu instance
./bin/mizu-admin health --addr http://localhost:8080

# View stats
./bin/mizu-admin stats --addr http://localhost:8080

# List blocked IPs
./bin/mizu-admin blocked-ips --addr http://localhost:8080

# Flush recipient cache (distributed mode)
./bin/mizu-admin flush-cache --addr http://localhost:8080

# Use with authentication
./bin/mizu-admin health --addr http://localhost:8080 -u admin -p changeme
```

## 🧪 Testing

Mizu has a comprehensive test suite.

```bash
# Run all tests
make test

# Run tests with the race detector
go test -race ./...

# Run integration tests for the SMTP package
go test ./pkg/smtp -run E2E -v
```

You can also perform a manual test using `telnet` while Mizu is running in local mode.
```bash
./bin/mizu --local &
telnet localhost 25
```
Example SMTP session:
```
EHLO test.local
MAIL FROM:<sender@example.com>
RCPT TO:<recipient@example.com>
DATA
Subject: Test Email

This is a test.
.
QUIT
```

## 🏗️ Architecture

```
┌─────────────┐
│   Internet  │
└──────┬──────┘
       │ SMTP (port 25, STARTTLS)
       ▼
┌─────────────────────────────────────────┐
│         Mizu SMTP Relay Cluster         │
│                                         │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐ │
│  │ Mizu #1 │  │ Mizu #2 │  │ Mizu #3 │ │
│  └─────────┘  └─────────┘  └─────────┘ │
│       │            │            │       │
│       └────────────┼────────────┘       │
│              P2P Gossip                 │
│ (Connections, Rate Limits, Reputation)  │
└─────────────────┬───────────────────────┘
                  │
        ┌─────────┴─────────┐
        │                   │
        ▼                   ▼
   ┌─────────┐         ┌─────────┐
   │   S3    │         │  HTTP   │
   │  Certs  │         │ Backend │
   │  Stats  │         │Endpoint │
   └─────────┘         └─────────┘
```

### Core Design Principles

1.  **Zero Message Loss**: The SMTP `250 OK` response is sent **only after** receiving a successful HTTP `200` or `202` from the destination backend.
2.  **Synchronous Delivery**: Messages are delivered during the SMTP session without internal queues, providing immediate feedback.
3.  **Production Ready**: Features comprehensive error handling, panic recovery, and graceful shutdowns.
4.  **Distributed Architecture**: Designed from the ground up for multi-instance deployments.
5.  **Security First**: Enforces TLS and a strict regimen of anti-spam and sender validation checks.

### Message Flow

1.  **SMTP Reception**: Accept connection and check against connection and rate limits.
2.  **Security Validation**: Perform rDNS, DNSBL, SPF, and DMARC checks.
3.  **Content Validation**: Validate headers, size, and check for duplicates.
4.  **Synchronous Delivery**: Send the message to the destination via HTTP POST, with retries.
5.  **SMTP Response**: Respond with `250 OK` only if the HTTP delivery was successful.
6.  **Stats Recording**: Update reputation scores and sync to the cluster.

## 🐛 Known Limitations

- **No Internal Message Queue**: Delivery is synchronous and blocks the SMTP session by design. If the backend is down, emails are rejected (SMTP 4xx), and the sending server is expected to retry.
- **Single Destination**: Each Mizu instance can forward to only one HTTP endpoint.
- **No Prometheus Metrics**: The server currently exposes metrics via a JSON API only. Prometheus support is a planned enhancement.

## 🤝 Contributing

Contributions are welcome! Before submitting a pull request, please:

1.  Review the architecture and design principles.
2.  Ensure all existing tests pass by running `make test`.
3.  Add new tests for any new features or bug fixes.
4.  Follow the existing code style and patterns.
5.  Update documentation if necessary.

## 📄 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## 🙏 Credits

Mizu is built on several excellent open-source libraries, including:
- [caddyserver/certmagic](https://github.com/caddyserver/certmagic) for automatic TLS.
- [emersion/go-smtp](https://github.com/emersion/go-smtp) for the core SMTP server implementation.
- [emersion/go-msgauth](https://github.com/emersion/go-msgauth) for DMARC, DKIM, and SPF validation.
- [minio/minio-go](https://github.com/minio/minio-go) for the S3 client.
- [uber-go/zap](https://github.com/uber-go/zap) for structured logging.