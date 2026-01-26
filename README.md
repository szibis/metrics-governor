# metrics-governor

OTLP metrics proxy with buffering and statistics. Receives metrics via gRPC and HTTP, buffers them, tracks cardinality and datapoints, and forwards to a configurable OTLP endpoint with batching support.

## Features

- OTLP gRPC receiver (default: `:4317`)
- OTLP HTTP receiver (default: `:4318`)
- Configurable metrics buffering
- Batch export with configurable size
- Graceful shutdown with final flush
- JSON structured logging
- **Limits enforcement:**
  - Configurable datapoints rate and cardinality limits
  - Per-metric and per-label-combination rules
  - Regex and wildcard matching
  - Actions: log (dry-run), sample, drop
  - Prometheus metrics for limit violations
- **Metrics statistics tracking:**
  - Per-metric datapoints and cardinality
  - Per-label-combination stats (configurable labels)
  - Prometheus `/metrics` endpoint for scraping
  - Periodic global stats logging (every 30s)

## Installation

### From source

```bash
go install github.com/slawomirskowron/metrics-governor/cmd/metrics-governor@latest
```

### Build from source

```bash
git clone https://github.com/slawomirskowron/metrics-governor.git
cd metrics-governor
make build
```

### Multi-platform builds

```bash
make all  # Builds darwin-arm64, linux-arm64, linux-amd64
```

Binaries are output to `bin/` directory.

### Docker

```bash
make docker
# or
docker build -t metrics-governor .
```

## Usage

```bash
metrics-governor [OPTIONS]
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-grpc-listen` | `:4317` | gRPC receiver listen address |
| `-http-listen` | `:4318` | HTTP receiver listen address |
| `-exporter-endpoint` | `localhost:4317` | OTLP exporter endpoint |
| `-exporter-insecure` | `true` | Use insecure connection for exporter |
| `-exporter-timeout` | `30s` | Exporter request timeout |
| `-buffer-size` | `10000` | Maximum number of metrics to buffer |
| `-flush-interval` | `5s` | Buffer flush interval |
| `-batch-size` | `1000` | Maximum batch size for export |
| `-stats-addr` | `:9090` | Stats/metrics HTTP endpoint address |
| `-stats-labels` | | Comma-separated labels to track (e.g., `service,env,cluster`) |
| `-limits-config` | | Path to limits configuration YAML file |
| `-limits-dry-run` | `true` | Dry run mode: log violations but don't drop/sample |
| `-h`, `-help` | | Show help message |
| `-v`, `-version` | | Show version |

### Examples

```bash
# Start with default settings
metrics-governor

# Custom receiver ports
metrics-governor -grpc-listen :5317 -http-listen :5318

# Forward to remote endpoint
metrics-governor -exporter-endpoint otel-collector:4317

# Adjust buffering
metrics-governor -buffer-size 50000 -flush-interval 10s -batch-size 2000

# Enable stats tracking by service, environment and cluster
metrics-governor -stats-labels service,env,cluster

# Enable limits enforcement (dry-run by default)
metrics-governor -limits-config /etc/metrics-governor/limits.yaml

# Enable limits enforcement with actual drop/sample
metrics-governor -limits-config /etc/metrics-governor/limits.yaml -limits-dry-run=false
```

### Docker

```bash
docker run -p 4317:4317 -p 4318:4318 -p 9090:9090 metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -stats-labels service,env,cluster
```

## Statistics

### Prometheus Metrics Endpoint

Stats are exposed on `:9090/metrics` (configurable via `-stats-addr`):

```bash
curl localhost:9090/metrics
```

**Exposed metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `metrics_governor_datapoints_total` | counter | Total datapoints processed |
| `metrics_governor_metrics_total` | gauge | Total unique metric names |
| `metrics_governor_metric_datapoints_total{metric_name="..."}` | counter | Datapoints per metric name |
| `metrics_governor_metric_cardinality{metric_name="..."}` | gauge | Cardinality (unique series) per metric name |
| `metrics_governor_label_datapoints_total{service="...",env="..."}` | counter | Datapoints per label combination |
| `metrics_governor_label_cardinality{service="...",env="..."}` | gauge | Cardinality per label combination |
| `metrics_governor_limit_datapoints_exceeded_total{rule="..."}` | counter | Times datapoints rate limit was exceeded |
| `metrics_governor_limit_cardinality_exceeded_total{rule="..."}` | counter | Times cardinality limit was exceeded |
| `metrics_governor_limit_datapoints_dropped_total{rule="..."}` | counter | Datapoints dropped due to limits |
| `metrics_governor_limit_datapoints_sampled_total{rule="..."}` | counter | Datapoints affected by sampling |

### Periodic Logging

Global stats are logged every 30 seconds in JSON format:

```json
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"stats","fields":{"datapoints_total":1234567,"unique_metrics":42,"total_cardinality":8901}}
```

This includes:
- **datapoints_total**: Cumulative count of all datapoints processed
- **unique_metrics**: Number of unique metric names seen
- **total_cardinality**: Sum of unique series across all metrics

## Logging

All logs are output in JSON format for easy parsing and integration with log aggregation systems:

```json
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"metrics-governor started","fields":{"grpc_addr":":4317","http_addr":":4318","exporter_endpoint":"localhost:4317","stats_addr":":9090"}}
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"gRPC receiver started","fields":{"addr":":4317"}}
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"HTTP receiver started","fields":{"addr":":4318"}}
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"stats endpoint started","fields":{"addr":":9090","path":"/metrics"}}
{"timestamp":"2024-01-26T12:00:30Z","level":"info","message":"stats","fields":{"datapoints_total":1000,"unique_metrics":10,"total_cardinality":150}}
```

**Log levels:**
- `info` - Normal operational messages
- `warn` - Warning conditions
- `error` - Error conditions
- `fatal` - Fatal errors (application exits)

## Limits Configuration

Limits are configured via a YAML file specified with `-limits-config`. See [examples/limits.yaml](examples/limits.yaml) for a complete example.

### Configuration Structure

```yaml
defaults:
  max_datapoints_rate: 1000000  # per minute
  max_cardinality: 100000
  action: log                   # log, sample, or drop

rules:
  - name: "rule-name"
    match:
      metric_name: "http_request_.*"  # regex pattern (optional)
      labels:                          # label matching (optional)
        service: "payment-api"         # exact match
        env: "*"                       # wildcard - any value
    max_datapoints_rate: 50000        # per minute, 0 = no limit
    max_cardinality: 2000             # 0 = no limit
    action: drop                      # log, sample, or drop
    sample_rate: 0.5                  # required when action=sample
```

### Actions

| Action | Description |
|--------|-------------|
| `log` | Log violation but pass through data (default, safe for dry-run) |
| `sample` | Sample data at `sample_rate` when limit exceeded |
| `drop` | Drop data entirely when limit exceeded |

### Matching Rules

- **metric_name**: Exact match or regex pattern (e.g., `http_request_.*`)
- **labels**: Key-value pairs where `*` matches any value
- Rules are evaluated in order; first match wins

### Example: Limit Violation Log

When a limit is exceeded, a warning is logged:

```json
{"timestamp":"2024-01-26T12:00:00Z","level":"warn","message":"limit exceeded","fields":{"rule":"payment-service-prod-limits","metric":"http_requests_total","reason":"cardinality","action":"drop","dry_run":false,"datapoints":10}}
```

### Dry Run Mode

By default, limits run in dry-run mode (`-limits-dry-run=true`). This logs all violations but doesn't actually drop or sample data. Use this to:

1. Understand your metrics cardinality before enforcing limits
2. Tune limit thresholds based on actual traffic
3. Safely test limit configurations in production

To enable actual enforcement:

```bash
metrics-governor -limits-config limits.yaml -limits-dry-run=false
```

### Action Examples

#### Log Action (Monitoring Only)

Use `action: log` to monitor limit violations without affecting data flow. Ideal for understanding traffic patterns before enforcing limits.

```yaml
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log  # Global default: log only

rules:
  - name: "monitor-high-cardinality"
    match:
      metric_name: "http_request_duration_.*"
    max_cardinality: 5000
    action: log
```

**Log output when limit is exceeded:**
```json
{"timestamp":"2024-01-26T12:00:00Z","level":"warn","message":"limit exceeded","fields":{"rule":"monitor-high-cardinality","metric":"http_request_duration_seconds","reason":"cardinality","action":"log","dry_run":true,"datapoints":15}}
```

**Prometheus metrics exposed:**
```
metrics_governor_limit_cardinality_exceeded_total{rule="monitor-high-cardinality"} 42
```

#### Sample Action (Rate Limiting)

Use `action: sample` to reduce data volume by randomly sampling datapoints when limits are exceeded. Useful for high-volume metrics where some data loss is acceptable.

```yaml
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log

rules:
  - name: "sample-payment-service"
    match:
      labels:
        service: "payment-api"
        env: "prod"
    max_datapoints_rate: 50000
    max_cardinality: 2000
    action: sample
    sample_rate: 0.5  # Keep 50% of datapoints when limit exceeded
```

**Log output when sampling is applied:**
```json
{"timestamp":"2024-01-26T12:00:00Z","level":"warn","message":"limit exceeded","fields":{"rule":"sample-payment-service","metric":"payment_transactions_total","reason":"datapoints_rate","action":"sample","dry_run":false,"datapoints":100,"sample_rate":0.5}}
```

**Prometheus metrics exposed:**
```
metrics_governor_limit_datapoints_exceeded_total{rule="sample-payment-service"} 156
metrics_governor_limit_datapoints_sampled_total{rule="sample-payment-service"} 7800
```

#### Drop Action (Hard Limit)

Use `action: drop` to completely discard datapoints when limits are exceeded. Use for strict cardinality control or protecting against runaway metrics.

```yaml
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log

rules:
  - name: "drop-runaway-metrics"
    match:
      metric_name: "legacy_app_.*"
    max_cardinality: 100
    action: drop

  - name: "protect-high-cardinality-http"
    match:
      metric_name: "http_request_duration_.*"
    max_cardinality: 1000
    action: drop
```

**Log output when datapoints are dropped:**
```json
{"timestamp":"2024-01-26T12:00:00Z","level":"warn","message":"limit exceeded","fields":{"rule":"drop-runaway-metrics","metric":"legacy_app_request_count","reason":"cardinality","action":"drop","dry_run":false,"datapoints":25}}
```

**Prometheus metrics exposed:**
```
metrics_governor_limit_cardinality_exceeded_total{rule="drop-runaway-metrics"} 89
metrics_governor_limit_datapoints_dropped_total{rule="drop-runaway-metrics"} 2225
```

#### Combined Example

A complete configuration using all action types:

```yaml
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log  # Safe default

rules:
  # Critical: Drop known problematic metrics
  - name: "drop-legacy-metrics"
    match:
      metric_name: "legacy_.*"
    max_cardinality: 100
    action: drop

  # Important: Sample high-volume production services
  - name: "sample-prod-services"
    match:
      labels:
        env: "prod"
        service: "*"
    max_datapoints_rate: 100000
    max_cardinality: 5000
    action: sample
    sample_rate: 0.25

  # Monitor: Log violations for dev environment
  - name: "monitor-dev"
    match:
      labels:
        env: "dev"
    max_datapoints_rate: 500000
    max_cardinality: 50000
    action: log
```

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  OTLP Clients   │────▶│ metrics-governor│────▶│  OTLP Backend   │
│  (gRPC/HTTP)    │     │    (buffer)     │     │  (collector)    │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        :4317/:4318            │                      :4317
                               │
                               ▼
                        ┌─────────────┐
                        │ Prometheus  │
                        │  (scrape)   │
                        └─────────────┘
                             :9090
```

## Testing

A complete test environment is provided using Docker Compose. It includes:

- **metrics-governor**: The main proxy with limits configuration
- **otel-collector**: OpenTelemetry Collector as the backend
- **prometheus**: For scraping and visualizing metrics
- **metrics-generator**: A Go application that generates test metrics

### Quick Start

```bash
# Run the test environment
./test/run-test.sh

# Or manually with docker compose
docker compose up --build
```

### Available Endpoints

| Service | Endpoint | Description |
|---------|----------|-------------|
| metrics-governor gRPC | `localhost:4317` | OTLP gRPC receiver |
| metrics-governor HTTP | `localhost:4318` | OTLP HTTP receiver |
| metrics-governor stats | `http://localhost:9090/metrics` | Prometheus metrics |
| OTel Collector | `localhost:14317` | Backend gRPC endpoint |
| Prometheus UI | `http://localhost:9091` | Prometheus web interface |

### Useful Commands

```bash
# View metrics-governor stats
curl -s localhost:9090/metrics | grep metrics_governor

# Check limit violations
curl -s localhost:9090/metrics | grep limit

# View logs
docker compose logs -f metrics-governor
docker compose logs -f metrics-generator

# Stop all services
docker compose down
```

### Test Scenarios

The metrics generator creates various test scenarios:

1. **Normal traffic**: HTTP request metrics for multiple services and environments
2. **High cardinality**: Legacy app metrics with unique request IDs (triggers limits)
3. **Multiple services**: payment-api, order-api, inventory-api, legacy-app
4. **Multiple environments**: prod, staging, dev

Watch the metrics-governor logs to see limit violations being triggered:

```bash
docker compose logs -f metrics-governor | grep "limit exceeded"
```

## License

See [LICENSE](LICENSE) file.
