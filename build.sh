#!/usr/bin/env bash
# Build SimpleMon agent binaries for all supported Linux architectures.
# Version and commit are injected into the binary (visible via -version and
# reported to the server in the X-Agent-Version header). A machine-readable
# agent-version.json manifest is written next to the binaries.
set -euo pipefail

# Load environment variables from .env file
if [ -f .env ]; then
    export $(cat .env | grep -v '#' | awk '/=/ {print $1}')
else
    echo ".env file not found. Please create it with BUILD_PATH variable."
    exit 1
fi

# Check if BUILD_PATH is set
if [ -z "${BUILD_PATH:-}" ]; then
    echo "BUILD_PATH is not set. Please set it in .env."
    exit 1
fi

# Create build path directory if it doesn't exist
mkdir -p "$BUILD_PATH"

# Version from the latest git tag, falling back to a short commit hash.
# Tags use the "agent-v1.2.3" format - strip the prefix to get clean semver.
VERSION="$(git describe --tags --match 'agent-v[0-9]*' --abbrev=0 2>/dev/null || echo '')"
VERSION="${VERSION#agent-}"
if [ -z "$VERSION" ]; then
    VERSION="v0.0.0"
fi
COMMIT="$(git rev-parse --short HEAD)"

LDFLAGS="-X main.version=${VERSION} -X main.commit=${COMMIT}"

echo "Building simplemon-agent ${VERSION} (${COMMIT})"

# Build Go file for various platforms
GOOS=linux GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-linux-amd64" cmd/simplemon-agent/main.go
GOOS=linux GOARCH=386 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-linux-x86" cmd/simplemon-agent/main.go
GOOS=linux GOARCH=arm go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-linux-arm" cmd/simplemon-agent/main.go
GOOS=linux GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-linux-arm64" cmd/simplemon-agent/main.go
# GOOS=darwin GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-macos-amd64" cmd/simplemon-agent/main.go
# GOOS=darwin GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-macos-arm64" cmd/simplemon-agent/main.go
# GOOS=windows GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-windows-amd64.exe" cmd/simplemon-agent/main.go
# GOOS=windows GOARCH=386 go build -ldflags "${LDFLAGS}" -o "$BUILD_PATH/simplemon-windows-386.exe" cmd/simplemon-agent/main.go

( cd "$BUILD_PATH" && shasum -a 256 simplemon-linux-* > SHA256SUMS )

# Manifest consumed by the panel (UI badge) and update tooling.
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > "$BUILD_PATH/agent-version.json" <<EOF
{
  "version": "${VERSION}",
  "commit": "${COMMIT}",
  "built_at": "${BUILD_TIME}"
}
EOF

echo "Done. Binaries and agent-version.json written to ${BUILD_PATH}."
