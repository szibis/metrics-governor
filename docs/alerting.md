# Alerting

Recommended alerting rules for monitoring metrics-governor in production. These rules cover all critical failure modes: backend outages, cardinality explosions, export failures, memory pressure, and configuration drift.

## Prometheus Alerting Rules

Save as `metrics-governor-alerts.rules.yml` and load into Prometheus or VMAlert.

```yaml
groups:
  - name: metrics-governor
    rules:
      # --- Circuit Breaker ---
      - alert: MetricsGovernorCircuitOpen
        expr: metrics_governor_queue_circuit_breaker_state > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Circuit breaker open"
          description: "Queue circuit breaker is {{ if eq $value 1.0 }}OPEN{{ else }}HALF-OPEN{{ end }}. Backend may be unavailable."

      - alert: MetricsGovernorHighRejectionRate
        expr: rate(metrics_governor_queue_circuit_breaker_rejections_total[5m]) > 10
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "High circuit breaker rejection rate"
          description: "{{ $value | humanize }} rejections/sec — data is being lost."

      # --- Cardinality ---
      - alert: MetricsGovernorCardinalitySpike
        expr: sum(metrics_governor_metric_cardinality) > 500000
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High total cardinality"
          description: "Total cardinality {{ $value | humanize }} exceeds threshold. Check for label explosions."

      - alert: MetricsGovernorLimitViolations
        expr: rate(metrics_governor_limit_cardinality_exceeded_total[5m]) > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Sustained limit violations"
          description: "Rule {{ $labels.rule }} has been exceeding cardinality limits for 10+ minutes."

      # --- Drop Rate ---
      - alert: MetricsGovernorHighDropRate
        expr: |
          rate(metrics_governor_limit_datapoints_dropped_total[5m])
          / (rate(metrics_governor_limit_datapoints_dropped_total[5m])
             + rate(metrics_governor_limit_datapoints_passed_total[5m])) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High datapoint drop rate"
          description: "Rule {{ $labels.rule }} is dropping {{ $value | humanizePercentage }} of datapoints."

      # --- Export Errors ---
      - alert: MetricsGovernorExportErrors
        expr: rate(metrics_governor_prw_export_errors_total[5m]) > 0 or rate(metrics_governor_export_errors_total[5m]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Export errors detected"
          description: "Metrics export is failing. Check backend connectivity."

      # --- Backoff ---
      - alert: MetricsGovernorHighBackoff
        expr: metrics_governor_queue_current_backoff_seconds > 120
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High queue backoff delay"
          description: "Current backoff delay is {{ $value | humanize }}s. Backend may be slow to recover."

      # --- Memory ---
      - alert: MetricsGovernorHighMemory
        expr: process_resident_memory_bytes{job="metrics-governor"} / on() metrics_governor_memory_limit_bytes > 0.85
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High memory usage"
          description: "metrics-governor is using {{ $value | humanizePercentage }} of its memory limit."

      # --- Health ---
      - alert: MetricsGovernorUnhealthy
        expr: up{job="metrics-governor"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "metrics-governor is down"
          description: "Instance {{ $labels.instance }} is unreachable."

      # --- Config Reload ---
      - alert: MetricsGovernorStaleConfig
        expr: |
          metrics_governor_config_reload_last_success_timestamp_seconds > 0
          and time() - metrics_governor_config_reload_last_success_timestamp_seconds > 86400
        for: 5m
        labels:
          severity: info
        annotations:
          summary: "Config not reloaded in 24h"
          description: "The config-reload sidecar may not be working."

      # --- Queue ---
      - alert: MetricsGovernorQueueBacklog
        expr: metrics_governor_queue_size > metrics_governor_queue_max_size * 0.8
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Failover queue filling up"
          description: "Queue is at {{ $value | humanize }} entries (80%+ capacity). Entries may be evicted."
```

## VMAlert Integration

For VictoriaMetrics users, load the same rules file via VMAlert:

```yaml
# vmalert config
rule:
  - /etc/vmalert/rules/metrics-governor-alerts.rules.yml
datasource:
  url: http://victoriametrics:8428
notifier:
  url: http://alertmanager:9093
remoteWrite:
  url: http://victoriametrics:8428
```

```bash
vmalert \
  -rule=/etc/vmalert/rules/metrics-governor-alerts.rules.yml \
  -datasource.url=http://victoriametrics:8428 \
  -notifier.url=http://alertmanager:9093
```

## Grafana Alert Integration

The operations dashboard panels can be converted to Grafana alerts:

1. Open a panel (e.g., "Circuit Breaker State") → Edit → Alert tab
2. Set condition: `WHEN last() OF query IS ABOVE 0`
3. Configure notification channel
4. Set evaluation interval and pending period

Key panels to alert on:
- **Circuit Breaker State** — alert when open
- **Drop Rate** — alert when > 10%
- **Export Errors** — alert when rate > 0
- **Queue Size** — alert when > 80% capacity

## Notification Channels

### Alertmanager — Slack

```yaml
# alertmanager.yml
route:
  group_by: ['alertname', 'severity']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  receiver: 'slack-notifications'
  routes:
    - match:
        severity: critical
      receiver: 'slack-critical'

receivers:
  - name: 'slack-notifications'
    slack_configs:
      - api_url: 'https://hooks.slack.com/services/YOUR/WEBHOOK/URL'
        channel: '#observability-alerts'
        title: '{{ .CommonAnnotations.summary }}'
        text: '{{ .CommonAnnotations.description }}'

  - name: 'slack-critical'
    slack_configs:
      - api_url: 'https://hooks.slack.com/services/YOUR/WEBHOOK/URL'
        channel: '#on-call'
        title: 'CRITICAL: {{ .CommonAnnotations.summary }}'
        text: '{{ .CommonAnnotations.description }}'
```

### Alertmanager — PagerDuty

```yaml
receivers:
  - name: 'pagerduty-critical'
    pagerduty_configs:
      - service_key: 'YOUR-PAGERDUTY-SERVICE-KEY'
        severity: '{{ .CommonLabels.severity }}'
        description: '{{ .CommonAnnotations.summary }}'
        details:
          description: '{{ .CommonAnnotations.description }}'
```

### Alertmanager — Email

```yaml
receivers:
  - name: 'email-team'
    email_configs:
      - to: 'observability-team@example.com'
        from: 'alertmanager@example.com'
        smarthost: 'smtp.example.com:587'
        auth_username: 'alertmanager@example.com'
        auth_password: 'password'
```

## Runbook References

| Alert | Runbook Section |
|-------|-----------------|
| `MetricsGovernorCircuitOpen` | [Resilience — Circuit Breaker Keeps Opening](resilience.md#circuit-breaker-keeps-opening) |
| `MetricsGovernorHighBackoff` | [Resilience — Backoff Delay Too Long](resilience.md#backoff-delay-too-long) |
| `MetricsGovernorExportErrors` | [Resilience — Backend Rejecting Batches](resilience.md#backend-rejecting-batches-as-too-large) |
| `MetricsGovernorHighMemory` | [Resilience — OOM Kills](resilience.md#oom-kills-despite-memory-limits) |
| `MetricsGovernorCardinalitySpike` | [Limits — Adaptive Limiting](limits.md#adaptive-limiting-recommended) |
| `MetricsGovernorStaleConfig` | [Reload — Monitoring Reloads](reload.md#monitoring-reloads) |

## See Also

- [Resilience](resilience.md) — Circuit breaker, backoff, troubleshooting
- [Statistics](statistics.md) — All available Prometheus metrics
- [Dashboards](dashboards.md) — Grafana dashboard documentation
- [Limits](limits.md) — Limits enforcement configuration
