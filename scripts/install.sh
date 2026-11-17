#!/usr/bin/env bash
# Universal Installer for Linux & macOS (Darwin)
set -euo pipefail

REPO="unified-platform/container"
BINARY_NAME="skipper"
INSTALL_DIR="/usr/local/bin"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "==> Detected Platform: ${OS}/${ARCH}"

if [ -f "./bin/skipper-${OS}-${ARCH}" ]; then
  echo "==> Installing from local build..."
  sudo cp "./bin/skipper-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY_NAME}"
elif [ -f "./bin/skipper" ]; then
  sudo cp "./bin/skipper" "${INSTALL_DIR}/${BINARY_NAME}"
else
  echo "==> Building binary for ${OS}/${ARCH}..."
  go build -o "${INSTALL_DIR}/${BINARY_NAME}" ./cmd/skipper
fi

sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
echo "==> Installation complete! Run 'skipper --help' to get started."
