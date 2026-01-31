# TLS Configuration

metrics-governor supports TLS for both OTLP and PRW (Prometheus Remote Write) receivers and exporters, including mutual TLS (mTLS) for certificate-based authentication.

## OTLP Receiver TLS (Server-side)

Enable TLS for incoming OTLP connections (gRPC and HTTP):

### Basic TLS

```bash
metrics-governor -receiver-tls-enabled \
    -receiver-tls-cert /etc/certs/server.crt \
    -receiver-tls-key /etc/certs/server.key
```

### mTLS (Require Client Certificates)

```bash
metrics-governor -receiver-tls-enabled \
    -receiver-tls-cert /etc/certs/server.crt \
    -receiver-tls-key /etc/certs/server.key \
    -receiver-tls-ca /etc/certs/ca.crt \
    -receiver-tls-client-auth
```

### YAML Configuration

```yaml
receiver:
  tls:
    enabled: true
    cert_file: "/etc/certs/server.crt"
    key_file: "/etc/certs/server.key"
    ca_file: "/etc/certs/ca.crt"      # For mTLS
    client_auth: true                  # Require client certs
```

## OTLP Exporter TLS (Client-side)

Enable TLS for outgoing connections to the OTLP backend:

### Secure Connection with System CA

```bash
metrics-governor -exporter-insecure=false
```

### Custom CA Certificate

```bash
metrics-governor -exporter-insecure=false \
    -exporter-tls-enabled \
    -exporter-tls-ca /etc/certs/ca.crt
```

### mTLS (Client Certificate)

```bash
metrics-governor -exporter-insecure=false \
    -exporter-tls-enabled \
    -exporter-tls-cert /etc/certs/client.crt \
    -exporter-tls-key /etc/certs/client.key \
    -exporter-tls-ca /etc/certs/ca.crt
```

### Skip Certificate Verification

> **Warning**: Not recommended for production use.

```bash
metrics-governor -exporter-insecure=false \
    -exporter-tls-enabled \
    -exporter-tls-skip-verify
```

### Override Server Name

```bash
metrics-governor -exporter-insecure=false \
    -exporter-tls-enabled \
    -exporter-tls-server-name custom-hostname.example.com
```

### YAML Configuration

```yaml
exporter:
  insecure: false
  tls:
    enabled: true
    cert_file: "/etc/certs/client.crt"   # For mTLS
    key_file: "/etc/certs/client.key"    # For mTLS
    ca_file: "/etc/certs/ca.crt"
    skip_verify: false
    server_name: "custom-hostname.example.com"
```

---

## PRW Receiver TLS (Server-side)

Enable TLS for incoming Prometheus Remote Write connections:

### Basic TLS

```bash
metrics-governor -prw-listen :9090 \
    -prw-receiver-tls-enabled \
    -prw-receiver-tls-cert /etc/certs/server.crt \
    -prw-receiver-tls-key /etc/certs/server.key
```

### YAML Configuration

```yaml
prw:
  receiver:
    address: ":9090"
    tls:
      enabled: true
      cert_file: "/etc/certs/server.crt"
      key_file: "/etc/certs/server.key"
```

## PRW Exporter TLS (Client-side)

Enable TLS for outgoing connections to PRW backends (VictoriaMetrics, Prometheus, etc.):

### Custom CA Certificate

```bash
metrics-governor -prw-exporter-endpoint https://victoriametrics:8428/api/v1/write \
    -prw-exporter-tls-enabled \
    -prw-exporter-tls-ca /etc/certs/ca.crt
```

### mTLS (Client Certificate)

```bash
metrics-governor -prw-exporter-endpoint https://victoriametrics:8428/api/v1/write \
    -prw-exporter-tls-enabled \
    -prw-exporter-tls-cert /etc/certs/client.crt \
    -prw-exporter-tls-key /etc/certs/client.key \
    -prw-exporter-tls-ca /etc/certs/ca.crt
```

### YAML Configuration

```yaml
prw:
  exporter:
    endpoint: "https://victoriametrics:8428/api/v1/write"
    tls:
      enabled: true
      cert_file: "/etc/certs/client.crt"
      key_file: "/etc/certs/client.key"
      ca_file: "/etc/certs/ca.crt"
```

---

## TLS Options Reference

### OTLP Receiver TLS Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-receiver-tls-enabled` | `false` | Enable TLS for OTLP receivers |
| `-receiver-tls-cert` | | Path to server certificate file |
| `-receiver-tls-key` | | Path to server private key file |
| `-receiver-tls-ca` | | Path to CA certificate for client verification |
| `-receiver-tls-client-auth` | `false` | Require client certificates (mTLS) |

### OTLP Exporter TLS Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-insecure` | `true` | Use insecure connection |
| `-exporter-tls-enabled` | `false` | Enable custom TLS config |
| `-exporter-tls-cert` | | Path to client certificate file (mTLS) |
| `-exporter-tls-key` | | Path to client private key file (mTLS) |
| `-exporter-tls-ca` | | Path to CA certificate for server verification |
| `-exporter-tls-skip-verify` | `false` | Skip TLS certificate verification |
| `-exporter-tls-server-name` | | Override server name for TLS verification |

### PRW Receiver TLS Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-receiver-tls-enabled` | `false` | Enable TLS for PRW receiver |
| `-prw-receiver-tls-cert` | | Path to server certificate file |
| `-prw-receiver-tls-key` | | Path to server private key file |

### PRW Exporter TLS Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-tls-enabled` | `false` | Enable TLS for PRW exporter |
| `-prw-exporter-tls-cert` | | Path to client certificate file (mTLS) |
| `-prw-exporter-tls-key` | | Path to client private key file (mTLS) |
| `-prw-exporter-tls-ca` | | Path to CA certificate for server verification |
