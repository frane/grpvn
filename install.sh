#!/bin/sh
# Installs the latest grpvn release binary with sha256 verification.
#   curl -sSL https://raw.githubusercontent.com/frane/grpvn/main/install.sh | sh
# Override the install directory with BINDIR (default /usr/local/bin).
set -eu

REPO="frane/grpvn"
BINARY="grpvn"
BINDIR="${BINDIR:-/usr/local/bin}"

OS=$(uname | tr '[:upper:]' '[:lower:]')
case "$OS" in
    linux|darwin) ;;
    *) echo "Unsupported OS: $OS (use 'go install github.com/frane/grpvn/cmd/grpvn@latest')"; exit 1 ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="x86_64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
if [ -z "$TAG" ]; then
    echo "Could not determine the latest release tag"; exit 1
fi
VERSION=${TAG#v}

# Archive name must match .goreleaser.yaml's name_template:
#   {ProjectName}_{Version}_{Os}_{x86_64|arm64}.tar.gz
ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/$REPO/releases/download/$TAG"

TMPDIR_INSTALL=$(mktemp -d)
trap 'rm -rf "$TMPDIR_INSTALL"' EXIT
cd "$TMPDIR_INSTALL"

echo "Downloading $BINARY $TAG for $OS/$ARCH..."
curl -fsSL -o "$ARCHIVE" "$BASE/$ARCHIVE"
curl -fsSL -o checksums.txt "$BASE/checksums.txt"

# Verify against the goreleaser-published sha256 manifest.
EXPECTED=$(grep " $ARCHIVE\$" checksums.txt | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
    echo "checksums.txt has no entry for $ARCHIVE"; exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$ARCHIVE" | awk '{print $1}')
else
    ACTUAL=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Checksum mismatch for $ARCHIVE"
    echo "  expected: $EXPECTED"
    echo "  actual:   $ACTUAL"
    exit 1
fi

tar -xzf "$ARCHIVE" "$BINARY"
chmod +x "$BINARY"

if [ -w "$BINDIR" ]; then
    mv "$BINARY" "$BINDIR/"
else
    echo "Installing to $BINDIR (sudo required)..."
    sudo mv "$BINARY" "$BINDIR/"
fi
echo "$BINARY $TAG installed to $BINDIR/$BINARY"
