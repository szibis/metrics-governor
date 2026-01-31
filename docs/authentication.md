# Authentication

metrics-governor supports bearer token and basic authentication for both OTLP and PRW (Prometheus Remote Write) receivers and exporters.

## OTLP Receiver Authentication

Require authentication for incoming OTLP connections (gRPC and HTTP):

### Bearer Token Authentication

```bash
metrics-governor -receiver-auth-enabled \
    -receiver-auth-bearer-token "your-secret-token"
```

Clients must include the header:
```
Authorization: Bearer your-secret-token
```

### Basic Authentication

```bash
metrics-governor -receiver-auth-enabled \
    -receiver-auth-basic-username "user" \
    -receiver-auth-basic-password "password"
```

Clients must include the header:
```
Authorization: Basic <base64(user:password)>
```

### YAML Configuration

```yaml
receiver:
  auth:
    enabled: true
    bearer_token: "your-secret-token"
    # OR
    basic:
      username: "user"
      password: "password"
```

## OTLP Exporter Authentication

Authenticate when connecting to the OTLP backend:

### Bearer Token Authentication

```bash
metrics-governor -exporter-auth-bearer-token "your-secret-token"
```

### Basic Authentication

```bash
metrics-governor -exporter-auth-basic-username "user" \
    -exporter-auth-basic-password "password"
```

### Custom Headers

Use custom headers for API keys or tenant identification:

```bash
metrics-governor -exporter-auth-headers "X-API-Key=your-api-key,X-Tenant-ID=tenant123"
```

### YAML Configuration

```yaml
exporter:
  auth:
    bearer_token: "your-secret-token"
    # OR
    basic:
      username: "user"
      password: "password"
    # Additional headers
    headers:
      X-API-Key: "your-api-key"
      X-Tenant-ID: "tenant123"
```

## PRW Receiver Authentication

Require authentication for incoming Prometheus Remote Write requests:

### Bearer Token Authentication

```bash
metrics-governor -prw-listen :9090 \
    -prw-receiver-auth-enabled \
    -prw-receiver-auth-bearer-token "your-secret-token"
```

Clients must include the header:
```
Authorization: Bearer your-secret-token
```

### YAML Configuration

```yaml
prw:
  receiver:
    address: ":9090"
    auth:
      enabled: true
      bearer_token: "your-secret-token"
```

## PRW Exporter Authentication

Authenticate when connecting to the PRW backend (VictoriaMetrics, Prometheus, etc.):

### Bearer Token Authentication

```bash
metrics-governor -prw-exporter-endpoint http://victoriametrics:8428/api/v1/write \
    -prw-exporter-auth-bearer-token "your-secret-token"
```

### YAML Configuration

```yaml
prw:
  exporter:
    endpoint: "http://victoriametrics:8428/api/v1/write"
    auth:
      bearer_token: "your-secret-token"
```

## Authentication Options Reference

### OTLP Receiver Authentication Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-receiver-auth-enabled` | `false` | Enable authentication for OTLP receivers |
| `-receiver-auth-bearer-token` | | Expected bearer token |
| `-receiver-auth-basic-username` | | Basic auth username |
| `-receiver-auth-basic-password` | | Basic auth password |

### OTLP Exporter Authentication Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-auth-bearer-token` | | Bearer token to send with requests |
| `-exporter-auth-basic-username` | | Basic auth username |
| `-exporter-auth-basic-password` | | Basic auth password |
| `-exporter-auth-headers` | | Custom headers (format: `key1=value1,key2=value2`) |

### PRW Receiver Authentication Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-receiver-auth-enabled` | `false` | Enable authentication for PRW receiver |
| `-prw-receiver-auth-bearer-token` | | Expected bearer token |

### PRW Exporter Authentication Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-auth-bearer-token` | | Bearer token to send with requests |
