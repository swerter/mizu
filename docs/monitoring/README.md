# Mizu Monitoring Guide

This directory contains documentation for monitoring and observability of the Mizu SMTP relay server.

## Overview

Mizu provides comprehensive monitoring through three channels:

1. **Prometheus Metrics** - `/metrics` endpoint for time-series data
2. **Health Endpoint** - `/health` endpoint for component health checks
3. **Admin CLI** - `mizu-admin` tool for operational tasks

## Quick Start

### Enable Metrics Endpoint

```toml
[metrics]
enabled = true
listen_addr = "localhost:9090"
path = "/metrics"
username = "admin"           # Optional: HTTP Basic Auth
password = "secret"           # Optional: HTTP Basic Auth
```

### Enable Health Endpoint

```toml
[health]
enabled = true
listen_addr = "localhost:8080"
username = "admin"            # Optional: HTTP Basic Auth
password = "secret"           # Optional: HTTP Basic Auth
```

### Access Endpoints

```bash
# Prometheus metrics
curl -u admin:secret http://localhost:9090/metrics

# Health check
curl -u admin:secret http://localhost:8080/health

# Component stats
curl -u admin:secret http://localhost:8080/api/stats

# Admin CLI
mizu-admin --server http://localhost:8080 health
mizu-admin --server http://localhost:8080 stats
mizu-admin --server http://localhost:8080 blocked-ips
```

## Monitoring Categories

### 1. Queue Metrics
Monitor the async delivery queue health and performance.
- [Queue Metrics Documentation](queue-metrics.md)

### 2. SMTP Metrics
Monitor SMTP connection and message processing.
- Connection counts and durations
- Message acceptance/rejection rates
- SPF/DKIM/DMARC validation results

### 3. HTTP Delivery Metrics
Monitor webhook delivery performance.
- Request success/failure rates
- Response times
- Circuit breaker state

### 4. System Metrics
Monitor system resources and cluster health.
- Connection tracking
- Rate limiting
- Cluster membership

## Alerting

Pre-configured alerting rules for Prometheus AlertManager:
- [Alerting Rules](alerting.md)

## Example Configurations

- [Prometheus Configuration](prometheus.yml)
- [Grafana Dashboard](grafana-dashboard.json)

## Integration Examples

### Prometheus

```yaml
scrape_configs:
  - job_name: 'mizu'
    static_configs:
      - targets: ['localhost:9090']
    basic_auth:
      username: 'admin'
      password: 'secret'
    scrape_interval: 15s
```

### Grafana

Import the [example dashboard](grafana-dashboard.json) or create custom dashboards using the metrics documented in each section.

### Datadog

```yaml
instances:
  - prometheus_url: http://localhost:9090/metrics
    namespace: mizu
    metrics:
      - mizu_*
    auth:
      username: admin
      password: secret
```

## Health Check Integration

### Kubernetes Liveness/Readiness Probes

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```

### Docker Health Check

```dockerfile
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1
```

### Load Balancer Health Check

Configure your load balancer to check `GET /health`:
- **Healthy**: HTTP 200 with `{"status":"healthy"}`
- **Unhealthy**: HTTP 503 with `{"status":"unhealthy"}`

## Monitoring Best Practices

### 1. Set Up Alerts
Configure alerts for critical metrics (see [alerting.md](alerting.md))

### 2. Create Dashboards
Build dashboards for:
- Queue depth and processing rate
- Email delivery success rate
- SMTP connection rate and rejections
- Circuit breaker state

### 3. Log Aggregation
Integrate with log aggregation systems:
- Structured JSON logging
- Log levels: DEBUG, INFO, WARN, ERROR
- Correlation via `trace_id`

### 4. Retention Policy
- **Metrics**: Keep 15-30 days of detailed metrics
- **Health Checks**: Real-time only, no historical storage needed
- **Logs**: Keep 7-30 days depending on compliance requirements

### 5. SLIs and SLOs

**Service Level Indicators (SLIs):**
- Email acceptance rate (% of SMTP connections accepted)
- Delivery success rate (% of emails delivered successfully)
- Delivery latency (p95 time to deliver)
- Queue depth (current jobs in queue)

**Service Level Objectives (SLOs):**
- 99.9% email acceptance rate
- 99.5% delivery success rate within 48 hours
- p95 delivery latency < 5 seconds
- Queue depth < 100 under normal load

## Troubleshooting

### High Queue Depth

1. Check worker count: `mizu_queue_workers`
2. Check delivery duration: `mizu_queue_delivery_duration_seconds`
3. Check circuit breaker state: `mizu_circuit_breaker_state`
4. Check DLQ: `mizu_queue_jobs_dlq`

### High Failure Rate

1. Check HTTP errors: `mizu_http_requests_total{status_code="5xx"}`
2. Check circuit breaker rejects: `mizu_circuit_breaker_rejects_total`
3. Review logs for error patterns
4. Check destination health: `mizu-admin health`

### High Latency

1. Check p95 delivery time: `histogram_quantile(0.95, mizu_queue_delivery_duration_seconds_bucket)`
2. Check destination response time: `mizu_http_request_duration_seconds`
3. Check network connectivity to destination
4. Review circuit breaker state

### Memory/Disk Issues

1. Check queue storage size: `mizu_queue_storage_bytes`
2. Check email files: `mizu_queue_email_files`
3. Run orphan cleanup: `mizu-admin flush-cache`
4. Check BadgerDB garbage collection

## Support

For issues or questions:
- GitHub Issues: https://github.com/migadu/mizu/issues
- Documentation: https://docs.mizu.example.com
