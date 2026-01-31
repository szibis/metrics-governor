---
name: ship
description: Ship a new release via PR with automatic changelog, test coverage updates, and GitHub release
disable-model-invocation: true
argument-hint: <version> <description>
allowed-tools:
  - Bash(go test:*)
  - Bash(git add:*)
  - Bash(git commit:*)
  - Bash(git push:*)
  - Bash(git checkout:*)
  - Bash(git branch:*)
  - Bash(git log:*)
  - Bash(git status:*)
  - Bash(git diff:*)
  - Bash(gh pr:*)
  - Bash(grep:*)
  - Bash(wc:*)
  - Read
  - Edit
  - Glob
  - Grep
---

# Ship Release Workflow

Ship release version **$ARGUMENTS** via Pull Request.

## Instructions

You are the ship agent for metrics-governor. Execute the following PR-based release workflow:

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

# Ensure main is up to date
git fetch origin main
```

### 3. Generate Release Notes from Commits

Get commits since last tag:
```bash
git log $(git describe --tags --abbrev=0)..HEAD --oneline --no-merges
```

Format these into release notes for the PR body.

### 4. Analyze Test Coverage

Count tests in each category:
- Unit tests: `grep -r "func Test" internal/*/*.go | grep -v benchmark | wc -l`
- Functional tests: `grep -r "func Test" functional/*.go | wc -l`
- E2E tests: `grep -r "func Test" e2e/*.go test/e2e*.go | wc -l`
- Benchmarks: `grep -r "func Benchmark" internal/*/*.go | wc -l`

### 5. Check for Helm Changes

```bash
git diff $(git describe --tags --abbrev=0)..HEAD --name-only | grep "^helm/"
```

If helm/ has changes, update `helm/metrics-governor/Chart.yaml`:
- Set `version: <VERSION>`
- Set `appVersion: "<VERSION>"`

### 6. Update README

Update these in README.md:
- Tests badge: `[![Tests](https://img.shields.io/badge/Tests-<TOTAL>+-success`
- Test coverage table totals row with new counts

### 7. Update CHANGELOG.md

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

### 8. Run Tests

```bash
go test ./... -count=1
```

All tests must pass before continuing.

### 9. Create Release Branch and Commit

```bash
git checkout -b release/v<VERSION>

git add README.md CHANGELOG.md
# Add helm/metrics-governor/Chart.yaml if helm changed

git commit --no-gpg-sign -m "Release v<VERSION>

<DESCRIPTION>

- Update test coverage: <TOTAL>+ tests
- Update README badges and test table
- Update CHANGELOG"
```

**IMPORTANT**: Do NOT add "Co-Authored-By: Claude" to the commit message.

### 10. Push Branch and Create PR

Tell the user to touch their hardware security key, then push:

```bash
git push -u origin release/v<VERSION>
```

Create the PR with auto-merge enabled:

```bash
gh pr create \
  --title "Release v<VERSION>" \
  --body "## Release v<VERSION>

<DESCRIPTION>

### Changes since last release

<COMMIT_LOG_FROM_STEP_3>

### Test Coverage
- Unit Tests: <COUNT>
- Functional Tests: <COUNT>
- E2E Tests: <COUNT>
- Benchmarks: <COUNT>
- **Total: <TOTAL>+ tests**

### Checklist
- [x] README badges updated
- [x] CHANGELOG updated
- [x] Tests passing
- [x] Helm chart version bumped (if applicable)

---
After merge, the tag will be created and GitHub Actions will build the release." \
  --base main

# Enable auto-merge
gh pr merge --auto --squash
```

### 11. Report Success

After creating the PR, inform the user:

```
Release PR created for v<VERSION>!

PR: <PR_URL>

The PR has auto-merge enabled. Once CI passes:
1. PR will be automatically merged
2. You'll need to create and push the tag manually:
   git checkout main
   git pull origin main
   git tag -a "v<VERSION>" -m "Release v<VERSION> - <DESCRIPTION>"
   git push origin "v<VERSION>"

GitHub Actions will then:
- Build binaries: darwin-arm64, linux-arm64, linux-amd64
- Package Helm chart: metrics-governor-<VERSION>.tgz
- Build & push Docker images:
  - docker.io/slaskoss/metrics-governor:<VERSION>
  - docker.io/slaskoss/metrics-governor:latest
  - ghcr.io/szibis/metrics-governor:<VERSION>
  - ghcr.io/szibis/metrics-governor:latest

Monitor: https://github.com/szibis/metrics-governor/actions
```

## Error Handling

- If tests fail: Stop and report which tests failed
- If not on main: Stop and ask user to switch to main
- If tag exists: Stop and report the tag already exists
- If PR creation fails: Check gh auth status and retry
- If push fails: Remind user to touch hardware key and retry
