# Configuration

metrics-governor supports two configuration methods:
1. **YAML configuration file** (recommended for complex setups)
2. **CLI flags** (for simple setups or quick overrides)

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
```
