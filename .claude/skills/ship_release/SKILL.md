---
name: ship_release
description: Ship a new release via PR with automatic changelog from merged PRs, test coverage updates, and GitHub release
disable-model-invocation: true
argument-hint: <version> [description]
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
  - Bash(gh api:*)
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

You are the ship_release agent for metrics-governor. Execute the following PR-based release workflow:

### 1. Parse Arguments

Extract version and optional description from: `$ARGUMENTS`
- Format: `<version> [description]` (e.g., `0.6.3` or `0.6.3 Major performance improvements`)
- Version should be semantic versioning without 'v' prefix (e.g., `0.6.3`)
- Description is optional - if not provided, will be generated from merged PRs

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

### 3. Gather Merged PRs Since Last Release

Get all PRs merged since the last tag:

```bash
# Get last tag
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

# Get merged PRs since last tag using GitHub API
gh api graphql -f query='
  query {
    repository(owner: "szibis", name: "metrics-governor") {
      pullRequests(states: MERGED, first: 100, orderBy: {field: UPDATED_AT, direction: DESC}) {
        nodes {
          number
          title
          mergedAt
          labels(first: 10) {
            nodes { name }
          }
          author { login }
        }
      }
    }
  }
'
```

Filter PRs merged after the last tag date and categorize by labels:
- `bug` -> Bug Fixes
- `enhancement` -> New Features
- `performance` -> Performance Improvements
- `documentation` -> Documentation
- `ci` -> CI/CD
- `dependencies` -> Dependencies
- `breaking-change` -> Breaking Changes

### 4. Generate Release Notes

Create structured release notes from merged PRs:

```markdown
## What's Changed

### Breaking Changes
- PR title (#number) @author

### New Features
- PR title (#number) @author

### Bug Fixes
- PR title (#number) @author

### Performance Improvements
- PR title (#number) @author

### Documentation
- PR title (#number) @author

### Other Changes
- PR title (#number) @author

**Full Changelog**: https://github.com/szibis/metrics-governor/compare/v<PREV>...v<VERSION>
```

### 5. Analyze Test Coverage

Count tests in each category:
- Unit tests: `grep -r "func Test" internal/*/*.go | grep -v benchmark | wc -l`
- Functional tests: `grep -r "func Test" functional/*.go | wc -l`
- E2E tests: `grep -r "func Test" e2e/*.go test/e2e*.go | wc -l`
- Benchmarks: `grep -r "func Benchmark" internal/*/*.go | wc -l`

### 6. Check for Helm Changes

```bash
git diff $(git describe --tags --abbrev=0)..HEAD --name-only | grep "^helm/"
```

If helm/ has changes, update `helm/metrics-governor/Chart.yaml`:
- Set `version: <VERSION>`
- Set `appVersion: "<VERSION>"`

### 7. Update README

Update these in README.md:
- Tests badge: `[![Tests](https://img.shields.io/badge/Tests-<TOTAL>+-success`
- Test coverage table totals row with new counts

### 8. Update CHANGELOG.md

Add new version entry after `## [Unreleased]`:

```markdown
## [<VERSION>] - <YYYY-MM-DD>

<RELEASE_NOTES_FROM_STEP_4>

**Test Coverage:**
- Unit Tests: <COUNT>
- Functional Tests: <COUNT>
- E2E Tests: <COUNT>
- Benchmarks: <COUNT>
- Total: <TOTAL>+ tests
```

### 9. Run Tests

```bash
go test ./... -count=1
```

All tests must pass before continuing.

### 10. Create Release Branch and Commit

```bash
git checkout -b release/v<VERSION>

git add README.md CHANGELOG.md
# Add helm/metrics-governor/Chart.yaml if helm changed

git commit --no-gpg-sign -m "Release v<VERSION>

<SHORT_DESCRIPTION>

- Update test coverage: <TOTAL>+ tests
- Update README badges and test table
- Update CHANGELOG with merged PRs"
```

**IMPORTANT**: Do NOT add "Co-Authored-By: Claude" to the commit message.

### 11. Push Branch and Create PR

Tell the user to touch their hardware security key, then push:

```bash
git push -u origin release/v<VERSION>
```

Create the PR:

```bash
gh pr create \
  --title "Release v<VERSION>" \
  --body "<FULL_RELEASE_NOTES>" \
  --base main \
  --label release
```

### 12. Report Success

After creating the PR, inform the user:

```
Release PR created for v<VERSION>!

PR: <PR_URL>

Included PRs since v<PREV_VERSION>:
- #<NUM> <TITLE>
- #<NUM> <TITLE>
...

Once CI passes and PR is merged:
1. Tag v<VERSION> will be created automatically by GitHub Actions
2. Release workflow will build and publish:
   - Binaries: darwin-arm64, linux-arm64, linux-amd64
   - Helm chart: metrics-governor-<VERSION>.tgz
   - Docker images to Docker Hub and GHCR

Monitor: https://github.com/szibis/metrics-governor/actions
```

## Error Handling

- If tests fail: Stop and report which tests failed
- If not on main: Stop and ask user to switch to main
- If tag exists: Stop and report the tag already exists
- If PR creation fails: Check gh auth status and retry
- If push fails: Remind user to touch hardware key and retry
