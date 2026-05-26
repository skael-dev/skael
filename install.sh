#!/bin/sh
set -e

REPO="alternayte/skael-releases"
BINARY="skael"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

get_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) echo "unsupported: $(uname -s)" >&2; exit 1 ;;
  esac
}

get_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "unsupported: $(uname -m)" >&2; exit 1 ;;
  esac
}

OS=$(get_os)
ARCH=$(get_arch)

if [ -n "$1" ]; then
  VERSION="$1"
else
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
fi

if [ -z "$VERSION" ]; then
  echo "Error: could not determine latest version" >&2
  exit 1
fi

EXT="tar.gz"
if [ "$OS" = "windows" ]; then
  EXT="zip"
fi

FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading skael v${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}"

CHECKSUM_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"
curl -fsSL "$CHECKSUM_URL" -o "${TMPDIR}/checksums.txt"

cd "$TMPDIR"
if command -v sha256sum >/dev/null 2>&1; then
  grep "$FILENAME" checksums.txt | sha256sum -c --quiet
elif command -v shasum >/dev/null 2>&1; then
  grep "$FILENAME" checksums.txt | shasum -a 256 -c --quiet
else
  echo "Warning: no sha256sum or shasum found, skipping checksum verification" >&2
fi

if [ "$EXT" = "tar.gz" ]; then
  tar xzf "$FILENAME"
else
  unzip -q "$FILENAME"
fi

if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY" "$INSTALL_DIR/"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "$BINARY" "$INSTALL_DIR/"
fi

echo "skael v${VERSION} installed to ${INSTALL_DIR}/skael"
