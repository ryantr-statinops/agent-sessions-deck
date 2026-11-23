#!/bin/sh
set -eu

# package-release.sh — Build and package Linux release archives with SHA-256 checksums
#
# Usage:
#   ./scripts/package-release.sh [version]
#
# Generates:
#   dist/asd_<version>_linux_amd64.tar.gz
#   dist/asd_<version>_linux_arm64.tar.gz
#   dist/sha256sums.txt

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

DIST_DIR="$ROOT_DIR/dist"
STAGE_DIR="$ROOT_DIR/bin/dist"

rm -rf "$DIST_DIR" "$STAGE_DIR"
mkdir -p "$DIST_DIR" "$STAGE_DIR"

LDFLAGS="-s -w -X main.buildVersion=${VERSION} -X main.buildCommit=${COMMIT} -X main.buildDate=${DATE}"

TARGETS="linux/amd64 linux/arm64"

for TARGET in $TARGETS; do
    OS="$(echo "$TARGET" | cut -d/ -f1)"
    ARCH="$(echo "$TARGET" | cut -d/ -f2)"
    TARGET_NAME="asd_${VERSION}_${OS}_${ARCH}"
    TARGET_STAGE="$STAGE_DIR/$TARGET_NAME"

    echo "==> Building ${TARGET} (${VERSION})..."
    mkdir -p "$TARGET_STAGE"

    CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build \
        -trimpath \
        -ldflags "$LDFLAGS" \
        -o "$TARGET_STAGE/asd" \
        ./cmd/asd

    CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build \
        -trimpath \
        -ldflags "$LDFLAGS" \
        -o "$TARGET_STAGE/agentd" \
        ./cmd/agentd

    # Copy release collateral into archive payload
    cp LICENSE "$TARGET_STAGE/LICENSE"
    cp README.md "$TARGET_STAGE/README.md"
    cp config.example.yaml "$TARGET_STAGE/config.example.yaml"

    echo "==> Packaging ${TARGET_NAME}.tar.gz..."
    tar -czf "$DIST_DIR/${TARGET_NAME}.tar.gz" -C "$STAGE_DIR" "$TARGET_NAME"
done

echo "==> Generating SHA-256 checksums..."
cd "$DIST_DIR"
sha256sum asd_*.tar.gz > sha256sums.txt

echo "==> Release artifacts ready in dist/:"
cat sha256sums.txt
