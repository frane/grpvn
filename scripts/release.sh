#!/bin/bash
set -e

PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64")

mkdir -p dist

for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo $PLATFORM | cut -d/ -f1)
    ARCH=$(echo $PLATFORM | cut -d/ -f2)
    BINARY="grpvn"
    if [ "$OS" == "windows" ]; then
        BINARY="grpvn.exe"
    fi
    OUTPUT="dist/grpvn-$OS-$ARCH/$BINARY"
    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o "$OUTPUT" ./cmd/grpvn
done
