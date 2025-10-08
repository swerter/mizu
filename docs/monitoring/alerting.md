# Alerting Rules

Recommended Prometheus alerting rules for Mizu SMTP relay monitoring.

## Quick Start

Add these rules to your Prometheus `alert.rules.yml`:

```yaml
groups:
  - name: mizu_queue
    interval: 30s
    rules:
      # Import from sections below
```

## Queue Alerts

### Critical Alerts

#### QueueDepthCritical
High queue depth indicates delivery problems or insufficient capacity.

```yaml
- alert: QueueDepthCritical
  expr: mizu_queue_jobs_active > 1000
  for: 5m
  labels:
    severity: critical
    component: queue
  annotations:
    summary: "Queue depth critically high"
    description: "Queue has {{ $value }} jobs pending (threshold: 1000). Check destination health and worker capacity."
    runbook: "https://docs.mizu.example.com/runbooks/queue-depth"
```

**Causes:**
- Destination endpoint is down or slow
- Circuit breaker is open
- Insufficient workers
- High incoming email rate

**Actions:**
1. Check destination health: `mizu-admin health`
2. Check circuit breaker state
3. Review delivery errors in logs
4. Consider increasing worker count
5. Check if destination can handle the load

---

#### QueueDLQHigh
High DLQ count indicates systematic delivery failures.

```yaml
- alert: QueueDLQHigh
  expr: mizu_queue_jobs_dlq > 100
  for: 10m
  labels:
    severity: critical
    component: queue
  annotations:
    summary: "Dead letter queue has {{ $value }} failed jobs"
    description: "More than 100 jobs in DLQ indicates systematic delivery issues."
    runbook: "https://docs.mizu.example.com/runbooks/dlq-high"
```

**Causes:**
- Destination endpoint rejecting emails (4xx errors)
- Authentication failures
- Invalid email format
- Destination configuration issues

**Actions:**
1. Review DLQ entries: Check logs for common error patterns
2. Test destination endpoint manually
3. Verify API keys and authentication
4. Check destination endpoint configuration

---

#### QueueFailureRateHigh
High failure rate indicates serious delivery problems.

```yaml
- alert: QueueFailureRateHigh
  expr: |
    rate(mizu_queue_jobs_failed_total[5m]) /
    rate(mizu_queue_jobs_delivered_total[5m]) * 100 > 10
  for: 5m
  labels:
    severity: critical
    component: queue
  annotations:
    summary: "Queue failure rate is {{ $value | humanizePercentage }}"
    description: "More than 10% of jobs are failing permanently."
    runbook: "https://docs.mizu.example.com/runbooks/failure-rate"
```

**Causes:**
- Destination endpoint issues
- Network connectivity problems
- Authentication failures
- Invalid payload format

---

#### QueueNoWorkers
Workers are not processing jobs despite queue having pending work.

```yaml
- alert: QueueNoWorkers
  expr: |
    mizu_queue_jobs_active > 0 and
    mizu_queue_workers > 0 and
    rate(mizu_queue_jobs_delivered_total[5m]) == 0
  for: 10m
  labels:
    severity: critical
    component: queue
  annotations:
    summary: "Queue has pending jobs but no deliveries"
    description: "{{ $value }} jobs pending but no deliveries in 10 minutes. Workers may be stuck."
    runbook: "https://docs.mizu.example.com/runbooks/stuck-workers"
```

**Causes:**
- Worker goroutines deadlocked
- BadgerDB lock contention
- Destination endpoint hanging (not timing out)
- Bug in delivery logic

**Actions:**
1. Check server health: `mizu-admin health`
2. Review server logs for errors/panics
3. Check for goroutine leaks
4. Restart server if necessary

---

### Warning Alerts

#### QueueDepthWarning
Queue depth is elevated but not yet critical.

```yaml
- alert: QueueDepthWarning
  expr: mizu_queue_jobs_active > 500
  for: 10m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "Queue depth elevated: {{ $value }} jobs"
    description: "Queue has more than 500 jobs for 10+ minutes."
    runbook: "https://docs.mizu.example.com/runbooks/queue-depth"
```

---

#### QueueDLQWarning
DLQ size is growing but not yet critical.

```yaml
- alert: QueueDLQWarning
  expr: mizu_queue_jobs_dlq > 50
  for: 10m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "DLQ has {{ $value }} failed jobs"
    description: "Dead letter queue size is elevated."
```

---

#### QueueSlowDelivery
Delivery latency is high.

```yaml
- alert: QueueSlowDelivery
  expr: |
    histogram_quantile(0.95,
      rate(mizu_queue_delivery_duration_seconds_bucket[5m])
    ) > 10
  for: 5m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "p95 delivery duration is {{ $value }}s"
    description: "Deliveries are taking longer than 10 seconds (p95)."
    runbook: "https://docs.mizu.example.com/runbooks/slow-delivery"
```

---

#### QueueHighJobAge
Jobs are taking too long to deliver.

```yaml
- alert: QueueHighJobAge
  expr: |
    histogram_quantile(0.95,
      rate(mizu_queue_job_age_seconds_bucket[5m])
    ) > 3600
  for: 10m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "p95 job age is {{ $value | humanizeDuration }}"
    description: "Jobs are taking over 1 hour to deliver (p95)."
    runbook: "https://docs.mizu.example.com/runbooks/high-job-age"
```

---

#### QueueHighRetryRate
Excessive retries indicate destination reliability issues.

```yaml
- alert: QueueHighRetryRate
  expr: |
    rate(mizu_queue_jobs_retries_total[5m]) /
    rate(mizu_queue_jobs_delivered_total[5m]) > 2
  for: 10m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "Average {{ $value }} retries per job"
    description: "Jobs require excessive retries to deliver successfully."
    runbook: "https://docs.mizu.example.com/runbooks/high-retry-rate"
```

---

#### QueueStorageHigh
Email storage is growing large.

```yaml
- alert: QueueStorageHigh
  expr: mizu_queue_storage_bytes / 1024 / 1024 / 1024 > 10
  for: 30m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "Queue storage is {{ $value }}GB"
    description: "Email file storage exceeds 10GB."
    runbook: "https://docs.mizu.example.com/runbooks/storage-high"
```

---

#### QueueScheduleInconsistency
Schedule entries don't match job count (indicates a bug).

```yaml
- alert: QueueScheduleInconsistency
  expr: abs(mizu_queue_schedule_entries - mizu_queue_jobs_active) > 10
  for: 15m
  labels:
    severity: warning
    component: queue
  annotations:
    summary: "Schedule/job count mismatch: {{ $value }}"
    description: "Schedule entries ({{ $labels.schedule_entries }}) don't match active jobs ({{ $labels.jobs_active }}). Possible bug."
    runbook: "https://docs.mizu.example.com/runbooks/schedule-inconsistency"
```

---

## SMTP Alerts

### Critical Alerts

#### SMTPHighRejectionRate

```yaml
- alert: SMTPHighRejectionRate
  expr: |
    rate(mizu_smtp_messages_rejected_total[5m]) /
    rate(mizu_smtp_messages_received_total[5m]) * 100 > 50
  for: 5m
  labels:
    severity: critical
    component: smtp
  annotations:
    summary: "{{ $value | humanizePercentage }} of SMTP messages rejected"
    description: "More than 50% of incoming emails are being rejected."
```

---

### Warning Alerts

#### SMTPConnectionLimit

```yaml
- alert: SMTPConnectionLimit
  expr: mizu_smtp_connections_active / mizu_connections_tracker_limit > 0.8
  for: 5m
  labels:
    severity: warning
    component: smtp
  annotations:
    summary: "SMTP connections at {{ $value | humanizePercentage }} of limit"
    description: "Connection limit may be reached soon."
```

---

## Circuit Breaker Alerts

#### CircuitBreakerOpen

```yaml
- alert: CircuitBreakerOpen
  expr: mizu_circuit_breaker_state{state="open"} == 1
  for: 2m
  labels:
    severity: critical
    component: circuit_breaker
  annotations:
    summary: "Circuit breaker is OPEN"
    description: "Destination endpoint is failing, circuit breaker opened to prevent cascading failures."
    runbook: "https://docs.mizu.example.com/runbooks/circuit-breaker-open"
```

**Actions:**
1. Check destination endpoint health
2. Review recent errors
3. Circuit breaker will auto-close after recovery
4. Monitor `mizu_circuit_breaker_failures_total`

---

## System Alerts

#### HighMemoryUsage

```yaml
- alert: HighMemoryUsage
  expr: |
    process_resident_memory_bytes /
    node_memory_MemTotal_bytes * 100 > 80
  for: 5m
  labels:
    severity: warning
    component: system
  annotations:
    summary: "Memory usage at {{ $value | humanizePercentage }}"
    description: "Process memory usage is high."
```

---

#### HighCPUUsage

```yaml
- alert: HighCPUUsage
  expr: rate(process_cpu_seconds_total[5m]) * 100 > 80
  for: 10m
  labels:
    severity: warning
    component: system
  annotations:
    summary: "CPU usage at {{ $value | humanizePercentage }}"
    description: "Process CPU usage is elevated."
```

---

## Health Check Alerts

#### HealthCheckFailed

```yaml
- alert: HealthCheckFailed
  expr: up{job="mizu"} == 0
  for: 2m
  labels:
    severity: critical
    component: system
  annotations:
    summary: "Mizu server is down"
    description: "Health check failed for {{ $labels.instance }}."
```

---

#### ComponentUnhealthy

```yaml
- alert: ComponentUnhealthy
  expr: |
    probe_success{job="mizu_health",
                  endpoint="/health"} == 0
  for: 2m
  labels:
    severity: critical
    component: health
  annotations:
    summary: "Health endpoint reports unhealthy status"
    description: "One or more components are unhealthy."
```

---

## Alert Grouping

Group related alerts to reduce notification spam:

```yaml
# alertmanager.yml
route:
  group_by: ['alertname', 'component', 'severity']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h

  routes:
    # Critical alerts: immediate notification
    - match:
        severity: critical
      receiver: pagerduty
      group_wait: 10s
      repeat_interval: 1h

    # Warning alerts: batched notifications
    - match:
        severity: warning
      receiver: slack
      group_wait: 5m
      repeat_interval: 12h
```

---

## Alert Receivers

### PagerDuty

```yaml
receivers:
  - name: pagerduty
    pagerduty_configs:
      - service_key: '<pagerduty-service-key>'
        description: '{{ .GroupLabels.alertname }}: {{ .Annotations.summary }}'
        details:
          firing: '{{ .Alerts.Firing | len }}'
          description: '{{ .Annotations.description }}'
          runbook: '{{ .Annotations.runbook }}'
```

---

### Slack

```yaml
receivers:
  - name: slack
    slack_configs:
      - api_url: '<slack-webhook-url>'
        channel: '#mizu-alerts'
        title: '{{ .GroupLabels.alertname }}'
        text: |
          {{ range .Alerts }}
          *Alert:* {{ .Labels.alertname }} - {{ .Labels.severity }}
          *Summary:* {{ .Annotations.summary }}
          *Description:* {{ .Annotations.description }}
          {{ if .Annotations.runbook }}*Runbook:* {{ .Annotations.runbook }}{{ end }}
          {{ end }}
```

---

### Email

```yaml
receivers:
  - name: email
    email_configs:
      - to: 'ops@example.com'
        from: 'alertmanager@example.com'
        smarthost: 'smtp.example.com:587'
        auth_username: 'alertmanager@example.com'
        auth_password: '<password>'
        headers:
          Subject: '[{{ .Status | toUpper }}] {{ .GroupLabels.alertname }}'
```

---

## Testing Alerts

### Test Alert Rules

```bash
# Validate alert rules
promtool check rules alert.rules.yml

# Test specific alert
promtool test rules alert.rules.test.yml
```

### Example Test File

```yaml
# alert.rules.test.yml
rule_files:
  - alert.rules.yml

evaluation_interval: 1m

tests:
  - interval: 1m
    input_series:
      - series: 'mizu_queue_jobs_active'
        values: '0 500 1000 1500'

    alert_rule_test:
      - eval_time: 6m
        alertname: QueueDepthCritical
        exp_alerts:
          - exp_labels:
              severity: critical
              component: queue
            exp_annotations:
              summary: "Queue depth critically high"
```

---

## Runbook Templates

Create runbooks for each alert at the URLs specified in annotations.

### Example Runbook Structure

```markdown
# Alert: QueueDepthCritical

## Symptoms
- Queue has >1000 pending jobs
- Emails are delayed
- Health endpoint shows degraded status

## Investigation
1. Check current queue size: `mizu-admin health`
2. Check delivery rate: Prometheus query
3. Check destination health
4. Check circuit breaker state
5. Review error logs

## Resolution Steps
1. Verify destination is reachable
2. Check destination can handle load
3. Increase worker count if needed
4. Investigate and fix destination issues
5. Monitor recovery

## Prevention
- Set up capacity planning alerts
- Load test destination regularly
- Implement backpressure mechanisms
- Scale workers based on load
```

---

## Related Documentation

- [Queue Metrics](queue-metrics.md)
- [Main Monitoring Guide](README.md)
- [Prometheus Configuration](prometheus.yml)
