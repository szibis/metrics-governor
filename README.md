# metrics-governor

OTLP metrics proxy with buffering and statistics. Receives metrics via gRPC and HTTP, buffers them, tracks cardinality and datapoints, and forwards to a configurable OTLP endpoint with batching support.

## Features

- OTLP gRPC receiver (default: `:4317`)
- OTLP HTTP receiver (default: `:4318`)
- Configurable metrics buffering
- Batch export with configurable size
- Graceful shutdown with final flush
- JSON structured logging
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

## License

See [LICENSE](LICENSE) file.
