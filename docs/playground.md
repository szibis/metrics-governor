# Playground

The Playground is a browser-based tool that helps operators plan and configure metrics-governor deployments. It provides resource estimation, Kubernetes pod sizing, cloud storage recommendations, and live YAML generation.

## Quick Start

```bash
# Open directly in your browser — no build step required
open tools/playground/index.html

# Or from repo root:
open index.html
```

## How It Works

### Input-Driven Estimation

Enter your expected throughput (datapoints/sec), metric count, cardinality, and cluster resources. The tool calculates:

| Estimate | Formula |
|----------|---------|
| CPU cores | `dps / 100,000` (configurable via `dps_per_core`) |
| Memory | Tracker memory + buffer memory + 50 MB base overhead + queue (if memory mode) |
| Disk (PVC) | `queue_max_size * 1.2` (20% headroom) |
| Write IOPS | `ceil(dps / batch_size)` |
| Read IOPS | `write_IOPS * 0.1` |
| Write throughput | `(dps / batch_size) * batch_bytes * compression_ratio` |

### Kubernetes Pod Sizing

The tool recommends:

- **Pod count**: `ceil(dps / (cores_per_pod * 100K * 0.7))` — 70% CPU target utilization
- **Per-pod CPU request**: actual expected usage in millicores
- **Per-pod CPU limit**: request * 1.5 (burst headroom for GC and spikes)
- **Per-pod memory request**: `ceil(total_memory / pods)`
- **Per-pod memory limit**: request * 1.3 (Go GC headroom)
- **Per-pod disk**: PVC size including 20% headroom (disk queue only)
- **Total cluster resources**: aggregated across all pods

### Cloud Storage Recommendations

When using a disk-based queue, the tool recommends storage classes for AWS, Azure, and GCP. Recommendations are **data-driven** — each disk type has parameters for:

- Baseline and per-GiB IOPS scaling
- Baseline, per-GiB, per-TiB, or per-IOPS throughput scaling
- Maximum IOPS and throughput caps
- Burst model (`provisioned`, `credit`, or `none`)

Performance is calculated at the actual PVC volume size, so recommendations reflect what the disk will deliver for your specific workload.

### Bidirectional YAML Editing

Three YAML tabs (Helm values, app config, limits rules) support two-way editing:

- Change an input → YAML updates immediately
- Edit YAML directly → inputs and estimates update on blur

## Architecture: Single Source of Truth

### The Problem

The playground previously had hardcoded defaults that drifted from the Go app:

| Field | Go Default | Old HTML Default |
|-------|-----------|-----------------|
| `BufferSize` | 10,000 | 5,000 |
| `QueueMaxBytes` | 1 GB | 100 MB |
| `QueueType` | `memory` | `disk` |

### The Solution

The HTML embeds a `<script type="application/json" id="config-meta">` block with three sections:

```
config-meta JSON
├── defaults     ← Go DefaultConfig() values
├── estimation   ← Resource estimation constants
└── storage      ← Cloud storage disk type definitions
```

On page load, `init()` reads this JSON and applies it:

```javascript
const meta = JSON.parse(document.getElementById('config-meta').textContent);
this._meta = meta;
this._est = meta.estimation;       // Used by all estimation getters
this._storage = meta.storage;       // Used by storageOptions getter
// Apply Go-sourced defaults before localStorage restore
this.bufferMaxSize = meta.defaults.buffer_size;
this.queueType = meta.defaults.queue_type;
// ...
```

### CI Validation

A Go test (`tools/playground/validate_test.go`) extracts the JSON from `index.html` and asserts every default matches `config.DefaultConfig()`:

```bash
# Run locally
make validate-playground

# Output on drift:
# --- FAIL: TestConfigMetaMatchesGoDefaults
#     buffer_size: HTML config-meta has 5000, Go DefaultConfig() has 10000
```

This runs automatically in CI as the `validate-playground` job.

## Estimation Constants

All magic numbers are defined in the `estimation` section of config-meta:

| Constant | Value | Description |
|----------|-------|-------------|
| `dps_per_core` | 100,000 | Datapoints per second per CPU core |
| `compression_ratio` | 0.3 | Queue disk compression ratio (30%) |
| `base_overhead_bytes` | 50 MB | Go runtime + base process overhead |
| `pvc_headroom_factor` | 1.2 | PVC size = queue size * 1.2 |
| `cpu_target_utilization` | 0.7 | Target 70% CPU utilization per pod |
| `read_iops_ratio` | 0.1 | Read IOPS = 10% of write IOPS |
| `cpu_limit_factor` | 1.5 | CPU limit = request * 1.5 |
| `mem_limit_factor` | 1.3 | Memory limit = request * 1.3 |
| `hll_memory_bytes` | 12,288 | HyperLogLog tracker memory (~12 KB) |
| `exact_mode_bytes_per_series` | 75 | Exact mode memory per tracked series |
| `label_key_overhead_bytes` | 10 | Bytes per label key overhead |
| `series_base_overhead_bytes` | 16 | Base overhead per series entry |

## Storage Disk Type Definitions

Each entry in `storage.<provider>[]` defines a disk type:

```json
{
  "name": "gp3",
  "class": "gp3",
  "disk_type": "gp3",
  "min_gib": 1,
  "max_gib": 16384,
  "baseline_iops": 3000,
  "iops_per_gib": 0,
  "max_iops": 80000,
  "baseline_tput_mbs": 125,
  "tput_per_gib_mbs": 0,
  "max_tput_mbs": 2000,
  "burst_model": "provisioned"
}
```

| Field | Description |
|-------|-------------|
| `name` | Display name in the UI |
| `class` | Kubernetes StorageClass name |
| `disk_type` | Cloud-specific disk type parameter |
| `min_gib` / `max_gib` | Volume size range |
| `baseline_iops` | IOPS at minimum size |
| `iops_per_gib` | Additional IOPS per GiB |
| `max_iops` | IOPS cap |
| `baseline_tput_mbs` | Throughput at minimum size (MB/s) |
| `tput_per_gib_mbs` | Additional throughput per GiB |
| `tput_per_tib_mbs` | Additional throughput per TiB (e.g., st1) |
| `tput_per_iops_mbs` | Throughput derived from IOPS (e.g., Azure Ultra) |
| `max_tput_mbs` | Throughput cap |
| `burst_iops` | Credit-based burst IOPS (Azure Premium) |
| `burst_tput_mbs` | Credit-based burst throughput |
| `burst_tput_per_tib_mbs` | Volume-scaled burst throughput (AWS st1) |
| `burst_model` | `provisioned`, `credit`, or `none` |

## Maintaining the Playground

All maintenance flows through `make generate-config-meta`, which reads Go defaults and
`storage-specs.json`, then injects the assembled config-meta JSON into both HTML files.

### When changing Go defaults

1. Edit `internal/config/config.go` (the `DefaultConfig()` function)
2. Run `make generate-config-meta` — the HTML is updated automatically
3. Run `make validate-playground` to double-check
4. Commit the updated HTML files

CI will fail if you forget step 2: the `validate-playground` job regenerates and checks
for uncommitted diffs.

### When updating cloud storage disk specs

1. Edit `tools/playground/storage-specs.json` — this is a clean JSON file with one array per cloud provider
2. Run `make generate-config-meta`
3. Commit the updated files

Example — adding a new AWS disk type:

```json
// tools/playground/storage-specs.json
{
  "aws": [
    ... existing entries ...,
    {
      "name": "gp3-new",
      "class": "gp3-new",
      "disk_type": "gp3",
      "min_gib": 1,
      "max_gib": 16384,
      "baseline_iops": 5000,
      "iops_per_gib": 0,
      "max_iops": 100000,
      "baseline_tput_mbs": 200,
      "max_tput_mbs": 3000,
      "burst_model": "provisioned"
    }
  ]
}
```

Then: `make generate-config-meta && make validate-playground`

The UI auto-populates — no JavaScript changes needed.

### When adding a new estimation constant

1. Add the value to the `estimation` map in `tools/playground/cmd/generate/main.go`
2. Reference it as `this._est.your_constant` in the HTML JavaScript
3. Run `make generate-config-meta`

## Files

| File | Description |
|------|-------------|
| `tools/playground/index.html` | Main source file (generated config-meta block) |
| `tools/playground/storage-specs.json` | Cloud storage disk type definitions (edit this) |
| `tools/playground/cmd/generate/main.go` | Generator: Go defaults + storage specs → HTML |
| `tools/playground/validate_test.go` | Go test: JSON defaults == `DefaultConfig()` |
| `tools/playground/README.md` | Quick-start README |
| `index.html` | Root copy (auto-updated by generator) |
