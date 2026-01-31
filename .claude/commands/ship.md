# Ship Release Workflow

Ship release version **$ARGUMENTS** via Pull Request.

## Quick Reference

```bash
# Use the script directly
./scripts/release.sh 0.5.3 -m "Add new feature X"

# Or via make
make ship VERSION=0.5.3 MESSAGE="Add new feature X"

# Dry run to preview changes
make ship-dry-run VERSION=0.5.3 MESSAGE="Add new feature X"
```

## What the Ship Process Does

1. **Validates** - Checks you're on main, no uncommitted changes, tag doesn't exist
2. **Generates release notes** - Collects commits since last tag
3. **Updates test coverage** - Counts all tests and benchmarks
4. **Updates files** - README badges, CHANGELOG, Helm chart version
5. **Runs tests** - Ensures all tests pass
6. **Creates release branch** - `release/v<VERSION>`
7. **Creates PR** - With auto-merge enabled

## After PR Merges (Fully Automated)

Once CI passes and the PR is merged:

1. **Tag is created automatically** by the `tag-on-merge` workflow
2. **Release workflow triggers** and builds:
   - Binaries for darwin-arm64, linux-arm64, linux-amd64
   - Helm chart package
   - Docker images pushed to Docker Hub and GHCR

No manual steps required after PR merge!

## Troubleshooting

- **Not on main**: `git checkout main && git pull`
- **Tag exists**: Choose a different version number
- **gh not authenticated**: `gh auth login`
- **Push fails**: Touch your hardware security key
