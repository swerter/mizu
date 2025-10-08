# Queue Metrics

The persistent queue exports comprehensive metrics for monitoring email delivery performance and health.

## Metrics Overview

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_queue_jobs_total` | Counter | Total number of jobs enqueued |
| `mizu_queue_jobs_active` | Gauge | Current number of jobs in queue |
| `mizu_queue_jobs_delivered_total` | Counter | Total jobs successfully delivered |
| `mizu_queue_jobs_failed_total` | Counter | Total jobs that failed permanently |
| `mizu_queue_jobs_retries_total` | Counter | Total number of retry attempts |
| `mizu_queue_jobs_dlq` | Gauge | Current jobs in dead letter queue |
| `mizu_queue_workers` | Gauge | Number of active worker goroutines |
| `mizu_queue_delivery_duration_seconds` | Histogram | Time to deliver a job (HTTP call duration) |
| `mizu_queue_job_age_seconds` | Histogram | Age of jobs when delivered (enqueue → delivery) |
| `mizu_queue_storage_bytes` | Gauge | Total storage used by queue (email files) |
| `mizu_queue_email_files` | Gauge | Number of email files stored on filesystem |
| `mizu_queue_schedule_entries` | Gauge | Number of schedule entries for retry timing |

## Counter Metrics

### `mizu_queue_jobs_total`
Total jobs enqueued since server start.

**Use Cases:**
- Calculate enqueue rate: `rate(mizu_queue_jobs_total[5m])`
- Track total throughput over time

**Example Query:**
```promql
# Jobs enqueued per second
rate(mizu_queue_jobs_total[5m])
```

### `mizu_queue_jobs_delivered_total`
Total jobs successfully delivered to the destination endpoint.

**Use Cases:**
- Calculate delivery rate: `rate(mizu_queue_jobs_delivered_total[5m])`
- Calculate delivery success rate: `delivered / enqueued * 100`

**Example Queries:**
```promql
# Deliveries per second
rate(mizu_queue_jobs_delivered_total[5m])

# Delivery success rate (percentage)
rate(mizu_queue_jobs_delivered_total[5m]) / rate(mizu_queue_jobs_total[5m]) * 100
```

### `mizu_queue_jobs_failed_total`
Total jobs that failed permanently and were moved to the dead letter queue (DLQ).

**Causes of Permanent Failure:**
- Job exceeded max retry hours (default: 48 hours)
- Non-retryable HTTP errors (4xx except 429)

**Example Queries:**
```promql
# Failure rate
rate(mizu_queue_jobs_failed_total[5m]) / rate(mizu_queue_jobs_delivered_total[5m]) * 100

# Failures in last hour
increase(mizu_queue_jobs_failed_total[1h])
```

### `mizu_queue_jobs_retries_total`
Total number of retry attempts across all jobs.

**Use Cases:**
- Monitor retry behavior
- Detect destination reliability issues
- Calculate average retries per job

**Example Queries:**
```promql
# Retries per second
rate(mizu_queue_jobs_retries_total[5m])

# Average retries per delivered job
rate(mizu_queue_jobs_retries_total[1h]) / rate(mizu_queue_jobs_delivered_total[1h])
```

## Gauge Metrics

### `mizu_queue_jobs_active`
Current number of jobs in the queue (pending delivery).

**Normal Values:**
- **Healthy**: 0-100 jobs
- **Warning**: 100-500 jobs (destination may be slow)
- **Critical**: >500 jobs (delivery issues or high load)

**Example Queries:**
```promql
# Current queue depth
mizu_queue_jobs_active

# Queue depth over last hour (max)
max_over_time(mizu_queue_jobs_active[1h])

# Alert if queue depth > 500 for 5 minutes
mizu_queue_jobs_active > 500
```

### `mizu_queue_jobs_dlq`
Current number of jobs in the dead letter queue (permanently failed).

**Normal Values:**
- **Healthy**: 0-10 jobs
- **Warning**: 10-100 jobs
- **Critical**: >100 jobs (systematic delivery issues)

**DLQ Retention:** 7 days (jobs auto-expire after 7 days)

**Example Queries:**
```promql
# Current DLQ size
mizu_queue_jobs_dlq

# DLQ growth rate
rate(mizu_queue_jobs_dlq[1h])

# Alert if DLQ > 100
mizu_queue_jobs_dlq > 100
```

### `mizu_queue_workers`
Number of active worker goroutines processing jobs.

**Configuration:**
```toml
[queue]
workers = 10  # Number of concurrent workers
```

**Use Cases:**
- Verify worker configuration
- Monitor worker utilization

**Example Queries:**
```promql
# Current workers
mizu_queue_workers

# Worker utilization (jobs per worker)
mizu_queue_jobs_active / mizu_queue_workers
```

### `mizu_queue_storage_bytes`
Total size in bytes of email files stored on filesystem.

**Storage Usage:**
- Emails > 1MB are stored on filesystem
- Emails < 1MB are stored inline in BadgerDB
- Files use content-addressable storage (SHA256 hash)

**Example Queries:**
```promql
# Storage in MB
mizu_queue_storage_bytes / 1024 / 1024

# Storage growth rate (MB per hour)
rate(mizu_queue_storage_bytes[1h]) / 1024 / 1024 * 3600
```

### `mizu_queue_email_files`
Number of email files stored on filesystem (large emails >1MB).

**Example Queries:**
```promql
# Current email files
mizu_queue_email_files

# Average file size
mizu_queue_storage_bytes / mizu_queue_email_files
```

### `mizu_queue_schedule_entries`
Number of schedule entries in BadgerDB (one per queued job).

**Expected Value:** Should equal `mizu_queue_jobs_active`

**Use Case:** Detect orphaned schedule entries (indicates a bug)

**Example Queries:**
```promql
# Check for consistency
mizu_queue_schedule_entries - mizu_queue_jobs_active

# Alert if mismatch
abs(mizu_queue_schedule_entries - mizu_queue_jobs_active) > 10
```

## Histogram Metrics

### `mizu_queue_delivery_duration_seconds`
Time taken to deliver a job to the destination endpoint (HTTP request duration).

**Buckets:** Default Prometheus buckets (0.005s to 10s)

**Example Queries:**
```promql
# p50 delivery duration
histogram_quantile(0.50, rate(mizu_queue_delivery_duration_seconds_bucket[5m]))

# p95 delivery duration
histogram_quantile(0.95, rate(mizu_queue_delivery_duration_seconds_bucket[5m]))

# p99 delivery duration
histogram_quantile(0.99, rate(mizu_queue_delivery_duration_seconds_bucket[5m]))

# Average delivery duration
rate(mizu_queue_delivery_duration_seconds_sum[5m]) / rate(mizu_queue_delivery_duration_seconds_count[5m])

# Alert if p95 > 10 seconds
histogram_quantile(0.95, rate(mizu_queue_delivery_duration_seconds_bucket[5m])) > 10
```

### `mizu_queue_job_age_seconds`
Age of jobs when delivered (time from enqueue to successful delivery).

**Buckets:** 1s, 5s, 10s, 30s, 1m, 5m, 10m, 30m, 1h, 2h, 4h, 8h, 24h, 48h

**Example Queries:**
```promql
# p95 job age
histogram_quantile(0.95, rate(mizu_queue_job_age_seconds_bucket[5m]))

# Jobs delivered within 1 minute
rate(mizu_queue_job_age_seconds_bucket{le="60"}[5m])

# Jobs older than 1 hour when delivered
sum(rate(mizu_queue_job_age_seconds_bucket{le="+Inf"}[5m])) - sum(rate(mizu_queue_job_age_seconds_bucket{le="3600"}[5m]))

# Alert if p95 age > 1 hour
histogram_quantile(0.95, rate(mizu_queue_job_age_seconds_bucket[5m])) > 3600
```

## Health Endpoint

The `/health` endpoint provides queue health status:

```bash
curl http://localhost:8080/health
```

**Response:**
```json
{
  "status": "healthy",
  "components": {
    "queue": {
      "status": "healthy",
      "details": {
        "queue_size": 42,
        "workers_active": 10,
        "workers_running": 3,
        "total_enqueued": 15234,
        "total_delivered": 15180,
        "total_failed": 12,
        "total_retries": 145,
        "dlq_entries": 12,
        "storage_jobs": 42,
        "storage_schedules": 42,
        "email_files": 3,
        "email_storage_mb": 12.5,
        "delivery_rate_percent": "99.64",
        "failure_rate_percent": "0.08"
      }
    }
  }
}
```

**Health Status Levels:**
- `healthy` - All metrics normal
- `degraded` - Warnings (high queue size, high DLQ, high failure rate)
- `unhealthy` - Critical issues (storage errors)

**Degraded Conditions:**
- Queue size > 1000
- DLQ entries > 100
- Failure rate > 10%
- No workers running while jobs pending

## Admin CLI

View queue statistics via admin CLI:

```bash
# Overall health
mizu-admin health

# Statistics
mizu-admin stats
```

## Retry Schedule

Jobs are retried using a time-based progressive backoff schedule over 48 hours:

| Age Range | Retry Interval | Attempts |
|-----------|---------------|----------|
| 0-1 min | immediate | 1 |
| 1-5 min | 1 min | 4 |
| 5-30 min | 5 min | 5 |
| 30 min-2h | 15 min | 6 |
| 2-6h | 30 min | 8 |
| 6-24h | 2 hours | 9 |
| 24-48h | 4 hours | 6 |

**Total:** ~39 attempts over 48 hours

**Configuration:**
```toml
[queue]
max_retry_hours = 48  # Maximum hours before moving to DLQ
```

## Monitoring Scenarios

### Scenario 1: High Queue Depth

**Symptoms:**
- `mizu_queue_jobs_active` > 500
- Increasing over time

**Investigation:**
```promql
# Current queue depth
mizu_queue_jobs_active

# Enqueue vs delivery rate
rate(mizu_queue_jobs_total[5m]) - rate(mizu_queue_jobs_delivered_total[5m])

# Delivery duration (is destination slow?)
histogram_quantile(0.95, rate(mizu_queue_delivery_duration_seconds_bucket[5m]))

# Circuit breaker state (is it open?)
mizu_circuit_breaker_state{state="open"}
```

**Actions:**
1. Check destination health
2. Increase worker count if CPU allows
3. Check circuit breaker state
4. Review destination logs

### Scenario 2: High Failure Rate

**Symptoms:**
- `mizu_queue_jobs_failed_total` increasing
- `mizu_queue_jobs_dlq` growing

**Investigation:**
```promql
# Failure rate
rate(mizu_queue_jobs_failed_total[5m]) / rate(mizu_queue_jobs_delivered_total[5m]) * 100

# HTTP errors
mizu_http_requests_total{status_code=~"5.."}

# Recent DLQ additions
increase(mizu_queue_jobs_dlq[1h])
```

**Actions:**
1. Check server logs for error patterns
2. Test destination endpoint manually
3. Review DLQ entries for common failures
4. Check destination authentication/configuration

### Scenario 3: Slow Deliveries

**Symptoms:**
- `mizu_queue_job_age_seconds` p95 > 1 hour
- Jobs taking long time to deliver

**Investigation:**
```promql
# p95 job age
histogram_quantile(0.95, rate(mizu_queue_job_age_seconds_bucket[5m]))

# p95 delivery duration
histogram_quantile(0.95, rate(mizu_queue_delivery_duration_seconds_bucket[5m]))

# Retry rate
rate(mizu_queue_jobs_retries_total[5m])
```

**Actions:**
1. Check if destination is slow (high delivery duration)
2. Check if retries are frequent (destination reliability issues)
3. Consider increasing worker count
4. Optimize destination endpoint performance

## Dashboard Recommendations

### Queue Overview Dashboard

**Row 1: Throughput**
- Enqueue rate (jobs/sec)
- Delivery rate (jobs/sec)
- Success rate (%)
- Failure rate (%)

**Row 2: Queue Depth**
- Current queue size (gauge)
- Queue size over time (graph)
- DLQ size (gauge)
- DLQ growth (graph)

**Row 3: Latency**
- p50/p95/p99 delivery duration
- p50/p95/p99 job age
- Jobs by age bucket (stacked graph)

**Row 4: Resources**
- Worker count
- Worker utilization
- Storage size (MB)
- Email files count

**Row 5: Retries**
- Retry rate (retries/sec)
- Average retries per job
- Jobs by attempt count

## Related Documentation

- [Main Monitoring Guide](README.md)
- [Alerting Rules](alerting.md)
- [Prometheus Configuration](prometheus.yml)
