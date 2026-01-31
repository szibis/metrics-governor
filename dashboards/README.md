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

**Use for:**
- Day-to-day monitoring
- Troubleshooting issues
- Capacity planning
- Limits tuning

### 2. E2E Testing Dashboard (`e2e-testing.json`)

Full testing dashboard including metrics generator and verifier panels.

**Additional Sections:**
- **Generator** - Metrics/datapoints per second, batch latency, burst events
- **Verifier** - Verification pass rate, ingestion rate, check status

**Use for:**
- E2E testing with docker-compose
- Development and debugging
- CI/CD validation

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

Both dashboards use a `datasource` template variable to select the Prometheus datasource.

## Customization

Dashboards are editable. Common customizations:

- Adjust time range defaults
- Add alerts on key metrics
- Filter by specific rules or endpoints
- Add company-specific thresholds
