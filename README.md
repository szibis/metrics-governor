# metrics-governor

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()

**Test Coverage**

| Package | Coverage |
|---------|----------|
| ![buffer](https://img.shields.io/badge/buffer-100%25-brightgreen) | internal/buffer |
| ![config](https://img.shields.io/badge/config-100%25-brightgreen) | internal/config |
| ![exporter](https://img.shields.io/badge/exporter-100%25-brightgreen) | internal/exporter |
| ![stats](https://img.shields.io/badge/stats-93%25-green) | internal/stats |
| ![receiver](https://img.shields.io/badge/receiver-88%25-green) | internal/receiver |
| ![logging](https://img.shields.io/badge/logging-84%25-green) | internal/logging |
| ![limits](https://img.shields.io/badge/limits-83%25-green) | internal/limits |
| ![Total](https://img.shields.io/badge/Total-80.6%25-green) | **All packages** |

---

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
  - Actions: log (dry-run), adaptive, drop
  - Prometheus metrics for limit violations
- **Metrics statistics tracking:**
  - Per-metric datapoints and cardinality
  - Per-label-combination stats (configurable labels)
  - Prometheus `/metrics` endpoint for scraping
  - Periodic global stats logging (every 30s)

## Installation

### From source

```bash
go install ./cmd/metrics-governor
```

### Build from source

```bash
git clone <repository-url>
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
| `metrics_governor_limit_datapoints_passed_total{rule="..."}` | counter | Datapoints passed through (within limits) |

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
  action: log                   # log, adaptive, or drop

rules:
  - name: "rule-name"
    match:
      metric_name: "http_request_.*"  # regex pattern (optional)
      labels:                          # label matching (optional)
        service: "payment-api"         # exact match
        env: "*"                       # wildcard - any value
    max_datapoints_rate: 50000        # per minute, 0 = no limit
    max_cardinality: 2000             # 0 = no limit
    action: adaptive                  # log, adaptive, or drop
    group_by: ["service", "env"]      # labels for adaptive grouping
```

### Actions

| Action | Description |
|--------|-------------|
| `log` | Log violation but pass through all data (default, safe for dry-run) |
| `adaptive` | **Intelligent limiting**: Track per-group stats, drop only top offenders to stay within limits |
| `drop` | Drop all data when limit exceeded (nuclear option) |

### Adaptive Limiting (Recommended)

The `adaptive` action is the key feature of metrics-governor. Instead of dropping all metrics or randomly sampling, it:

1. **Tracks statistics per group** defined by `group_by` labels
2. **Identifies top offenders** when limits are exceeded
3. **Drops only the worst groups** to bring totals within limits
4. **Preserves smaller contributors** that are within reasonable bounds

This ensures you get maximum data delivery while staying within your cardinality and datapoints budgets.

#### How Adaptive Works

```
Example: max_cardinality: 1000, group_by: ["service"]

Current state:
  - service=api-a:     400 series  (40%)
  - service=api-b:     300 series  (30%)
  - service=api-c:     200 series  (20%)
  - service=legacy:    500 series  (50%)  <- TOP OFFENDER
                      -----
  Total:             1400 series (over limit by 400)

Adaptive action:
  1. Sort groups by contribution (descending): legacy(500), api-a(400), api-b(300), api-c(200)
  2. Mark "legacy" for dropping (500 >= 400 excess)
  3. Keep api-a, api-b, api-c (within limit now: 900 series)

Result: Only legacy service metrics are dropped for this window
```

### Matching Rules

- **metric_name**: Exact match or regex pattern (e.g., `http_request_.*`)
- **labels**: Key-value pairs where `*` matches any value
- **group_by**: Labels to use for tracking groups (required for adaptive)
- Rules are evaluated in order; first match wins

### Dry Run Mode

By default, limits run in dry-run mode (`-limits-dry-run=true`). This logs all violations but doesn't actually drop data. Use this to:

1. Understand your metrics cardinality before enforcing limits
2. Tune limit thresholds based on actual traffic
3. Safely test limit configurations in production

To enable actual enforcement:

```bash
metrics-governor -limits-config limits.yaml -limits-dry-run=false
```

### Action Examples

#### Adaptive Action (Intelligent Limiting)

Use `action: adaptive` to intelligently drop only the top offenders while preserving smaller contributors.

```yaml
rules:
  - name: "adaptive-by-service"
    match:
      labels:
        env: "prod"
        service: "*"
    max_datapoints_rate: 100000
    max_cardinality: 5000
    action: adaptive
    group_by: ["service"]  # Track and limit per service
```

**Log output when adaptive limiting kicks in:**
```json
{"timestamp":"2024-01-26T12:00:00Z","level":"warn","message":"limit exceeded","fields":{"rule":"adaptive-by-service","metric":"http_requests_total","group":"service=legacy-app","reason":"cardinality","action":"adaptive","dry_run":false,"datapoints":100}}
{"timestamp":"2024-01-26T12:00:00Z","level":"info","message":"adaptive: marked group for dropping","fields":{"rule":"adaptive-by-service","group":"service=legacy-app","reason":"cardinality","contribution_datapoints":5000,"contribution_cardinality":3000}}
```

**Prometheus metrics exposed:**
```
metrics_governor_limit_cardinality_exceeded_total{rule="adaptive-by-service"} 42
metrics_governor_limit_datapoints_dropped_total{rule="adaptive-by-service"} 5000
metrics_governor_limit_datapoints_passed_total{rule="adaptive-by-service"} 95000
metrics_governor_limit_groups_dropped_total{rule="adaptive-by-service"} 2
metrics_governor_rule_current_cardinality{rule="adaptive-by-service"} 4500
metrics_governor_rule_groups_total{rule="adaptive-by-service"} 15
metrics_governor_rule_dropped_groups_total{rule="adaptive-by-service"} 2
```

#### Log Action (Monitoring Only)

Use `action: log` to monitor limit violations without affecting data flow.

```yaml
rules:
  - name: "monitor-only"
    match:
      metric_name: "http_request_duration_.*"
    max_cardinality: 5000
    action: log
```

#### Drop Action (Hard Limit)

Use `action: drop` when you want to completely block metrics that exceed limits. Use sparingly.

```yaml
rules:
  - name: "block-known-bad"
    match:
      metric_name: "known_problematic_metric"
    max_cardinality: 100
    action: drop
```

#### Combined Example

A complete configuration using adaptive limiting as the primary strategy:

```yaml
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log  # Safe default

rules:
  # Production: Adaptive limiting by service
  - name: "prod-service-limits"
    match:
      labels:
        env: "prod"
        service: "*"
    max_datapoints_rate: 100000
    max_cardinality: 5000
    action: adaptive
    group_by: ["service"]

  # HTTP metrics: Adaptive by service+endpoint
  - name: "http-endpoint-limits"
    match:
      metric_name: "http_request_.*"
    max_cardinality: 2000
    action: adaptive
    group_by: ["service", "endpoint"]

  # Legacy apps: Strict adaptive control
  - name: "legacy-limits"
    match:
      metric_name: "legacy_.*"
    max_cardinality: 100
    action: adaptive
    group_by: ["service", "env"]

  # Dev: Just monitor
  - name: "dev-monitor"
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

## Development

### Running Tests

```bash
# Run all tests
make test

# Run tests with verbose output
make test-verbose

# Run tests with coverage report
make test-coverage
```

Coverage report is generated at `bin/coverage.html`.

### Project Structure

```
metrics-governor/
├── cmd/metrics-governor/    # Main application entry point
├── internal/
│   ├── buffer/              # Metrics buffering and batching
│   ├── config/              # Configuration management
│   ├── exporter/            # OTLP gRPC exporter
│   ├── limits/              # Limits enforcement (adaptive, drop, log)
│   ├── logging/             # JSON structured logging
│   ├── receiver/            # gRPC and HTTP receivers
│   └── stats/               # Statistics collection
├── examples/                # Example configuration files
├── test/                    # Integration test environment
├── bin/                     # Build output directory
├── Dockerfile
├── docker-compose.yaml
└── Makefile
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
