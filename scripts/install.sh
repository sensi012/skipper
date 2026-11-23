#!/usr/bin/env bash
# Universal Installer for Linux & macOS (Darwin)
set -euo pipefail

REPO="${SKIPPER_REPO:-sensi012/skipper}"
BINARY_NAME="skipper"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
SKIPPER_VERSION="${SKIPPER_VERSION:-}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported operating system: $OS" >&2; exit 1 ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

install_binary() {
  local src="$1"
  if { [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; } || [ "$(id -u)" -eq 0 ]; then
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$src" "${INSTALL_DIR}/${BINARY_NAME}"
  elif command -v sudo >/dev/null 2>&1; then
    sudo mkdir -p "$INSTALL_DIR"
    sudo install -m 0755 "$src" "${INSTALL_DIR}/${BINARY_NAME}"
  else
    INSTALL_DIR="${HOME}/.local/bin"
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$src" "${INSTALL_DIR}/${BINARY_NAME}"
    echo "==> Installed to ${INSTALL_DIR}/${BINARY_NAME}. Ensure ${INSTALL_DIR} is on your PATH:"
    echo "    export PATH=\"${INSTALL_DIR}:\$PATH\""
  fi
}

verify_sha256() {
  local file="$1"
  local checksums="$2"
  local asset="$3"
  local expected actual

  expected="$(awk -v name="$asset" '$2 == name || $2 == "*"name { print $1; exit }' "$checksums" | tr '[:upper:]' '[:lower:]')"
  if [ -z "$expected" ]; then
    echo "Error: checksum entry for '${asset}' not found in checksums.txt" >&2
    return 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
  else
    echo "Error: neither sha256sum nor shasum is available to verify download checksum" >&2
    return 1
  fi

  if [ "$expected" != "$actual" ]; then
    echo "Error: SHA256 mismatch for '${asset}' (expected ${expected}, got ${actual})" >&2
    return 1
  fi
  echo "==> Verified SHA256 checksum for ${asset}."
}

echo "==> Detected Platform: ${OS}/${ARCH}"

if [ -f "./bin/skipper-${OS}-${ARCH}" ]; then
  echo "==> Installing from local prebuilt binary (./bin/skipper-${OS}-${ARCH})..."
  install_binary "./bin/skipper-${OS}-${ARCH}"
elif [ -f "./bin/skipper" ]; then
  echo "==> Installing from local binary (./bin/skipper)..."
  install_binary "./bin/skipper"
elif [ -f "./cmd/skipper/main.go" ] && command -v go >/dev/null 2>&1; then
  echo "==> Building skipper from source for ${OS}/${ARCH}..."
  go build -o "${TMP_DIR}/${BINARY_NAME}" ./cmd/skipper
  install_binary "${TMP_DIR}/${BINARY_NAME}"
else
  if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl is required to download Skipper release binaries." >&2
    exit 1
  fi

  ASSET="skipper-${OS}-${ARCH}"
  if [ -n "$SKIPPER_VERSION" ]; then
    BASE_URL="https://github.com/${REPO}/releases/download/${SKIPPER_VERSION}"
  elif curl -fsSLI "https://github.com/${REPO}/releases/latest/download/${ASSET}" >/dev/null 2>&1; then
    BASE_URL="https://github.com/${REPO}/releases/latest/download"
  else
    LATEST_TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases" | grep -m1 '"tag_name":' | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/')"
    if [ -z "$LATEST_TAG" ]; then
      echo "Error: could not resolve a GitHub Release tag for ${REPO}." >&2
      exit 1
    fi
    BASE_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}"
  fi

  echo "==> Downloading ${ASSET} from ${BASE_URL}..."
  curl -fsSL "${BASE_URL}/${ASSET}" -o "${TMP_DIR}/${ASSET}"
  curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMP_DIR}/checksums.txt"
  verify_sha256 "${TMP_DIR}/${ASSET}" "${TMP_DIR}/checksums.txt" "${ASSET}"
  install_binary "${TMP_DIR}/${ASSET}"
fi

echo "==> Installation complete! Run 'skipper --help' to get started."
