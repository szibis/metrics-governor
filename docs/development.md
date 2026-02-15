# Development

## Building

```bash
# Build for current platform
make build

# Build all platforms (darwin-arm64, linux-arm64, linux-amd64)
make all

# Build Docker image
make docker
```

Binaries are output to `bin/` directory.

### Profile-Guided Optimization (PGO)

PGO uses CPU profiles from benchmarks to guide the compiler's optimization decisions, yielding 2-7% throughput improvement:

```bash
# Step 1: Generate PGO profile from representative benchmarks
make pgo-profile
# Creates default.pgo in the project root (~5-10 MB)

# Step 2: Build with PGO
make pgo-build
# Output: bin/metrics-governor (PGO-optimized)

# Docker: PGO is used automatically when default.pgo exists in the build context
docker build -t metrics-governor:latest .
```

The `default.pgo` file should be regenerated periodically (e.g., after significant code changes) and committed to the repository so CI builds also benefit.

### Native Compression (Optional)

For deployments where gzip/zlib/deflate compression is a CPU bottleneck, an optional Rust FFI backend provides ~1.6x faster compression through native C libraries (flate2 with zlib-ng). This is build-tag gated — the default `CGO_ENABLED=0` build uses pure Go automatically.

**Requirements:** Rust toolchain (1.85+), C compiler, CGO enabled.

```bash
# Build the Rust FFI library
make rust-build

# Build Go with native compression
make build-native

# Run tests with native compression
make test-native

# Docker image with native compression
make docker-native
```

Zstd and snappy always use pure Go regardless of the build tag (klauspost/compress and S2 already outperform their native counterparts). See [compression.md](compression.md#native-compression-optional-build-tag-gated) for details.

## Running Tests

```bash
# Run all tests
make test

# Run tests with verbose output
make test-verbose

# Run tests with coverage report
make test-coverage
```

Coverage report is generated at `bin/coverage.html`.

### Test Commands

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run benchmarks
go test -bench=. -benchmem ./...

# Run functional tests only
go test ./functional/...

# Run e2e tests (requires Docker)
go test ./e2e/...

# Run specific test
go test -v -run TestAdaptiveLimiting ./internal/limits/...
```

## Project Structure

```
metrics-governor/
├── cmd/metrics-governor/    # Main application entry point
├── internal/
│   ├── auth/                # Authentication (bearer token, basic auth)
│   ├── buffer/              # OTLP metrics buffering and batching
│   ├── compression/         # Compression support (gzip, zstd, etc.)
│   ├── config/              # Configuration management
│   ├── exporter/            # OTLP and PRW exporters
│   ├── limits/              # Limits enforcement (adaptive, drop, log)
│   ├── logging/             # JSON structured logging
│   ├── prw/                 # Prometheus Remote Write support (buffer, queue, types)
│   ├── queue/               # FastQueue persistent queue for OTLP retries
│   ├── receiver/            # OTLP (gRPC/HTTP) and PRW receivers
│   ├── sharding/            # Consistent hashing and DNS discovery
│   ├── stats/               # Statistics collection (OTLP and PRW metrics)
│   └── tls/                 # TLS configuration utilities
├── functional/              # Functional tests
├── e2e/                     # End-to-end tests
├── helm/metrics-governor/   # Helm chart for Kubernetes
├── dashboards/              # Grafana dashboards
├── examples/                # Example configuration files
├── docs/                    # Documentation
├── test/                    # Integration test environment
├── bin/                     # Build output directory
├── compose_overrides/          # Docker Compose override files
├── rust/compress-ffi/          # Optional Rust FFI for native compression
├── Dockerfile
├── Dockerfile.native           # Multi-stage build with Rust FFI
├── docker-compose.yaml
└── Makefile
```

## Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting
- Run `go vet` before committing
- Keep functions focused and small
- Write tests for new functionality

## Adding New Features

1. Create feature branch from `main`
2. Implement feature with tests
3. Ensure all tests pass: `make test`
4. Update documentation if needed
5. Create pull request

## Pull Request Labels

Pull requests are automatically labeled based on their content. Labels are applied when PRs are opened, edited, or updated.

### Type Labels (Conventional Commits)

Based on PR title prefix:

| Prefix | Label | Color | Description |
|--------|-------|-------|-------------|
| `fix:` | `bug` | ![#d73a4a](https://placehold.co/15x15/d73a4a/d73a4a) `#d73a4a` | Bug fixes |
| `feat:` | `enhancement` | ![#a2eeef](https://placehold.co/15x15/a2eeef/a2eeef) `#a2eeef` | New features |
| `docs:` | `documentation` | ![#0075ca](https://placehold.co/15x15/0075ca/0075ca) `#0075ca` | Documentation changes |
| `perf:` | `performance` | ![#f9d0c4](https://placehold.co/15x15/f9d0c4/f9d0c4) `#f9d0c4` | Performance improvements |
| `refactor:` | `refactor` | ![#c5def5](https://placehold.co/15x15/c5def5/c5def5) `#c5def5` | Code refactoring |
| `test:` | `testing` | ![#bfd4f2](https://placehold.co/15x15/bfd4f2/bfd4f2) `#bfd4f2` | Test changes |
| `ci:` | `ci` | ![#e6e6e6](https://placehold.co/15x15/e6e6e6/e6e6e6) `#e6e6e6` | CI/CD changes |
| `chore:` | `chore` | ![#fef2c0](https://placehold.co/15x15/fef2c0/fef2c0) `#fef2c0` | Maintenance tasks |
| `build:` | `build` | ![#d4c5f9](https://placehold.co/15x15/d4c5f9/d4c5f9) `#d4c5f9` | Build system changes |
| `release:` | `release` | ![#5319e7](https://placehold.co/15x15/5319e7/5319e7) `#5319e7` | Release related |
| `security:` | `security` | ![#d93f0b](https://placehold.co/15x15/d93f0b/d93f0b) `#d93f0b` | Security fixes |
| `deps:` | `dependencies` | ![#0366d6](https://placehold.co/15x15/0366d6/0366d6) `#0366d6` | Dependency updates |

### Component Labels

Based on changed file paths:

| Path Pattern | Label | Color |
|--------------|-------|-------|
| `internal/buffer/` | `component/buffer` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/queue/` | `component/queue` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/exporter/` | `component/exporter` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/receiver/` | `component/receiver` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/config/` | `component/config` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/compression/` | `component/compression` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/auth/` | `component/auth` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/limits/` | `component/limits` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/stats/` | `component/stats` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/sharding/` | `component/sharding` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/prw/` | `component/prw` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `internal/tls/` | `component/tls` | ![#1d76db](https://placehold.co/15x15/1d76db/1d76db) `#1d76db` |
| `helm/` | `helm` | ![#0052cc](https://placehold.co/15x15/0052cc/0052cc) `#0052cc` |
| `.github/` | `ci` | ![#e6e6e6](https://placehold.co/15x15/e6e6e6/e6e6e6) `#e6e6e6` |
| `*.md`, `docs/` | `documentation` | ![#0075ca](https://placehold.co/15x15/0075ca/0075ca) `#0075ca` |

### Size Labels

Based on total lines changed (additions + deletions):

| Lines Changed | Label | Color | Description |
|---------------|-------|-------|-------------|
| ≤ 10 | `size/XS` | ![#3cba54](https://placehold.co/15x15/3cba54/3cba54) `#3cba54` | Extra small |
| ≤ 50 | `size/S` | ![#77c043](https://placehold.co/15x15/77c043/77c043) `#77c043` | Small |
| ≤ 200 | `size/M` | ![#f9a825](https://placehold.co/15x15/f9a825/f9a825) `#f9a825` | Medium |
| ≤ 500 | `size/L` | ![#e65100](https://placehold.co/15x15/e65100/e65100) `#e65100` | Large |
| > 500 | `size/XL` | ![#d93f0b](https://placehold.co/15x15/d93f0b/d93f0b) `#d93f0b` | Extra large |

### Special Labels

| Trigger | Label | Color | Description |
|---------|-------|-------|-------------|
| "breaking" in title/body | `breaking-change` | ![#b60205](https://placehold.co/15x15/b60205/b60205) `#b60205` | Breaking changes |
| Draft PR or "wip" in title | `work-in-progress` | ![#fbca04](https://placehold.co/15x15/fbca04/fbca04) `#fbca04` | Work in progress |
| `*_test.go` files changed | `testing` | ![#bfd4f2](https://placehold.co/15x15/bfd4f2/bfd4f2) `#bfd4f2` | Test changes |
| `*benchmark*` files changed | `performance` | ![#f9d0c4](https://placehold.co/15x15/f9d0c4/f9d0c4) `#f9d0c4` | Benchmark changes |
| `go.mod`/`go.sum` changed | `dependencies` | ![#0366d6](https://placehold.co/15x15/0366d6/0366d6) `#0366d6` | Dependency updates |
| "help wanted" in body | `help wanted` | ![#008672](https://placehold.co/15x15/008672/008672) `#008672` | Extra attention needed |
| "good first issue" in body | `good first issue` | ![#7057ff](https://placehold.co/15x15/7057ff/7057ff) `#7057ff` | Good for newcomers |

## Running Before/After Benchmarks

To measure the performance impact of a code change, run benchmarks before and after and compare with `benchstat`:

1. Checkout baseline: `git stash`
2. Run: `go test -bench=. -benchmem -count=5 ./... > old.txt`
3. Apply changes: `git stash pop`
4. Run: `go test -bench=. -benchmem -count=5 ./... > new.txt`
5. Compare: `benchstat old.txt new.txt`

Install `benchstat` if you do not have it:

```bash
go install golang.org/x/perf/cmd/benchstat@latest
```

The `-count=5` flag runs each benchmark five times so `benchstat` can compute statistical significance. Look for the `delta` column to see percentage changes and the `p` value to confirm results are statistically meaningful.

## Debugging

### Enable Verbose Logging

All logs are JSON formatted. Use `jq` for parsing:

```bash
metrics-governor 2>&1 | jq .
```

### Profile CPU/Memory

```bash
go test -cpuprofile=cpu.prof -memprofile=mem.prof -bench=. ./...
go tool pprof cpu.prof
```

### Debug with Delve

```bash
dlv debug ./cmd/metrics-governor -- -config config.yaml
```

## Release Process

Releases are automated via GitHub Actions. Use the release script to create a release:

```bash
./scripts/release.sh <version> -m "Release message"

# Examples:
./scripts/release.sh 0.5.5 -m "Add new feature X"
./scripts/release.sh 1.0.0 -m "Major release"
./scripts/release.sh 0.5.6 -m "Bug fixes" --dry-run  # Preview changes
```

The script will:
1. Verify prerequisites (main branch, clean working dir, tag doesn't exist)
2. Count and update test coverage in README
3. Update CHANGELOG with version entry
4. Update Helm chart version (if helm/ has changes)
5. Run tests to verify
6. Commit changes and create git tag
7. Optionally push to GitHub (prompts for confirmation)

After pushing, GitHub Actions will:
- Build binaries: darwin-arm64, linux-arm64, linux-amd64
- Package Helm chart
- Build and push Docker images to Docker Hub and GHCR
