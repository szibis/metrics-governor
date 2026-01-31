# Compression

metrics-governor supports compression for both OTLP and PRW (Prometheus Remote Write) pipelines. Compression can significantly reduce network bandwidth, especially for high-volume metrics.

## OTLP Compression

### Supported Algorithms

| Algorithm | Content-Encoding | Description |
|-----------|------------------|-------------|
| `gzip` | `gzip` | Standard gzip compression, widely supported |
| `zstd` | `zstd` | Zstandard compression, excellent ratio and speed |
| `snappy` | `snappy` | Fast compression with moderate ratio |
| `zlib` | `zlib` | Zlib compression (similar to gzip) |
| `deflate` | `deflate` | Raw deflate compression |
| `lz4` | `lz4` | Very fast compression with lower ratio |

## Compression Levels

Each algorithm supports different compression levels:

| Algorithm | Levels | Description |
|-----------|--------|-------------|
| **gzip/zlib/deflate** | 1-9, -1 | 1 = fastest, 9 = best compression, -1 = default |
| **zstd** | 1, 3, 6, 11 | 1 = fastest, 3 = default, 6 = better, 11 = best |
| **snappy** | N/A | No compression levels supported |
| **lz4** | N/A | Uses default compression |

## OTLP Exporter Compression

### Enable gzip Compression (Default Level)

```bash
metrics-governor -exporter-protocol http \
    -exporter-compression gzip
```

### Enable gzip with Best Compression

```bash
metrics-governor -exporter-protocol http \
    -exporter-compression gzip \
    -exporter-compression-level 9
```

### Enable zstd with Default Level

```bash
metrics-governor -exporter-protocol http \
    -exporter-compression zstd
```

### Enable zstd with Best Compression

```bash
metrics-governor -exporter-protocol http \
    -exporter-compression zstd \
    -exporter-compression-level 11
```

### Enable snappy for Fast Compression

```bash
metrics-governor -exporter-protocol http \
    -exporter-compression snappy
```

### YAML Configuration

```yaml
exporter:
  protocol: "http"
  compression:
    type: "zstd"
    level: 6
```

## OTLP Receiver Decompression

The HTTP receiver automatically decompresses incoming requests based on the `Content-Encoding` header.

**Supported encodings:**
- `gzip`, `x-gzip`
- `zstd`
- `snappy`, `x-snappy-framed`
- `zlib`
- `deflate`
- `lz4`

No configuration is required - decompression is automatic.

## OTLP Compression Options Reference

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-compression` | `none` | Compression type: `none`, `gzip`, `zstd`, `snappy`, `zlib`, `deflate`, `lz4` |
| `-exporter-compression-level` | `0` | Compression level (algorithm-specific, 0 for default) |

## OTLP Performance Considerations

| Algorithm | Speed | Compression Ratio | Use Case |
|-----------|-------|-------------------|----------|
| **lz4** | Fastest | Lowest | Low latency requirements |
| **snappy** | Very Fast | Low-Medium | Balanced speed/ratio |
| **gzip** | Medium | Good | Compatibility |
| **zstd** | Fast | Excellent | Best overall choice |

**Recommendation**: Use `zstd` for the best balance of compression ratio and speed. Use `lz4` or `snappy` when latency is critical.

---

## PRW Compression

Prometheus Remote Write uses different compression requirements than OTLP.

### Supported Algorithms

| Algorithm | PRW 1.0 | PRW 2.0 | Description |
|-----------|---------|---------|-------------|
| **snappy** | Required | Required | Default compression for PRW protocol |
| **zstd** | N/A | Optional | Better compression ratio (PRW 2.0 only) |

### PRW Receiver Decompression

The PRW receiver automatically decompresses incoming requests based on the `Content-Encoding` header:

- `snappy` - Required for all PRW versions
- `zstd` - Supported for PRW 2.0 clients

No configuration is required - decompression is automatic.

### PRW Exporter Compression

#### Snappy Compression (Default)

```bash
metrics-governor -prw-listen :9090 \
    -prw-exporter-endpoint http://victoriametrics:8428/api/v1/write
```

#### Zstd Compression (VictoriaMetrics Mode)

```bash
metrics-governor -prw-listen :9090 \
    -prw-exporter-endpoint http://victoriametrics:8428/api/v1/write \
    -prw-exporter-vm-mode \
    -prw-exporter-vm-compression zstd
```

### YAML Configuration

```yaml
prw:
  exporter:
    endpoint: "http://victoriametrics:8428/api/v1/write"
    victoriametrics:
      enabled: true
      compression: "zstd"  # "snappy" or "zstd"
```

### PRW Compression Options Reference

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-vm-compression` | `snappy` | Compression: `snappy` or `zstd` (requires VM mode) |

### PRW Performance Considerations

| Algorithm | PRW Version | Speed | Compression Ratio | Use Case |
|-----------|-------------|-------|-------------------|----------|
| **snappy** | 1.0 & 2.0 | Very Fast | Moderate | Standard PRW, maximum compatibility |
| **zstd** | 2.0 only | Fast | Excellent | VictoriaMetrics, better bandwidth savings |

**Recommendation**: Use `snappy` for maximum compatibility with all PRW backends. Use `zstd` with VictoriaMetrics mode for better compression when bandwidth is limited.
