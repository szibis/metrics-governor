#!/bin/bash
# Release script for metrics-governor
# Creates a PR with version bumping, changelog, tests coverage, badges, helm chart updates

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$ROOT_DIR"

#######################################
# Print colored message
#######################################
info() { echo -e "${BLUE}[INFO]${NC} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

#######################################
# Show usage
#######################################
usage() {
    cat << EOF
Usage: $(basename "$0") <version> [options]

Arguments:
    version     Version to release (e.g., 0.5.2, 1.0.0)

Options:
    -m, --message   Release message/description (required)
    -n, --dry-run   Show what would be done without making changes
    -h, --help      Show this help message

Examples:
    $(basename "$0") 0.5.2 -m "Add new feature X"
    $(basename "$0") 1.0.0 -m "Major release with breaking changes"
    $(basename "$0") 0.5.3 -m "Bug fixes" --dry-run

EOF
    exit 0
}

#######################################
# Parse arguments
#######################################
VERSION=""
MESSAGE=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -m|--message)
            MESSAGE="$2"
            shift 2
            ;;
        -n|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            if [[ -z "$VERSION" ]]; then
                VERSION="$1"
            else
                error "Unknown argument: $1"
            fi
            shift
            ;;
    esac
done

[[ -z "$VERSION" ]] && error "Version is required. Use -h for help."
[[ -z "$MESSAGE" ]] && error "Release message is required. Use -m 'message'"

# Validate version format
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    error "Invalid version format. Use semantic versioning (e.g., 1.0.0)"
fi

info "Preparing release v${VERSION}"
info "Message: ${MESSAGE}"
[[ "$DRY_RUN" == true ]] && warn "DRY RUN MODE - No changes will be made"

#######################################
# Check prerequisites
#######################################
info "Checking prerequisites..."

# Check we're on main branch
CURRENT_BRANCH=$(git branch --show-current)
[[ "$CURRENT_BRANCH" != "main" ]] && error "Must be on main branch (currently on $CURRENT_BRANCH)"

# Check working directory is clean
if [[ -n $(git status --porcelain) ]]; then
    warn "Working directory has uncommitted changes"
    git status --short
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    [[ ! $REPLY =~ ^[Yy]$ ]] && exit 1
fi

# Check tag doesn't exist
if git rev-parse "v${VERSION}" >/dev/null 2>&1; then
    error "Tag v${VERSION} already exists"
fi

# Check gh CLI is authenticated
if ! gh auth status >/dev/null 2>&1; then
    error "GitHub CLI not authenticated. Run 'gh auth login' first."
fi

# Fetch latest from origin
git fetch origin main

success "Prerequisites check passed"

#######################################
# Generate release notes from commits
#######################################
info "Generating release notes from commits..."

LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
if [[ -n "$LAST_TAG" ]]; then
    COMMIT_LOG=$(git log "${LAST_TAG}..HEAD" --oneline --no-merges)
else
    COMMIT_LOG=$(git log --oneline --no-merges -20)
fi

info "Commits since last release:"
echo "$COMMIT_LOG"

#######################################
# Count tests and calculate coverage
#######################################
info "Analyzing test coverage..."

count_tests() {
    local dir=$1
    grep -r "func Test" "$dir"/*_test.go 2>/dev/null | grep -v benchmark | wc -l | tr -d ' '
}

count_benchmarks() {
    local dir=$1
    grep -r "func Benchmark" "$dir"/*_test.go 2>/dev/null | wc -l | tr -d ' '
}

# Count by component
BUFFER_UNIT=$(count_tests "internal/buffer")
EXPORTER_UNIT=$(count_tests "internal/exporter")
RECEIVER_UNIT=$(count_tests "internal/receiver")
LIMITS_UNIT=$(count_tests "internal/limits")
QUEUE_UNIT=$(count_tests "internal/queue")
SHARDING_UNIT=$(count_tests "internal/sharding")
STATS_UNIT=$(count_tests "internal/stats")
CONFIG_UNIT=$(count_tests "internal/config")
AUTH_UNIT=$(count_tests "internal/auth")
TLS_UNIT=$(count_tests "internal/tls")
COMPRESSION_UNIT=$(count_tests "internal/compression")
LOGGING_UNIT=$(count_tests "internal/logging")

# Functional tests
FUNCTIONAL_TOTAL=$(grep -r "func Test" functional/*_test.go 2>/dev/null | wc -l | tr -d ' ')

# E2E tests
E2E_TOTAL=$(grep -r "func Test" e2e/*_test.go test/e2e*_test.go 2>/dev/null | wc -l | tr -d ' ')

# Benchmarks
BENCHMARK_TOTAL=$(grep -r "func Benchmark" internal/*/*_test.go 2>/dev/null | wc -l | tr -d ' ')

# Calculate totals
UNIT_TOTAL=$((BUFFER_UNIT + EXPORTER_UNIT + RECEIVER_UNIT + LIMITS_UNIT + QUEUE_UNIT + SHARDING_UNIT + STATS_UNIT + CONFIG_UNIT + AUTH_UNIT + TLS_UNIT + COMPRESSION_UNIT + LOGGING_UNIT))
TOTAL_TESTS=$((UNIT_TOTAL + FUNCTIONAL_TOTAL + E2E_TOTAL))

info "Test counts: Unit=$UNIT_TOTAL, Functional=$FUNCTIONAL_TOTAL, E2E=$E2E_TOTAL, Benchmarks=$BENCHMARK_TOTAL"
info "Total tests: $TOTAL_TESTS"

#######################################
# Check if Helm chart has changes
#######################################
info "Checking for Helm chart changes..."

HELM_CHANGED=false

if [[ -n "$LAST_TAG" ]]; then
    if git diff --name-only "$LAST_TAG"..HEAD | grep -q "^helm/"; then
        HELM_CHANGED=true
        info "Helm chart has changes since $LAST_TAG"
    else
        info "No Helm chart changes since $LAST_TAG"
    fi
else
    HELM_CHANGED=true
    info "No previous tag found, will update Helm chart"
fi

#######################################
# Update README badges and test coverage table
#######################################
info "Updating README..."

update_readme() {
    local readme="$ROOT_DIR/README.md"
    local temp_file=$(mktemp)

    # Update Tests badge
    sed -E "s/\[Tests\]\(https:\/\/img\.shields\.io\/badge\/Tests-[0-9]+-/[Tests](https:\/\/img.shields.io\/badge\/Tests-${TOTAL_TESTS}+-/" "$readme" > "$temp_file"
    mv "$temp_file" "$readme"

    # Update test coverage table - this is more complex, update totals row
    sed -E "s/\| \*\*Total\*\* \| \*\*[0-9]+\*\* \| \*\*[0-9]+\*\* \| \*\*[0-9]+\*\* \| \*\*[0-9]+\*\*/| **Total** | **${UNIT_TOTAL}** | **${FUNCTIONAL_TOTAL}** | **${E2E_TOTAL}** | **${BENCHMARK_TOTAL}**/" "$readme" > "$temp_file"
    mv "$temp_file" "$readme"

    info "Updated README with test counts"
}

if [[ "$DRY_RUN" == false ]]; then
    update_readme
fi

#######################################
# Update CHANGELOG
#######################################
info "Updating CHANGELOG..."

update_changelog() {
    local changelog="$ROOT_DIR/CHANGELOG.md"
    local today=$(date +%Y-%m-%d)
    local temp_file=$(mktemp)

    # Create new changelog entry
    cat > "$temp_file" << EOF
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [${VERSION}] - ${today}

### Changed

${MESSAGE}

**Test Coverage:**
- Unit Tests: ${UNIT_TOTAL}
- Functional Tests: ${FUNCTIONAL_TOTAL}
- E2E Tests: ${E2E_TOTAL}
- Benchmarks: ${BENCHMARK_TOTAL}
- Total: ${TOTAL_TESTS}+ tests

EOF

    # Append existing changelog (skip header)
    tail -n +9 "$changelog" >> "$temp_file"
    mv "$temp_file" "$changelog"

    info "Updated CHANGELOG with v${VERSION}"
}

if [[ "$DRY_RUN" == false ]]; then
    update_changelog
fi

#######################################
# Update Helm chart version
#######################################
if [[ "$HELM_CHANGED" == true ]]; then
    info "Updating Helm chart version..."

    update_helm() {
        local chart="$ROOT_DIR/helm/metrics-governor/Chart.yaml"
        sed -i.bak "s/^version:.*/version: ${VERSION}/" "$chart"
        sed -i.bak "s/^appVersion:.*/appVersion: \"${VERSION}\"/" "$chart"
        rm -f "${chart}.bak"
        info "Updated Helm chart to v${VERSION}"
    }

    if [[ "$DRY_RUN" == false ]]; then
        update_helm
    fi
fi

#######################################
# Run tests to verify
#######################################
info "Running tests..."

if [[ "$DRY_RUN" == false ]]; then
    if ! go test ./... -count=1 > /dev/null 2>&1; then
        error "Tests failed! Fix tests before releasing."
    fi
    success "All tests passed"
else
    info "[DRY RUN] Would run: go test ./..."
fi

#######################################
# Create release branch and commit
#######################################
BRANCH_NAME="release/v${VERSION}"

info "Creating release branch: $BRANCH_NAME"

if [[ "$DRY_RUN" == false ]]; then
    git checkout -b "$BRANCH_NAME"

    # Stage all changes
    git add README.md CHANGELOG.md
    [[ "$HELM_CHANGED" == true ]] && git add helm/metrics-governor/Chart.yaml

    # Check if there are changes to commit
    if git diff --cached --quiet; then
        info "No changes to commit"
    else
        # Commit without GPG signing (user will push with their key)
        git commit --no-gpg-sign -m "Release v${VERSION}

${MESSAGE}

- Update test coverage: ${TOTAL_TESTS}+ tests
- Update README badges and test table
- Update CHANGELOG"

        success "Changes committed"
    fi
else
    info "[DRY RUN] Would create branch: $BRANCH_NAME"
    info "[DRY RUN] Would commit: Release v${VERSION}"
fi

#######################################
# Push branch and create PR
#######################################
info "Pushing branch and creating PR..."

# Format commit log for PR body
PR_COMMIT_LOG=$(echo "$COMMIT_LOG" | sed 's/^/- /')

PR_BODY="## Release v${VERSION}

${MESSAGE}

### Changes since last release

${PR_COMMIT_LOG}

### Test Coverage
- Unit Tests: ${UNIT_TOTAL}
- Functional Tests: ${FUNCTIONAL_TOTAL}
- E2E Tests: ${E2E_TOTAL}
- Benchmarks: ${BENCHMARK_TOTAL}
- **Total: ${TOTAL_TESTS}+ tests**

### Checklist
- [x] README badges updated
- [x] CHANGELOG updated
- [x] Tests passing
- [x] Helm chart version bumped (if applicable)

---
After merge, the tag will be created automatically and GitHub Actions will build the release."

if [[ "$DRY_RUN" == false ]]; then
    echo ""
    echo "==========================================="
    echo -e "${YELLOW}Touch your hardware security key to push${NC}"
    echo "==========================================="
    echo ""

    git push -u origin "$BRANCH_NAME"
    success "Branch pushed"

    # Create PR
    PR_URL=$(gh pr create \
        --title "Release v${VERSION}" \
        --body "$PR_BODY" \
        --base main)

    success "PR created: $PR_URL"

    # Enable auto-merge
    info "Enabling auto-merge..."
    if gh pr merge --auto --squash 2>/dev/null; then
        success "Auto-merge enabled"
    else
        warn "Could not enable auto-merge (may require repo settings adjustment)"
    fi

    echo ""
    echo "==========================================="
    echo -e "${GREEN}Release PR created for v${VERSION}!${NC}"
    echo "==========================================="
    echo ""
    echo "PR: $PR_URL"
    echo ""
    echo "Once CI passes and PR is merged:"
    echo "  1. Tag v${VERSION} will be created automatically"
    echo "  2. Release workflow will build and publish:"
    echo "     - Binaries: darwin-arm64, linux-arm64, linux-amd64"
    echo "     - Helm chart: metrics-governor-${VERSION}.tgz"
    echo "     - Docker images to Docker Hub and GHCR"
    echo ""
    echo "Monitor: https://github.com/szibis/metrics-governor/actions"
    echo ""
else
    info "[DRY RUN] Would push branch: $BRANCH_NAME"
    info "[DRY RUN] Would create PR: Release v${VERSION}"
    info "[DRY RUN] Would enable auto-merge"
    echo ""
    echo "PR body would be:"
    echo "---"
    echo "$PR_BODY"
    echo "---"
fi

success "Done!"
