# metrics-governor

OTLP metrics proxy with buffering. Receives metrics via gRPC and HTTP, buffers them, and forwards to a configurable OTLP endpoint with batching support.

## Features

- OTLP gRPC receiver (default: `:4317`)
- OTLP HTTP receiver (default: `:4318`)
- Configurable metrics buffering
- Batch export with configurable size
- Graceful shutdown with final flush

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
```

### Docker

```bash
docker run -p 4317:4317 -p 4318:4318 metrics-governor \
  -exporter-endpoint otel-collector:4317
```

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  OTLP Clients   │────▶│ metrics-governor│────▶│  OTLP Backend   │
│  (gRPC/HTTP)    │     │    (buffer)     │     │  (collector)    │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

## License

See [LICENSE](LICENSE) file.
