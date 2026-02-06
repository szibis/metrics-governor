# Grafana Dashboards

Pre-built Grafana dashboards for monitoring metrics-governor.

## Available Dashboards

### 1. Operations Dashboard (`operations.json`)

Production-focused dashboard for monitoring metrics-governor in real deployments.

**Sections:**
- **Overview** - Pass rate, datapoints/sec, unique metrics, cardinality, queue size, export errors
- **OTLP Throughput** - OTLP datapoints rate (received/sent/dropped), batches & exports
- **PRW Throughput** - Prometheus Remote Write datapoints/timeseries rate, batches & exports
- **Limits Enforcement** - Violations by rule, dropped vs passed, cardinality tracking, adaptive groups
- **Cardinality Analysis** - Top 10 metrics by cardinality, top 10 by datapoints rate
- **Queue & Persistence** - Queue size, max size, push/pop/retry operations
- **Sharding** - Active endpoints, datapoints by endpoint, DNS & rehash events
- **Runtime** - Memory usage (heap/stack), goroutines, GC rate
- **Config Reload & Health** - Uptime, config reload count/rate, last reload time, process start time

**Use for:**
- Day-to-day monitoring
- Troubleshooting issues
- Capacity planning
- Limits tuning

### 2. Development Dashboard (`development.json`)

Development and testing dashboard showing the full data pipeline: generator → governor → verifier.

**Sections:**
- **Test Pipeline Overview** - At-a-glance status: generator rate, governor throughput, export errors, verifier pass rate
- **Generator** - Throughput, batch latency, totals, errors, spike/mistake scenario status
- **Governor Pipeline** - Datapoints rate (received/sent/dropped), batches, buffer size, bytes throughput
- **Config & Health** - Uptime, config reloads, last reload time, goroutines, memory, GC
- **Limits Enforcement** - Dropped vs passed, limit violations, per-rule datapoints and cardinality
- **Verifier** - Pass rate, ingestion rate, VictoriaMetrics stats, check totals, end-to-end comparison

**Use for:**
- Local development with docker-compose
- Debugging data flow issues
- Verifying limits enforcement behavior
- Monitoring test pipeline health

## Installation

### Import via Grafana UI

1. Open Grafana → Dashboards → Import
2. Upload JSON file or paste contents
3. Select your Prometheus datasource
4. Click Import

### Provisioning (Kubernetes/Docker)

```yaml
# grafana-provisioning/dashboards/dashboards.yaml
apiVersion: 1
providers:
  - name: 'metrics-governor'
    orgId: 1
    folder: 'Metrics Governor'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    options:
      path: /var/lib/grafana/dashboards/metrics-governor
```

Copy dashboard JSON files to the provisioning path.

### Docker Compose Example

```yaml
grafana:
  image: grafana/grafana:latest
  volumes:
    - ./dashboards:/var/lib/grafana/dashboards/metrics-governor
    - ./provisioning:/etc/grafana/provisioning
```

## Requirements

- Grafana 9.x or later
- Prometheus datasource configured
- metrics-governor exposing metrics on `/metrics` endpoint

## Variables

All dashboards use a `datasource` template variable to select the Prometheus datasource.

## Customization

Dashboards are editable. Common customizations:

- Adjust time range defaults
- Add alerts on key metrics
- Filter by specific rules or endpoints
- Add company-specific thresholds
