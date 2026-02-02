# Compression

metrics-governor supports compression for HTTP exporters and automatic decompression for HTTP receivers. Compression can significantly reduce network bandwidth, especially for high-volume metrics.

> **Dual Pipeline Support**: Compression works for both OTLP and PRW pipelines. OTLP supports multiple algorithms (gzip, zstd, snappy, etc.), while PRW uses snappy (required) or zstd (PRW 2.0 optional).

## Compression Pipeline

```mermaid
flowchart LR
    subgraph Receiver["HTTP Receiver"]
        RX[Receive<br/>Request]
        Detect{Content-<br/>Encoding?}
        Decomp[Decompress]
        Parse[Parse<br/>Protobuf]
    end

    subgraph Processing["Processing"]
        Buffer[Buffer &<br/>Batch]
        Limits[Apply<br/>Limits]
    end

    subgraph Exporter["HTTP Exporter"]
        Marshal[Marshal<br/>Protobuf]
        Compress[Compress]
        Send[Send with<br/>Content-Encoding]
    end

    RX --> Detect
    Detect -->|gzip/zstd/snappy| Decomp --> Parse
    Detect -->|none| Parse
    Parse --> Buffer --> Limits
    Limits --> Marshal --> Compress --> Send

    style Decomp fill:#9cf,stroke:#333
    style Compress fill:#9cf,stroke:#333
```

## Compression Ratio Comparison

```mermaid
xychart-beta
    title "Compression Performance (lower is better)"
    x-axis [lz4, snappy, gzip-1, gzip-6, gzip-9, zstd-1, zstd-6, zstd-11]
    y-axis "Relative Size %" 0 --> 100
    bar [85, 75, 45, 35, 30, 40, 28, 22]
```

## Supported Algorithms

| Algorithm | Content-Encoding | OTLP | PRW | Description |
|-----------|------------------|------|-----|-------------|
| `gzip` | `gzip` | Yes | No | Standard gzip compression, widely supported |
| `zstd` | `zstd` | Yes | Yes (2.0) | Zstandard compression, excellent ratio and speed |
| `snappy` | `snappy` | Yes | Yes | Fast compression with moderate ratio (PRW default) |
| `zlib` | `zlib` | Yes | No | Zlib compression (similar to gzip) |
| `deflate` | `deflate` | Yes | No | Raw deflate compression |
| `lz4` | `lz4` | Yes | No | Very fast compression with lower ratio |

## Compression Levels

Each algorithm supports different compression levels:

| Algorithm | Levels | Description |
|-----------|--------|-------------|
| **gzip/zlib/deflate** | 1-9, -1 | 1 = fastest, 9 = best compression, -1 = default |
| **zstd** | 1, 3, 6, 11 | 1 = fastest, 3 = default, 6 = better, 11 = best |
| **snappy** | N/A | No compression levels supported |
| **lz4** | N/A | Uses default compression |

## Exporter Compression

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

## Receiver Decompression

The HTTP receiver automatically decompresses incoming requests based on the `Content-Encoding` header.

**Supported encodings:**
- `gzip`, `x-gzip`
- `zstd`
- `snappy`, `x-snappy-framed`
- `zlib`
- `deflate`
- `lz4`

No configuration is required - decompression is automatic.

## Compression Options Reference

### OTLP Compression Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-exporter-compression` | `none` | Compression type: `none`, `gzip`, `zstd`, `snappy`, `zlib`, `deflate`, `lz4` |
| `-exporter-compression-level` | `0` | Compression level (algorithm-specific, 0 for default) |

### PRW Compression Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-prw-exporter-vm-compression` | `snappy` | Compression: `snappy` or `zstd` (VictoriaMetrics mode) |

## Performance Considerations

| Algorithm | Speed | Compression Ratio | Use Case |
|-----------|-------|-------------------|----------|
| **lz4** | Fastest | Lowest | Low latency requirements |
| **snappy** | Very Fast | Low-Medium | Balanced speed/ratio, PRW default |
| **gzip** | Medium | Good | Compatibility |
| **zstd** | Fast | Excellent | Best overall choice |

**Recommendation**: Use `zstd` for the best balance of compression ratio and speed. Use `lz4` or `snappy` when latency is critical.
