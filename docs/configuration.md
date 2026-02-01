# Configuration

metrics-governor supports two configuration methods:
1. **YAML configuration file** (recommended for complex setups)
2. **CLI flags** (for simple setups or quick overrides)

> **Dual Pipeline Support**: All components (receivers, buffers, exporters, limits, sharding, queues) work identically for both OTLP and PRW pipelines. They are completely separate - OTLP options use standard flags, PRW options use `-prw-*` prefixed flags.

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
  exporter:
    endpoint: "http://victoriametrics:8428"
```

See [examples/config.yaml](../examples/config.yaml) for a complete example with all options documented.

For Prometheus Remote Write configuration, see [prw.md](./prw.md).

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

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-endpoint` | `localhost:4317` | OTLP exporter endpoint |
| `-exporter-protocol` | `grpc` | Exporter protocol: `grpc` or `http` |
| `-exporter-insecure` | `true` | Use insecure connection for exporter |
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
| `-batch-size` | `1000` | Maximum batch size for export |

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

The queue uses a high-performance FastQueue implementation inspired by VictoriaMetrics' persistentqueue. It provides metadata-only persistence with in-memory buffering for high throughput.

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-enabled` | `false` | Enable persistent queue for export retries |
| `-queue-path` | `./queue` | Queue storage directory |
| `-queue-max-size` | `10000` | Maximum number of batches in queue |
| `-queue-max-bytes` | `1073741824` | Maximum total queue size in bytes (1GB) |
| `-queue-retry-interval` | `5s` | Initial retry interval |
| `-queue-max-retry-delay` | `5m` | Maximum retry backoff delay |
| `-queue-full-behavior` | `drop_oldest` | Queue full behavior: `drop_oldest`, `drop_newest`, or `block` |
| `-queue-inmemory-blocks` | `256` | In-memory channel size for fast path |
| `-queue-chunk-size` | `536870912` | Chunk file size in bytes (512MB) |
| `-queue-meta-sync` | `1s` | Metadata sync interval (max data loss window) |
| `-queue-stale-flush` | `5s` | Interval to flush stale in-memory blocks to disk |

### PRW Receiver Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-listen` | | PRW receiver address (empty = disabled) |
| `-prw-receiver-version` | `auto` | Protocol version: `1.0`, `2.0`, or `auto` |
| `-prw-receiver-tls-enabled` | `false` | Enable TLS for PRW receiver |
| `-prw-receiver-tls-cert` | | Certificate file path |
| `-prw-receiver-tls-key` | | Private key file path |
| `-prw-receiver-auth-enabled` | `false` | Enable authentication |
| `-prw-receiver-auth-bearer-token` | | Expected bearer token |

### PRW Exporter Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-endpoint` | | PRW backend URL (empty = disabled) |
| `-prw-exporter-version` | `auto` | Protocol version: `1.0`, `2.0`, or `auto` |
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

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-queue-enabled` | `false` | Enable persistent retry queue |
| `-prw-queue-path` | `./prw-queue` | Queue directory |
| `-prw-queue-max-size` | `10000` | Max queue entries |
| `-prw-queue-retry-interval` | `5s` | Initial retry interval |

### PRW Sharding Options

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-sharding-enabled` | `false` | Enable consistent sharding |
| `-prw-sharding-headless-service` | | K8s headless service DNS name with port |
| `-prw-sharding-labels` | | Comma-separated labels for shard key |
| `-prw-sharding-dns-refresh-interval` | `30s` | DNS refresh interval |
| `-prw-sharding-virtual-nodes` | `150` | Virtual nodes per endpoint |

### OTLP Queue Options

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-enabled` | `false` | Enable persistent retry queue |
| `-queue-path` | `./queue` | Queue directory path |
| `-queue-max-size` | `10000` | Max queue entries |
| `-queue-max-bytes` | `536870912` | Max queue size in bytes (512MB) |
| `-queue-retry-interval` | `5s` | Initial retry interval |
| `-queue-full-behavior` | `drop_oldest` | Behavior when full: `drop_oldest`, `drop_newest`, `block` |
| `-queue-adaptive-enabled` | `true` | Enable adaptive queue sizing |
| `-queue-sync-mode` | `batched` | Sync mode: `immediate`, `batched`, `async` |
| `-queue-sync-batch-size` | `100` | Number of entries before sync (batched mode) |
| `-queue-sync-interval` | `100ms` | Time interval for sync (batched mode) |
| `-queue-compression` | `false` | Enable snappy compression for WAL entries |
| `-queue-write-ahead` | `true` | Enable write-ahead logging for crash safety |

> **Warning: WAL Compression CPU Impact**
>
> Enabling `-queue-compression=true` can significantly increase CPU usage at high throughput.
> At 200k datapoints/s, compression can add 60-100% CPU overhead due to snappy compression
> on every WAL write. For high-throughput environments, we recommend:
> - `-queue-compression=false` (default)
> - `-queue-sync-batch-size=1000` (batch more entries per sync)
> - `-queue-sync-interval=250ms` (less frequent syncs with bigger batches)

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

```bash
# Start with default settings
metrics-governor

# Use YAML configuration file
metrics-governor -config /etc/metrics-governor/config.yaml

# Use config file with CLI overrides
metrics-governor -config config.yaml -exporter-endpoint otel:4317

# Custom receiver ports
metrics-governor -grpc-listen :5317 -http-listen :5318

# Forward to remote gRPC endpoint
metrics-governor -exporter-endpoint otel-collector:4317

# Forward to remote HTTP endpoint
metrics-governor -exporter-endpoint otel-collector:4318 -exporter-protocol http

# Adjust buffering
metrics-governor -buffer-size 50000 -flush-interval 10s -batch-size 2000

# Enable stats tracking by service, environment and cluster
metrics-governor -stats-labels service,env,cluster

# Enable limits enforcement (dry-run by default)
metrics-governor -limits-config /etc/metrics-governor/limits.yaml

# Enable limits enforcement with actual drop/sample
metrics-governor -limits-config /etc/metrics-governor/limits.yaml -limits-dry-run=false

# Enable Prometheus Remote Write pipeline
metrics-governor -prw-listen :9090 -prw-exporter-endpoint http://victoriametrics:8428

# Run both OTLP and PRW pipelines
metrics-governor \
  -grpc-listen :4317 \
  -exporter-endpoint otel-collector:4317 \
  -prw-listen :9090 \
  -prw-exporter-endpoint http://victoriametrics:8428

# Performance tuning: limit concurrent exports and disable interning
metrics-governor -export-concurrency 32 -string-interning=false

# High-load environment: increase concurrency limit
metrics-governor -export-concurrency 64
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
