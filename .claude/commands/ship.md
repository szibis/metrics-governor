# Ship Release Workflow

Ship release version **$ARGUMENTS** via Pull Request.

## Quick Reference

```bash
# Use the script directly
./scripts/release.sh 0.5.3 -m "Add new feature X"

# Or via make
make release-version VERSION=0.5.3 MESSAGE="Add new feature X"

# Dry run to preview changes
make release-dry-run VERSION=0.5.3 MESSAGE="Add new feature X"
```

## What the Ship Process Does

1. **Validates** - Checks you're on main, no uncommitted changes, tag doesn't exist
2. **Generates release notes** - Collects commits since last tag
3. **Updates test coverage** - Counts all tests and benchmarks
4. **Updates files** - README badges, CHANGELOG, Helm chart version
5. **Runs tests** - Ensures all tests pass
6. **Creates release branch** - `release/v<VERSION>`
7. **Creates PR** - With auto-merge enabled
8. **After merge** - You manually create and push the tag

## After PR Merges

Once CI passes and the PR is merged:

```bash
git checkout main
git pull origin main
git tag -a "v<VERSION>" -m "Release v<VERSION> - <DESCRIPTION>"
git push origin "v<VERSION>"
```

GitHub Actions will then:
- Build binaries for darwin-arm64, linux-arm64, linux-amd64
- Package Helm chart
- Build & push Docker images to Docker Hub and GHCR

## Troubleshooting

- **Not on main**: `git checkout main && git pull`
- **Tag exists**: Choose a different version number
- **gh not authenticated**: `gh auth login`
- **Push fails**: Touch your hardware security key
