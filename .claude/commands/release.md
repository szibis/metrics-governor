# Release Workflow

Create release version **$ARGUMENTS**.

## Instructions

You are the release agent for metrics-governor. Execute the following release workflow:

### 1. Parse Arguments

Extract version and description from: `$ARGUMENTS`
- Format: `<version> <description>` (e.g., `0.5.3 Add new feature X`)
- Version should be semantic versioning without 'v' prefix (e.g., `0.5.3`)

### 2. Validate Prerequisites

```bash
# Must be on main branch
git branch --show-current

# Check for uncommitted changes
git status --porcelain

# Verify tag doesn't exist
git tag -l "v<VERSION>"
```

### 3. Analyze Test Coverage

Count tests in each category:
- Unit tests: `grep -r "func Test" internal/*/*.go | grep -v benchmark | wc -l`
- Functional tests: `grep -r "func Test" functional/*.go | wc -l`
- E2E tests: `grep -r "func Test" e2e/*.go test/e2e*.go | wc -l`
- Benchmarks: `grep -r "func Benchmark" internal/*/*.go | wc -l`

### 4. Check for Helm Changes

```bash
git diff $(git describe --tags --abbrev=0)..HEAD --name-only | grep "^helm/"
```

If helm/ has changes, update `helm/metrics-governor/Chart.yaml`:
- Set `version: <VERSION>`
- Set `appVersion: "<VERSION>"`

### 5. Update README

Update these in README.md:
- Tests badge: `[![Tests](https://img.shields.io/badge/Tests-<TOTAL>+-success`
- Test coverage table totals row with new counts

### 6. Update CHANGELOG.md

Add new version entry after `## [Unreleased]`:

```markdown
## [<VERSION>] - <YYYY-MM-DD>

### Changed

<DESCRIPTION>

**Test Coverage:**
- Unit Tests: <COUNT>
- Functional Tests: <COUNT>
- E2E Tests: <COUNT>
- Benchmarks: <COUNT>
- Total: <TOTAL>+ tests
```

### 7. Run Tests

```bash
go test ./... -count=1
```

All tests must pass before continuing.

### 8. Commit Changes

Stage and commit all changes:
```bash
git add README.md CHANGELOG.md
# Add helm/metrics-governor/Chart.yaml if helm changed

git commit --no-gpg-sign -m "Release v<VERSION>

<DESCRIPTION>

- Update test coverage: <TOTAL>+ tests
- Update README badges and test table
- Update CHANGELOG"
```

**IMPORTANT**: Do NOT add "Co-Authored-By: Claude" to the commit message.

### 9. Create Tag

```bash
git tag -a "v<VERSION>" -m "Release v<VERSION> - <DESCRIPTION>"
```

### 10. Push to GitHub

Tell the user to touch their hardware security key, then push:

```bash
git push origin main
git push origin "v<VERSION>"
```

Wait for the user's key touch between commands if needed.

### 11. Report Success

After pushing, inform the user:

```
Release v<VERSION> pushed successfully!

GitHub Actions will now:
- Build binaries: darwin-arm64, linux-arm64, linux-amd64
- Package Helm chart: metrics-governor-<VERSION>.tgz
- Build & push Docker images:
  - docker.io/slaskoss/metrics-governor:<VERSION>
  - docker.io/slaskoss/metrics-governor:latest
  - ghcr.io/szibis/metrics-governor:<VERSION>
  - ghcr.io/szibis/metrics-governor:latest

Monitor: https://github.com/szibis/metrics-governor/actions
Release: https://github.com/szibis/metrics-governor/releases/tag/v<VERSION>
```

## Error Handling

- If tests fail: Stop and report which tests failed
- If not on main: Stop and ask user to switch to main
- If tag exists: Stop and report the tag already exists
- If push fails: Remind user to touch hardware key and retry
