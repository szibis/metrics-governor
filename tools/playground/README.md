# metrics-governor Playground

An interactive single-page web app that helps operators plan their metrics-governor deployment.

## Usage

Open `index.html` directly in your browser - no build step, no server required:

```bash
open tools/playground/index.html
# or from repo root:
open index.html
```

## Features

- **Resource Estimation** - Given throughput inputs (datapoints/sec, metric count, cardinality), estimates CPU, memory, disk I/O, and queue sizing
- **K8s Pod Sizing** - Recommends replica count and per-pod resource requests/limits (including per-pod disk)
- **Cloud Storage Recommendations** - Data-driven storage class recommendations for AWS, Azure, and GCP with size-dependent IOPS/throughput calculations
- **Limits Rules Builder** - Interactive form to build limits rules with match patterns, actions, and group-by labels
- **Live Config Preview** - Three tabs with real-time YAML generation for Helm values, app config, and limits rules
- **Bidirectional YAML Editing** - Edit YAML directly and changes flow back to inputs (and vice versa)
- **Copy & Download** - One-click copy to clipboard or download as file
- **Dark Mode** - Automatically follows your system preference
- **LocalStorage Persistence** - All inputs auto-save and restore across sessions

## Architecture

### Single Source of Truth: `config-meta` JSON

The HTML embeds a `<script type="application/json" id="config-meta">` block containing:

| Section | Purpose |
|---------|---------|
| `defaults` | Go `DefaultConfig()` values (buffer size, queue type, queue max bytes, etc.) |
| `estimation` | Resource estimation constants (dps/core, compression ratio, overhead, headroom factors) |
| `storage` | Per-cloud-provider disk type definitions with IOPS/throughput scaling parameters |

On page load, `init()` reads this JSON and uses it to:
1. Set initial form values from Go defaults (before localStorage overrides)
2. Store estimation constants in `this._est` for all calculations
3. Store storage definitions in `this._storage` for the recommendation engine

### Drift Prevention

A Go test (`validate_test.go`) extracts the config-meta JSON from `index.html` and validates
every default against `config.DefaultConfig()`. This runs in CI via the `validate-playground`
job, catching any drift between the HTML and Go app.

```bash
# Run locally
make validate-playground

# Or directly
go test -v ./tools/playground/...
```

### Data-Driven Storage Engine

Storage recommendations use a generic `calcDiskPerformance(disk, sizeGiB)` function that
computes effective IOPS and throughput from per-disk-type parameters:

- **Baseline** + **per-GiB scaling** (capped at max)
- **Per-TiB** or **per-IOPS** throughput scaling
- **Burst** models: `provisioned`, `credit` (with burst IOPS/throughput), or `none`

All disk type definitions live in the config-meta JSON — no hardcoded cloud-specific logic.

## Files

```
tools/playground/
├── index.html           ← Single-file app with embedded config-meta JSON
├── storage-specs.json   ← Cloud storage disk type definitions (edit this!)
├── cmd/generate/main.go ← Generator: Go defaults + storage specs → HTML
├── validate_test.go     ← Go test: JSON defaults == Go DefaultConfig()
└── README.md            ← This file

index.html               ← Root copy (auto-updated by generator)
```

## Tech Stack

- [Alpine.js](https://alpinejs.dev/) (~14KB) - reactive data binding
- [Pico CSS](https://picocss.com/) (~10KB) - classless semantic styling
- Zero build tooling, single HTML file

## Maintaining

All updates flow through the generator:

```bash
make generate-config-meta   # Regenerate config-meta JSON in both HTML files
make validate-playground  # Verify defaults match Go
```

**When changing Go defaults** — edit `internal/config/config.go`, then `make generate-config-meta`.

**When updating cloud storage specs** — edit `storage-specs.json`, then `make generate-config-meta`.

**When adding a new estimation constant** — add to `cmd/generate/main.go` estimation map, reference as `this._est.your_constant` in JS, then `make generate-config-meta`.

CI checks that the generated output is up to date — it will fail if you forget to regenerate.
