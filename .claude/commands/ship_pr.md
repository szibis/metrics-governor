# Ship PR Workflow

Create a Pull Request with **$ARGUMENTS**.

## Usage

```bash
# Simple conventional commit
/ship_pr fix: resolve data race in buffer

# With scope
/ship_pr feat(prw): add sharding support

# Draft PR
/ship_pr feat: new feature --draft

# Full options
/ship_pr --title "fix: bug" --body "Description"
```

## Conventional Commit Types

| Type | Description | Label |
|------|-------------|-------|
| `feat:` | New feature | `enhancement` |
| `fix:` | Bug fix | `bug` |
| `docs:` | Documentation | `documentation` |
| `perf:` | Performance | `performance` |
| `refactor:` | Refactoring | `refactor` |
| `test:` | Tests | `testing` |
| `ci:` | CI/CD | `ci` |
| `chore:` | Maintenance | `chore` |
| `build:` | Build system | `build` |

## What It Does

1. **Parses arguments** - Extracts type, scope, and description
2. **Creates branch** - Auto-generates branch name from title
3. **Detects components** - Labels based on changed files
4. **Calculates size** - Adds size label based on changes
5. **Commits changes** - If there are uncommitted changes
6. **Pushes branch** - To origin
7. **Creates PR** - With all labels and structured body

## Automatic Labels

### Type Labels
Based on conventional commit prefix (feat, fix, docs, etc.)

### Component Labels
Based on changed file paths:
- `internal/buffer/` -> `component/buffer`
- `internal/prw/` -> `component/prw`
- `helm/` -> `helm`
- etc.

### Size Labels
Based on total lines changed:
- ≤10: `size/XS`
- ≤50: `size/S`
- ≤200: `size/M`
- ≤500: `size/L`
- >500: `size/XL`

## Examples

```bash
# Bug fix in buffer component
/ship_pr fix: resolve memory leak in buffer flush
# -> branch: fix/resolve-memory-leak-in-buffer-flush
# -> labels: bug, component/buffer, size/S

# New feature with scope
/ship_pr feat(receiver): add gRPC compression support
# -> branch: feature/add-grpc-compression-support
# -> labels: enhancement, component/receiver, size/M

# Documentation update
/ship_pr docs: add sharding configuration guide
# -> branch: docs/add-sharding-configuration-guide
# -> labels: documentation, size/S
```
