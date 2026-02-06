# Configuration

metrics-governor supports two configuration methods:
1. **YAML configuration file** (recommended for complex setups)
2. **CLI flags** (for simple setups or quick overrides)

> **Dual Pipeline Support**: All components (receivers, buffers, exporters, limits, sharding, queues) work identically for both OTLP and PRW pipelines. They are completely separate - OTLP options use standard flags, PRW options use `-prw-*` prefixed flags.

## Supported Backends

metrics-governor can export metrics to any OTLP or Prometheus Remote Write compatible backend:

### OTLP Protocol (gRPC or HTTP)

| Backend | Protocol | Default Path | Notes |
|---------|----------|--------------|-------|
| **OpenTelemetry Collector** | gRPC (4317) or HTTP (4318) | `/v1/metrics` | Most common setup |
| **Prometheus** (with OTLP receiver) | gRPC (4317) | `/v1/metrics` | Requires `--enable-feature=otlp-write-receiver` |
| **Grafana Mimir** | gRPC or HTTP | `/otlp/v1/metrics` | Native OTLP support |
| **Cortex** | gRPC or HTTP | `/api/v1/push` | Via OTLP receiver |
| **Thanos** | gRPC | `/v1/metrics` | Via sidecar or receive |
| **VictoriaMetrics** | HTTP only | `/opentelemetry/v1/metrics` | Native OTLP support |
| **ClickHouse** | gRPC or HTTP | `/v1/metrics` | Via OTLP receiver |
| **Grafana Cloud** | gRPC or HTTP | `/otlp/v1/metrics` | Cloud hosted |

### Prometheus Remote Write (PRW)

| Backend | Default Path | Notes |
|---------|--------------|-------|
| **Prometheus** | `/api/v1/write` | Native PRW support |
| **VictoriaMetrics** | `/api/v1/write` or `/write` | Use `-prw-exporter-vm-mode` for optimizations |
| **Grafana Mimir** | `/api/v1/push` | PRW compatible |
| **Cortex** | `/api/v1/push` | PRW compatible |
| **Thanos Receive** | `/api/v1/receive` | PRW compatible |
| **Grafana Cloud** | `/api/prom/push` | Cloud hosted |
| **Amazon Managed Prometheus** | `/api/v1/remote_write` | AWS hosted |
| **Google Cloud Managed Prometheus** | Custom | GCP hosted |

## YAML Configuration File

Use the `-config` flag to specify a YAML configuration file:

```bash
metrics-governor -config /etc/metrics-governor/config.yaml
```

### Example Configuration

```yaml
receiver:
  grpc:
    address: ":4317"
  http:
    address: ":4318"
    path: "/v1/metrics"  # Custom path for OTLP HTTP receiver
    server:
      max_request_body_size: 10485760  # 10MB
      read_header_timeout: 30s
      write_timeout: 1m
  tls:
    enabled: true
    cert_file: "/etc/tls/server.crt"
    key_file: "/etc/tls/server.key"

exporter:
  endpoint: "otel-collector:4317"
  protocol: "grpc"
  default_path: "/v1/metrics"  # Default path for HTTP exporter (when endpoint has no path)
  insecure: false
  timeout: 60s
  tls:
    enabled: true
    ca_file: "/etc/tls/ca.crt"
  compression:
    type: "gzip"
    level: 6

buffer:
  size: 50000
  batch_size: 2000
  max_batch_bytes: 8388608  # 8MB byte-aware batch splitting
  flush_interval: 10s

stats:
  address: ":9090"
  labels:
    - service
    - env

limits:
  dry_run: false

# Prometheus Remote Write (optional)
prw:
  receiver:
    address: ":9090"
    path: "/api/v1/write"  # Custom PRW receiver path (empty = register both /api/v1/write and /write)
  exporter:
    endpoint: "http://victoriametrics:8428"
    default_path: "/api/v1/write"  # Default PRW exporter path (when endpoint has no path)
```

See [examples/config.yaml](../examples/config.yaml) for a complete example with all options documented.

For Prometheus Remote Write configuration, see [prw.md](./prw.md).

For queue resilience features (circuit breaker, exponential backoff, memory limits), see [resilience.md](./resilience.md).

Additional example configs:
- [examples/config-minimal.yaml](../examples/config-minimal.yaml) - Minimal configuration
- [examples/config-production.yaml](../examples/config-production.yaml) - Production-ready settings

## CLI Flags

All settings can also be configured via CLI flags.

### Configuration Flag

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | | Path to YAML configuration file |

### Receiver Options

| Flag | Default | Description |
|------|---------|-------------|
| `-grpc-listen` | `:4317` | gRPC receiver listen address |
| `-http-listen` | `:4318` | HTTP receiver listen address |
| `-http-receiver-path` | `/v1/metrics` | URL path for HTTP receiver |
| `-receiver-tls-enabled` | `false` | Enable TLS for receivers |
| `-receiver-tls-cert` | | Path to server certificate file |
| `-receiver-tls-key` | | Path to server private key file |
| `-receiver-tls-ca` | | Path to CA certificate for client verification (mTLS) |
| `-receiver-tls-client-auth` | `false` | Require client certificates (mTLS) |
| `-receiver-auth-enabled` | `false` | Enable authentication for receivers |
| `-receiver-auth-bearer-token` | | Expected bearer token for authentication |
| `-receiver-auth-basic-username` | | Basic auth username |
| `-receiver-auth-basic-password` | | Basic auth password |

### Exporter Options

The OTLP exporter supports any OTLP-compatible backend via gRPC or HTTP protocols: OpenTelemetry Collector, Prometheus, Grafana Mimir, Cortex, Thanos, VictoriaMetrics, and others.

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-endpoint` | `localhost:4317` | OTLP exporter endpoint (host:port for gRPC, URL for HTTP) |
| `-exporter-protocol` | `grpc` | Exporter protocol: `grpc` (recommended, most backends) or `http` |
| `-exporter-default-path` | `/v1/metrics` | Default HTTP path when endpoint has no path. Standard: `/v1/metrics`. VictoriaMetrics: `/opentelemetry/v1/metrics` |
| `-exporter-insecure` | `true` | Use insecure connection (no TLS) for exporter |
| `-exporter-timeout` | `30s` | Exporter request timeout |
| `-exporter-tls-enabled` | `false` | Enable custom TLS config for exporter |
| `-exporter-tls-cert` | | Path to client certificate file (mTLS) |
| `-exporter-tls-key` | | Path to client private key file (mTLS) |
| `-exporter-tls-ca` | | Path to CA certificate for server verification |
| `-exporter-tls-skip-verify` | `false` | Skip TLS certificate verification |
| `-exporter-tls-server-name` | | Override server name for TLS verification |
| `-exporter-auth-bearer-token` | | Bearer token to send with requests |
| `-exporter-auth-basic-username` | | Basic auth username |
| `-exporter-auth-basic-password` | | Basic auth password |
| `-exporter-auth-headers` | | Custom headers (format: `key1=value1,key2=value2`) |

### Buffer Options

| Flag | Default | Description |
|------|---------|-------------|
| `-buffer-size` | `10000` | Maximum number of metrics to buffer |
| `-flush-interval` | `5s` | Buffer flush interval |
| `-batch-size` | `1000` | Maximum batch size for export (by count) |
| `-max-batch-bytes` | `8388608` | Maximum batch size in bytes (8MB). Batches exceeding this are recursively split. Set below backend limit. 0 disables byte splitting. |

### Stats Options

| Flag | Default | Description |
|------|---------|-------------|
| `-stats-addr` | `:9090` | Stats/metrics HTTP endpoint address |
| `-stats-labels` | | Comma-separated labels to track (e.g., `service,env,cluster`) |

### Limits Options

| Flag | Default | Description |
|------|---------|-------------|
| `-limits-config` | | Path to limits configuration YAML file |
| `-limits-dry-run` | `true` | Dry run mode: log violations but don't drop/sample |

### Queue Options (FastQueue)

The queue uses a high-performance FastQueue implementation inspired by VictoriaMetrics' persistentqueue. It provides metadata-only persistence with in-memory buffering for high throughput. See [resilience.md](./resilience.md) for circuit breaker and backoff documentation.

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-enabled` | `true` | Enable failover queue (safety net for export failures) |
| `-queue-type` | `memory` | Queue type: `memory` (bounded in-memory, fast) or `disk` (FastQueue, durable, survives restarts) |
| `-queue-path` | `./queue` | Queue storage directory (disk mode only) |
| `-queue-max-size` | `10000` | Maximum number of batches in queue |
| `-queue-max-bytes` | `1073741824` | Maximum total queue size in bytes (1GB) |
| `-queue-retry-interval` | `5s` | Initial retry interval |
| `-queue-max-retry-delay` | `5m` | Maximum retry backoff delay |
| `-queue-full-behavior` | `drop_oldest` | Queue full behavior: `drop_oldest`, `drop_newest`, or `block` |
| `-queue-adaptive-enabled` | `true` | Enable adaptive queue sizing (disk mode only) |
| `-queue-target-utilization` | `0.85` | Target disk utilization (0.0-1.0, disk mode only) |
| `-queue-inmemory-blocks` | `2048` | In-memory channel size for fast path (disk mode only) |
| `-queue-chunk-size` | `536870912` | Chunk file size in bytes (512MB, disk mode only) |
| `-queue-meta-sync` | `1s` | Metadata sync interval (max data loss window, disk mode only) |
| `-queue-stale-flush` | `30s` | Interval to flush stale in-memory blocks to disk (disk mode only) |
| `-queue-write-buffer-size` | `262144` | Buffered writer size in bytes (256KB, disk mode only) |
| `-queue-compression` | `snappy` | Queue block compression: `none`, `snappy` (disk mode only) |
| `-queue-backoff-enabled` | `true` | Enable exponential backoff for retries |
| `-queue-backoff-multiplier` | `2.0` | Backoff delay multiplier on each failure |
| `-queue-circuit-breaker-enabled` | `true` | Enable circuit breaker pattern |
| `-queue-circuit-breaker-threshold` | `10` | Consecutive failures to trip circuit |
| `-queue-circuit-breaker-reset-timeout` | `30s` | Time before half-open state |

### PRW Receiver Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-listen` | | PRW receiver address (empty = disabled) |
| `-prw-receiver-path` | `/api/v1/write` | URL path for PRW receiver (empty = register both `/api/v1/write` and `/write`) |
| `-prw-receiver-version` | `auto` | Protocol version: `1.0`, `2.0`, or `auto` |
| `-prw-receiver-tls-enabled` | `false` | Enable TLS for PRW receiver |
| `-prw-receiver-tls-cert` | | Certificate file path |
| `-prw-receiver-tls-key` | | Private key file path |
| `-prw-receiver-auth-enabled` | `false` | Enable authentication |
| `-prw-receiver-auth-bearer-token` | | Expected bearer token |

### PRW Exporter Options

The Prometheus Remote Write exporter supports any PRW-compatible backend: Prometheus, Grafana Mimir, Cortex, Thanos, VictoriaMetrics, and others.

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-endpoint` | | PRW backend URL (empty = disabled) |
| `-prw-exporter-default-path` | `/api/v1/write` | Default PRW path when endpoint has no path. Standard: `/api/v1/write`. Mimir/Cortex: `/api/v1/push`. Thanos: `/api/v1/receive` |
| `-prw-exporter-version` | `auto` | Protocol version: `1.0` (standard), `2.0` (native histograms), or `auto` |
| `-prw-exporter-timeout` | `30s` | Request timeout |
| `-prw-exporter-tls-enabled` | `false` | Enable TLS |
| `-prw-exporter-tls-cert` | | Client certificate (mTLS) |
| `-prw-exporter-tls-key` | | Client key (mTLS) |
| `-prw-exporter-tls-ca` | | CA certificate |
| `-prw-exporter-auth-bearer-token` | | Bearer token for auth |
| `-prw-exporter-vm-mode` | `false` | Enable VictoriaMetrics mode |
| `-prw-exporter-vm-compression` | `snappy` | Compression: `snappy` or `zstd` |

### PRW Buffer Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-buffer-size` | `10000` | Maximum requests in buffer |
| `-prw-flush-interval` | `5s` | Flush interval |
| `-prw-batch-size` | `1000` | Batch size for export |

### PRW Queue Options

The PRW queue uses the same high-performance disk-backed `SendQueue` as the OTLP pipeline, providing persistent storage, circuit breaker, exponential backoff, and split-on-error. See [resilience.md](./resilience.md) for detailed resilience documentation.

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-queue-enabled` | `false` | Enable persistent retry queue |
| `-prw-queue-path` | `./prw-queue` | Queue directory (disk-backed, survives restarts) |
| `-prw-queue-max-size` | `10000` | Max queue entries |
| `-prw-queue-max-bytes` | `1073741824` | Max queue size in bytes (1GB) |
| `-prw-queue-retry-interval` | `5s` | Initial retry interval |
| `-prw-queue-max-retry-delay` | `5m` | Maximum retry backoff delay |
| `-prw-queue-backoff-enabled` | `true` | Enable exponential backoff for retries |
| `-prw-queue-backoff-multiplier` | `2.0` | Multiply delay by this on each failure |
| `-prw-queue-circuit-breaker-enabled` | `true` | Enable circuit breaker pattern |
| `-prw-queue-circuit-breaker-threshold` | `10` | Consecutive failures before opening circuit |
| `-prw-queue-circuit-breaker-reset-timeout` | `30s` | Time before half-open state |

### PRW Sharding Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-sharding-enabled` | `false` | Enable consistent sharding |
| `-prw-sharding-headless-service` | | K8s headless service DNS name with port |
| `-prw-sharding-labels` | | Comma-separated labels for shard key |
| `-prw-sharding-dns-refresh-interval` | `30s` | DNS refresh interval |
| `-prw-sharding-virtual-nodes` | `150` | Virtual nodes per endpoint |

### OTLP Queue Options

The queue provides durability for export failures with memory or disk-backed storage. Memory mode (default) is fast with bounded in-memory queue. Disk mode uses a high-performance FastQueue implementation. See [resilience.md](./resilience.md) for detailed information on circuit breaker, backoff, failover queue, and split-on-error behavior.

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-enabled` | `true` | Enable failover queue (safety net for export failures) |
| `-queue-type` | `memory` | Queue type: `memory` (bounded, fast) or `disk` (FastQueue, durable) |
| `-queue-path` | `./queue` | Queue directory path (disk mode only) |
| `-queue-max-size` | `10000` | Max queue entries |
| `-queue-max-bytes` | `1073741824` | Max queue size in bytes (1GB) |
| `-queue-retry-interval` | `5s` | Initial retry interval |
| `-queue-max-retry-delay` | `5m` | Maximum retry backoff delay |
| `-queue-full-behavior` | `drop_oldest` | Behavior when full: `drop_oldest`, `drop_newest`, `block` |
| `-queue-adaptive-enabled` | `true` | Enable adaptive queue sizing (disk mode only) |
| `-queue-target-utilization` | `0.85` | Target disk utilization (disk mode only) |
| `-queue-inmemory-blocks` | `256` | In-memory channel size (disk mode only) |
| `-queue-chunk-size` | `536870912` | Chunk file size in bytes (disk mode only) |
| `-queue-meta-sync` | `1s` | Metadata sync interval (disk mode only) |
| `-queue-stale-flush` | `5s` | Flush stale in-memory blocks to disk (disk mode only) |

### Queue Resilience Options

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-backoff-enabled` | `true` | Enable exponential backoff for retries |
| `-queue-backoff-multiplier` | `2.0` | Multiply delay by this on each failure |
| `-queue-circuit-breaker-enabled` | `true` | Enable circuit breaker pattern |
| `-queue-circuit-breaker-threshold` | `10` | Consecutive failures before opening circuit |
| `-queue-circuit-breaker-reset-timeout` | `30s` | Time before half-open state |

### Memory Limit Options

| Flag | Default | Description |
|------|---------|-------------|
| `-memory-limit-ratio` | `0.9` | Ratio of container memory for GOMEMLIMIT (0.0-1.0, 0=disabled) |

### Sharding Options

| Flag | Default | Description |
|------|---------|-------------|
| `-sharding-enabled` | `false` | Enable consistent sharding |
| `-sharding-headless-service` | | K8s headless service DNS name with port |
| `-sharding-labels` | | Comma-separated labels for shard key |
| `-sharding-dns-refresh-interval` | `30s` | DNS refresh interval |
| `-sharding-virtual-nodes` | `150` | Virtual nodes per endpoint |
| `-sharding-fallback-on-empty` | `false` | Fall back to default exporter if no labels match |

### Performance Options

| Flag | Default | Description |
|------|---------|-------------|
| `-export-concurrency` | `0` | Max concurrent export goroutines (0 = NumCPU * 4) |
| `-string-interning` | `true` | Enable string interning for label deduplication |
| `-intern-max-value-length` | `64` | Max length for label value interning |

### Telemetry Options (OTLP Self-Monitoring)

| Flag | Default | Description |
|------|---------|-------------|
| `-telemetry-endpoint` | | OTLP endpoint for self-monitoring (empty = disabled) |
| `-telemetry-protocol` | `grpc` | OTLP protocol: `grpc` or `http` |
| `-telemetry-insecure` | `true` | Use insecure connection for OTLP telemetry |

When `-telemetry-endpoint` is set, metrics-governor exports its own logs (as OTLP log records) and Prometheus metrics (bridged to OTLP metric format) to the specified endpoint.

### HTTP Client Tuning (Exporter)

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-max-idle-conns` | `100` | Maximum idle connections across all hosts |
| `-exporter-max-idle-conns-per-host` | `100` | Maximum idle connections per host |
| `-exporter-max-conns-per-host` | `0` | Maximum total connections per host (0 = unlimited) |
| `-exporter-idle-conn-timeout` | `90s` | Idle connection timeout |
| `-exporter-disable-keep-alives` | `false` | Disable HTTP keep-alives |
| `-exporter-force-http2` | `false` | Force HTTP/2 for non-TLS connections |
| `-exporter-http2-read-idle-timeout` | `0` | HTTP/2 read idle timeout |
| `-exporter-http2-ping-timeout` | `0` | HTTP/2 ping timeout |

### Compression Options (Exporter)

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-compression` | `none` | Compression: `none`, `gzip`, `zstd`, `snappy`, `zlib`, `deflate` |
| `-exporter-compression-level` | `0` | Compression level (algorithm-specific) |

### Receiver HTTP Server Tuning

| Flag | Default | Description |
|------|---------|-------------|
| `-receiver-read-timeout` | `0` | HTTP server read timeout |
| `-receiver-read-header-timeout` | `1m` | HTTP server read header timeout |
| `-receiver-write-timeout` | `30s` | HTTP server write timeout |
| `-receiver-idle-timeout` | `1m` | HTTP server idle timeout |
| `-receiver-keep-alives-enabled` | `true` | Enable HTTP keep-alives for receiver |
| `-prw-receiver-max-body-size` | `0` | Maximum PRW request body size (0 = no limit) |
| `-prw-receiver-read-timeout` | `1m` | PRW receiver read timeout |
| `-prw-receiver-write-timeout` | `30s` | PRW receiver write timeout |

### PRW Queue Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-queue-enabled` | `false` | Enable persistent retry queue for PRW |
| `-prw-queue-path` | `./prw-queue` | PRW queue storage directory |
| `-prw-queue-max-size` | `10000` | Max PRW queue entries |
| `-prw-queue-max-bytes` | `1073741824` | Max PRW queue size in bytes (1GB) |
| `-prw-queue-retry-interval` | `5s` | PRW queue retry interval |
| `-prw-queue-max-retry-delay` | `5m` | Maximum PRW retry delay |

### PRW Limits Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-limits-enabled` | `false` | Enable limits for PRW pipeline |
| `-prw-limits-config` | | Path to PRW limits configuration YAML |
| `-prw-limits-dry-run` | `true` | PRW limits dry run mode |

### Cardinality Tracking Options

| Flag | Default | Description |
|------|---------|-------------|
| `-cardinality-mode` | `bloom` | Tracking mode: `bloom`, `hll`, `exact`, or `hybrid` |
| `-cardinality-expected-items` | `100000` | Expected unique items per tracker (Bloom sizing) |
| `-cardinality-fp-rate` | `0.01` | Bloom filter false positive rate (1% = 0.01) |
| `-cardinality-hll-threshold` | `10000` | Hybrid: cardinality at which Bloom switches to HLL |
| `-cardinality-hll-precision` | `14` | HLL precision (registers = 2^precision, 14 = ~12 KB) |

### Bloom Persistence Options

| Flag | Default | Description |
|------|---------|-------------|
| `-bloom-persistence-enabled` | `false` | Enable bloom filter state persistence |
| `-bloom-persistence-path` | `./bloom-state` | Directory for persistence files |
| `-bloom-persistence-save-interval` | `30s` | Interval between periodic saves |
| `-bloom-persistence-state-ttl` | `1h` | Unused tracker cleanup TTL |
| `-bloom-persistence-cleanup-interval` | `5m` | Interval between cleanup runs |
| `-bloom-persistence-max-size` | `500MB` | Maximum disk space for bloom state |
| `-bloom-persistence-max-memory` | `256MB` | Maximum memory for in-memory bloom filters |
| `-bloom-persistence-compression` | `true` | Enable gzip compression for state files |
| `-bloom-persistence-compression-level` | `1` | Gzip compression level (1=fast, 9=best) |

### Stats Options (Extended)

| Flag | Default | Description |
|------|---------|-------------|
| `-stats-log-interval` | `10s` | Operational stats log interval (0 = disabled) |

### Limits Options (Extended)

| Flag | Default | Description |
|------|---------|-------------|
| `-limits-log-interval` | `10s` | Limits enforcement summary log interval |
| `-limits-log-individual` | `false` | Log individual limit violations |
| `-rule-cache-max-size` | `10000` | Maximum entries in rule matching LRU cache |

### Shutdown

| Flag | Default | Description |
|------|---------|-------------|
| `-shutdown-timeout` | `30s` | Graceful shutdown timeout |

### General

| Flag | Description |
|------|-------------|
| `-h`, `-help` | Show help message |
| `-v`, `-version` | Show version |

## Configuration Priority

When both YAML config and CLI flags are used, the priority is:

1. **CLI flags** (highest priority) - explicitly set flags override config file
2. **YAML config file** - values from the config file
3. **Built-in defaults** (lowest priority)

Example combining config file with CLI override:

```bash
# Use config file but override the exporter endpoint
metrics-governor -config config.yaml -exporter-endpoint otel:4317
```

## Usage Examples

### Basic Usage

```bash
# Start with default settings (gRPC to localhost:4317)
metrics-governor

# Use YAML configuration file
metrics-governor -config /etc/metrics-governor/config.yaml

# Use config file with CLI overrides
metrics-governor -config config.yaml -exporter-endpoint otel:4317

# Custom receiver ports
metrics-governor -grpc-listen :5317 -http-listen :5318
```

### OTLP Exporter - Backend Examples

metrics-governor supports exporting OTLP metrics to any OTLP-compatible backend via gRPC or HTTP:

```bash
# OpenTelemetry Collector (gRPC - default, most common)
metrics-governor -exporter-endpoint otel-collector:4317

# OpenTelemetry Collector (HTTP)
metrics-governor -exporter-endpoint otel-collector:4318 -exporter-protocol http

# OpenTelemetry Collector with gzip compression
metrics-governor -exporter-endpoint otel-collector:4317 -exporter-compression gzip

# Grafana Mimir (gRPC)
metrics-governor -exporter-endpoint mimir:4317

# Grafana Mimir (HTTP with custom path)
metrics-governor -exporter-endpoint http://mimir:8080 -exporter-protocol http \
  -exporter-default-path /otlp/v1/metrics

# VictoriaMetrics (OTLP/HTTP with VM-specific path)
metrics-governor -exporter-endpoint http://victoriametrics:8428 -exporter-protocol http \
  -exporter-default-path /opentelemetry/v1/metrics -exporter-compression zstd

# Prometheus (with OTLP receiver enabled)
metrics-governor -exporter-endpoint prometheus:4317

# Cortex (gRPC)
metrics-governor -exporter-endpoint cortex:4317

# Thanos Receive (gRPC)
metrics-governor -exporter-endpoint thanos-receive:4317

# Secure endpoint with TLS
metrics-governor -exporter-endpoint secure-backend:4317 \
  -exporter-insecure=false -exporter-tls-ca /etc/certs/ca.crt

# Endpoint with bearer token auth
metrics-governor -exporter-endpoint backend:4317 \
  -exporter-auth-bearer-token "your-token-here"
```

### Prometheus Remote Write - Backend Examples

For Prometheus Remote Write protocol (PRW) support:

```bash
# VictoriaMetrics (standard PRW path)
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://victoriametrics:8428

# VictoriaMetrics with VM mode optimizations (zstd compression, extra labels)
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://victoriametrics:8428 \
  -prw-exporter-vm-mode=true -prw-exporter-vm-compression zstd

# Prometheus (PRW 1.0)
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://prometheus:9090 \
  -prw-exporter-version 1.0

# Grafana Mimir
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://mimir:8080 \
  -prw-exporter-default-path /api/v1/push

# Cortex
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://cortex:9009 \
  -prw-exporter-default-path /api/v1/push

# Thanos Receive
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://thanos-receive:19291 \
  -prw-exporter-default-path /api/v1/receive
```

### Dual Pipeline (OTLP + PRW)

```bash
# Run both OTLP and PRW pipelines simultaneously
metrics-governor \
  -grpc-listen :4317 \
  -exporter-endpoint otel-collector:4317 \
  -prw-listen :9090 \
  -prw-exporter-endpoint http://victoriametrics:8428
```

### Buffering and Performance

```bash
# Adjust buffering for high throughput
metrics-governor -buffer-size 50000 -flush-interval 10s -batch-size 2000

# Byte-aware batch splitting (default 8MB, set below backend limit)
metrics-governor -max-batch-bytes 8388608

# Enable stats tracking by service, environment and cluster
metrics-governor -stats-labels service,env,cluster

# Performance tuning: limit concurrent exports
metrics-governor -export-concurrency 32

# High-load environment with byte splitting
metrics-governor -export-concurrency 64 -buffer-size 100000 -batch-size 5000 -max-batch-bytes 8388608
```

### Limits Enforcement

```bash
# Enable limits enforcement (dry-run by default)
metrics-governor -limits-config /etc/metrics-governor/limits.yaml

# Enable limits enforcement with actual drop/sample
metrics-governor -limits-config /etc/metrics-governor/limits.yaml -limits-dry-run=false
```

## Performance Tuning

metrics-governor includes performance optimizations for high-throughput environments. These techniques are inspired by concepts described in VictoriaMetrics blog articles on TSDB optimization:

- [How VictoriaMetrics makes instant snapshots](https://valyala.medium.com/how-victoriametrics-makes-instant-snapshots-for-multi-terabyte-time-series-data-e1f3fb0e0282)
- [VictoriaMetrics achieving high performance](https://valyala.medium.com/victoriametrics-achieving-better-compression-for-time-series-data-than-gorilla-317bc1f95932)

> **Note**: These are original implementations using standard Go patterns (`sync.Map`, channel-based semaphores), not copied code from VictoriaMetrics. We only adopted the conceptual approaches.

### String Interning

When enabled (default), identical label names and values are deduplicated in memory for the PRW pipeline:

- **Prometheus labels** (e.g., `__name__`, `job`, `instance`) are always interned
- **Label values** shorter than `intern-max-value-length` (default: 64) are interned
- Applied to PRW label parsing and shard key building
- Reduces memory allocations by up to 66% for PRW unmarshal operations
- Achieves 99%+ cache hit rate for common labels

### Concurrency Limiting

Prevents goroutine explosion when exporting to multiple sharded endpoints:

- Default limit: `NumCPU * 4` (e.g., 32 on 8-core machine)
- Set `-export-concurrency=0` to use default
- Reduces concurrent goroutines by ~88% under high load

### Recommended Settings

| Environment | export-concurrency | string-interning |
|-------------|-------------------|------------------|
| Development | 0 (default) | true |
| Production | 32-64 | true |
| Memory-constrained | 16 | true |
| Ultra-low-latency | 128+ | false |
