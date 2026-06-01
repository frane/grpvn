#!/bin/sh
set -e

REPO="frane/grpvn"
BINARY="grpvn"

OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

RELEASE_URL="https://github.com/$REPO/releases/latest/download/grpvn-$OS-$ARCH.tar.gz"

echo "Downloading grpvn for $OS/$ARCH..."
curl -sSL "$RELEASE_URL" | tar -xz

chmod +x "$BINARY"
sudo mv "$BINARY" /usr/local/bin/
echo "grpvn installed to /usr/local/bin"
