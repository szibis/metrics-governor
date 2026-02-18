---
title: "HTTP Settings"
sidebar_position: 2
description: "HTTP client and server tuning"
---

## HTTP Client Settings (Exporter)

Configure the HTTP client connection pool for optimal performance.

### Connection Pool Configuration

```bash
# Increase connection pool for high-throughput scenarios
metrics-governor -exporter-protocol http \
    -exporter-max-idle-conns 200 \
    -exporter-max-idle-conns-per-host 100 \
    -exporter-max-conns-per-host 100 \
    -exporter-idle-conn-timeout 2m

# Disable keep-alives (new connection for each request)
metrics-governor -exporter-protocol http \
    -exporter-disable-keep-alives

# Enable HTTP/2 with health checks
metrics-governor -exporter-protocol http \
    -exporter-force-http2 \
    -exporter-http2-read-idle-timeout 30s \
    -exporter-http2-ping-timeout 10s
```

### Connection Pool Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `max-idle-conns` | 100 | Maximum idle connections across all hosts |
| `max-idle-conns-per-host` | 100 | Maximum idle connections per host |
| `max-conns-per-host` | 0 | Maximum total connections per host (0 = unlimited) |
| `idle-conn-timeout` | 90s | Time before idle connections are closed |
| `disable-keep-alives` | false | Force new connection per request |

### HTTP/2 Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `force-http2` | false | Enable HTTP/2 for non-TLS connections |
| `http2-read-idle-timeout` | 0 | Idle time before sending health check ping |
| `http2-ping-timeout` | 0 | Timeout waiting for ping response |

### YAML Configuration

```yaml
exporter:
  protocol: "http"
  http:
    max_idle_conns: 200
    max_idle_conns_per_host: 100
    max_conns_per_host: 100
    idle_conn_timeout: 2m
    disable_keep_alives: false
    force_http2: true
    http2_read_idle_timeout: 30s
    http2_ping_timeout: 10s
```

## HTTP Server Settings (Receiver)

Configure the HTTP receiver server timeouts for security and resource management.

### Server Timeout Configuration

```bash
# Configure server timeouts
metrics-governor -receiver-read-timeout 30s \
    -receiver-read-header-timeout 10s \
    -receiver-write-timeout 1m \
    -receiver-idle-timeout 2m

# Limit request body size (10MB)
metrics-governor -receiver-max-request-body-size 10485760

# Disable keep-alives for the server
metrics-governor -receiver-keep-alives-enabled=false
```

### Server Timeout Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `read-timeout` | 0 | Max time to read entire request (0 = no limit) |
| `read-header-timeout` | 1m | Max time to read request headers |
| `write-timeout` | 30s | Max time to write response |
| `idle-timeout` | 1m | Max time to wait for next request (keep-alive) |
| `max-request-body-size` | 0 | Max request body size in bytes (0 = no limit) |
| `keep-alives-enabled` | true | Enable HTTP keep-alives |

### YAML Configuration

```yaml
receiver:
  http:
    server:
      max_request_body_size: 10485760
      read_timeout: 30s
      read_header_timeout: 10s
      write_timeout: 1m
      idle_timeout: 2m
      keep_alives_enabled: true
```

## CLI Flags Reference

### Exporter HTTP Client Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-max-idle-conns` | `100` | Maximum idle connections across all hosts |
| `-exporter-max-idle-conns-per-host` | `100` | Maximum idle connections per host |
| `-exporter-max-conns-per-host` | `0` | Maximum total connections per host (0 = no limit) |
| `-exporter-idle-conn-timeout` | `90s` | Idle connection timeout |
| `-exporter-disable-keep-alives` | `false` | Disable HTTP keep-alives |
| `-exporter-force-http2` | `false` | Force HTTP/2 for non-TLS connections |
| `-exporter-http2-read-idle-timeout` | `0` | HTTP/2 read idle timeout for health checks |
| `-exporter-http2-ping-timeout` | `0` | HTTP/2 ping timeout |

### Receiver HTTP Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-receiver-max-request-body-size` | `0` | Maximum request body size in bytes (0 = no limit) |
| `-receiver-read-timeout` | `0` | HTTP server read timeout (0 = no timeout) |
| `-receiver-read-header-timeout` | `1m` | HTTP server read header timeout |
| `-receiver-write-timeout` | `30s` | HTTP server write timeout |
| `-receiver-idle-timeout` | `1m` | HTTP server idle timeout |
| `-receiver-keep-alives-enabled` | `true` | Enable HTTP keep-alives for receiver |
