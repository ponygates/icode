#!/bin/bash
# ============================================================================
# iCode Release Script
# ============================================================================
#
# Usage:
#   ./scripts/release.sh <version>        # e.g. ./scripts/release.sh v0.2.0
#   ./scripts/release.sh <version> "msg"   # with optional release notes
#
# This script:
#   1. Updates version in release tag
#   2. Builds cross-platform binaries (Linux/macOS/Windows)
#   3. Creates a GitHub release (requires gh CLI)
#   4. Updates CHANGELOG.md with release date
#
# Prerequisites:
#   - Go 1.26+
#   - gh CLI (authenticated)
#   - git (on main/master branch)
# ============================================================================

set -euo pipefail

# ── Colors ──────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}   $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# ── Validate arguments ─────────────────────────────────────────────────
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    error "Usage: $0 <version> [release-notes]"
fi

if ! echo "$VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+'; then
    error "Version must be in format vX.Y.Z (e.g. v0.2.0)"
fi

RELEASE_NOTES="${2:-}"

# ── Prerequisites ──────────────────────────────────────────────────────
command -v go >/dev/null 2>&1 || error "Go is required"
command -v git >/dev/null 2>&1 || error "git is required"

GO_VERSION=$(go version | sed 's/.*go\([0-9]\+\.[0-9]\+\).*/\1/')
info "Go version: $GO_VERSION"

# ── Verify git state ───────────────────────────────────────────────────
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" != "main" ] && [ "$BRANCH" != "master" ]; then
    error "Must be on main or master branch (current: $BRANCH)"
fi

if ! git diff-index --quiet HEAD --; then
    error "Working tree is dirty. Commit or stash changes first."
fi

info "Branch: $BRANCH"
info "Version: $VERSION"

# ── Build ──────────────────────────────────────────────────────────────
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT=$(git rev-parse --short HEAD)
LDFLAGS="-s -w -X main.Version=${VERSION#v} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

info "Building binaries..."

build() {
    local os="$1" arch="$2" suffix="$3"
    local out="release/icode-${os}-${arch}${suffix}"

    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags="$LDFLAGS" -o "$out" .
    ok "Built: $out"
}

mkdir -p release

build linux   amd64 ""
build darwin  amd64 ""
build darwin  arm64 ""
build windows amd64 ".exe"

# ── Run tests ──────────────────────────────────────────────────────────
info "Running tests..."
go test -count=1 -timeout=120s ./...

# ── Tag and push ───────────────────────────────────────────────────────
info "Creating git tag: $VERSION"
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"
ok "Tag pushed: $VERSION"

# ── Create GitHub Release ──────────────────────────────────────────────
if command -v gh >/dev/null 2>&1; then
    info "Creating GitHub release..."

    if [ -n "$RELEASE_NOTES" ]; then
        gh release create "$VERSION" \
            release/icode-* \
            --title "iCode $VERSION" \
            --notes "$RELEASE_NOTES"
    else
        gh release create "$VERSION" \
            release/icode-* \
            --title "iCode $VERSION" \
            --generate-notes
    fi
else
    warn "gh CLI not found. Tag created but release not published."
    warn "Create release manually at: https://github.com/ponygates/icode/releases/new"
fi

# ── Summary ─────────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo "  iCode $VERSION 发布完成"
echo "========================================"
echo ""
echo "  Binaries:"
ls -lh release/
echo ""
echo "  To verify:"
echo "    gh release view $VERSION"
echo ""
