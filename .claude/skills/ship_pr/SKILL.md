---
name: ship_pr
description: Create a Pull Request with conventional commit title, automatic labels, and structured description
disable-model-invocation: true
argument-hint: <type>: <description> OR --title "<title>" --body "<body>"
allowed-tools:
  - Bash(git add:*)
  - Bash(git commit:*)
  - Bash(git push:*)
  - Bash(git checkout:*)
  - Bash(git branch:*)
  - Bash(git log:*)
  - Bash(git status:*)
  - Bash(git diff:*)
  - Bash(gh pr:*)
  - Bash(gh label:*)
  - Read
  - Edit
  - Glob
  - Grep
---

# Ship PR Workflow

Create a Pull Request with **$ARGUMENTS**.

## Instructions

You are the ship_pr agent for metrics-governor. Create a well-structured PR following conventional commits.

### 1. Parse Arguments

Arguments can be:
- Simple: `fix: resolve data race in buffer`
- With scope: `feat(prw): add sharding support`
- Full options: `--title "feat: add feature" --body "Description here" --draft`

Conventional commit types:
- `feat:` - New feature (label: `enhancement`)
- `fix:` - Bug fix (label: `bug`)
- `docs:` - Documentation (label: `documentation`)
- `perf:` - Performance (label: `performance`)
- `refactor:` - Code refactoring (label: `refactor`)
- `test:` - Tests (label: `testing`)
- `ci:` - CI/CD (label: `ci`)
- `chore:` - Maintenance (label: `chore`)
- `build:` - Build system (label: `build`)

### 2. Check Current State

```bash
# Get current branch
BRANCH=$(git branch --show-current)

# Check if we're on main (need to create feature branch)
if [ "$BRANCH" = "main" ]; then
  echo "On main - will create feature branch"
fi

# Check for uncommitted changes
git status --porcelain

# Get changed files for component detection
git diff --name-only HEAD
```

### 3. Determine Branch Name

Generate branch name from PR type and description:
- `feat: add user auth` -> `feature/add-user-auth`
- `fix: resolve race condition` -> `fix/resolve-race-condition`
- `docs: update readme` -> `docs/update-readme`

```bash
# Create and switch to branch if on main
git checkout -b <branch-name>
```

### 4. Detect Component Labels

Based on changed files, determine component labels:

```bash
git diff --name-only main...HEAD
```

Map paths to labels:
- `internal/buffer/` -> `component/buffer`
- `internal/queue/` -> `component/queue`
- `internal/exporter/` -> `component/exporter`
- `internal/receiver/` -> `component/receiver`
- `internal/config/` -> `component/config`
- `internal/compression/` -> `component/compression`
- `internal/auth/` -> `component/auth`
- `internal/limits/` -> `component/limits`
- `internal/stats/` -> `component/stats`
- `internal/sharding/` -> `component/sharding`
- `internal/prw/` -> `component/prw`
- `helm/` -> `helm`
- `.github/` -> `ci`
- `*.md` or `docs/` -> `documentation`

### 5. Calculate Size Label

```bash
# Count total changes
ADDITIONS=$(git diff --stat main...HEAD | tail -1 | grep -oP '\d+(?= insertion)')
DELETIONS=$(git diff --stat main...HEAD | tail -1 | grep -oP '\d+(?= deletion)')
TOTAL=$((ADDITIONS + DELETIONS))
```

Size labels:
- ≤10 lines: `size/XS`
- ≤50 lines: `size/S`
- ≤200 lines: `size/M`
- ≤500 lines: `size/L`
- >500 lines: `size/XL`

### 6. Generate PR Body

Create structured PR body:

```markdown
## Summary

<Brief description from commit type and message>

### Changes

<List of key changes based on commits>

---

## Test Plan

- [ ] Unit tests pass (`go test ./internal/...`)
- [ ] Relevant tests added/updated

## Checklist

- [ ] Code follows project style guidelines
- [ ] Self-review completed
- [ ] Documentation updated (if applicable)
```

### 7. Commit Changes (if needed)

If there are uncommitted changes:

```bash
git add -A
git commit --no-gpg-sign -m "<type>(<scope>): <description>

<body if provided>"
```

**IMPORTANT**: Do NOT add "Co-Authored-By: Claude" to the commit message.

### 8. Push Branch

Tell user to touch hardware security key if needed:

```bash
git push -u origin <branch-name>
```

### 9. Create PR with Labels

```bash
# Collect all labels
LABELS="<type-label>,<component-labels>,<size-label>"

gh pr create \
  --title "<conventional-commit-title>" \
  --body "<generated-body>" \
  --base main \
  --label "$LABELS"
```

If `--draft` was specified:
```bash
gh pr create --draft ...
```

### 10. Report Success

```
PR created successfully!

PR: <PR_URL>
Branch: <branch-name>
Labels: <labels>

The PR will be automatically labeled with:
- Type: <type-label>
- Components: <component-labels>
- Size: <size-label>

CI will run automatically. Once checks pass, the PR is ready for review.
```

## Examples

### Simple bug fix
```
/ship_pr fix: resolve data race in buffer
```
Creates PR with title "fix: resolve data race in buffer", labels: `bug`, `component/buffer`, `size/S`

### Feature with scope
```
/ship_pr feat(prw): add sharding support for remote write
```
Creates PR with title "feat(prw): add sharding support for remote write", labels: `enhancement`, `component/prw`, `size/M`

### Draft PR
```
/ship_pr feat: new feature --draft
```
Creates draft PR

### Full options
```
/ship_pr --title "fix: critical bug" --body "Detailed description" --label security
```

## Error Handling

- If no changes: Stop and report no changes to commit
- If branch exists: Ask user to choose different name or force
- If PR exists: Report existing PR URL
- If push fails: Remind user to touch hardware key and retry
