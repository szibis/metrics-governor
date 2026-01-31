# Ship Release Workflow

Ship release version **$ARGUMENTS** via Pull Request.

## Usage

```bash
/ship_release 0.6.3
/ship_release 0.6.3 Major performance improvements
```

## What It Does

1. **Gathers merged PRs** - Collects all PRs merged since last release tag
2. **Categorizes changes** - Groups by labels (bug, enhancement, performance, etc.)
3. **Generates release notes** - Creates structured changelog from PRs
4. **Updates test coverage** - Counts all tests and benchmarks
5. **Updates files** - README badges, CHANGELOG, Helm chart version
6. **Runs tests** - Ensures all tests pass
7. **Creates release branch** - `release/v<VERSION>`
8. **Creates PR** - With full release notes

## After PR Merges (Fully Automated)

Once CI passes and the PR is merged:

1. **Tag is created automatically** by the `tag-on-merge` workflow
2. **Release workflow triggers** and builds:
   - Binaries for darwin-arm64, linux-arm64, linux-amd64
   - Helm chart package
   - Docker images pushed to Docker Hub and GHCR

No manual steps required after PR merge!

## Example Output

```markdown
## What's Changed

### New Features
- Add automatic PR labeling (#15) @szibis

### Bug Fixes
- Fix data race in ShardKeyBuilder (#12) @szibis

### Performance Improvements
- Optimize buffer allocation (#10) @szibis

**Full Changelog**: https://github.com/szibis/metrics-governor/compare/v0.6.2...v0.6.3
```

## Troubleshooting

- **Not on main**: `git checkout main && git pull`
- **Tag exists**: Choose a different version number
- **gh not authenticated**: `gh auth login`
- **Push fails**: Touch your hardware security key
