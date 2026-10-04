#!/bin/sh
set -eu

REPO="blesswinsamuel/devyard"

main() {
  # 1. Detect OS
  OS="$(uname -s)"
  case "$OS" in
    Darwin) OS="darwin" ;;
    Linux) OS="linux" ;;
    *)
      echo "Error: devyard currently only supports macOS and Linux." >&2
      echo "For Windows, please run devyard inside WSL2 (Windows Subsystem for Linux)." >&2
      exit 1
      ;;
  esac

  # 2. Detect Architecture
  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
      echo "Error: Unsupported architecture: $ARCH" >&2
      exit 1
      ;;
  esac

  # 3. Detect Downloader
  if command -v curl >/dev/null 2>&1; then
    HTTP_CLIENT="curl"
  elif command -v wget >/dev/null 2>&1; then
    HTTP_CLIENT="wget"
  else
    echo "Error: curl or wget is required to install devyard." >&2
    exit 1
  fi

  download() {
    url="$1"
    dest="$2"
    if [ "$HTTP_CLIENT" = "curl" ]; then
      curl -fsSL "$url" -o "$dest"
    else
      wget -qO "$dest" "$url"
    fi
  }

  get_redirect_tag() {
    if [ "$HTTP_CLIENT" = "curl" ]; then
      curl -fssIL "https://github.com/$REPO/releases/latest" 2>/dev/null | tr -d '\r' | grep -i '^location:' | tail -n 1 | sed -E 's/.*tag\/(.*)/\1/'
    else
      wget --server-response --spider "https://github.com/$REPO/releases/latest" 2>&1 | tr -d '\r' | grep -i 'Location:' | tail -n 1 | sed -E 's/.*tag\/(.*)/\1/'
    fi
  }

  # 4. Determine Version
  TAG="${DEVYARD_VERSION:-}"
  if [ -z "$TAG" ]; then
    echo "Finding latest release of $REPO..."
    TAG=""
    if [ "$HTTP_CLIENT" = "curl" ]; then
      TAG="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name":' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' || true)"
    else
      TAG="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name":' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' || true)"
    fi

    if [ -z "$TAG" ]; then
      TAG="$(get_redirect_tag || true)"
    fi

    if [ -z "$TAG" ]; then
      echo "Error: Could not determine latest release version from GitHub." >&2
      exit 1
    fi
  fi

  # Strip leading 'v' for the filename (GoReleaser standard)
  VERSION="${TAG#v}"

  ARCHIVE="devyard_${VERSION}_${OS}_${ARCH}.tar.gz"
  DOWNLOAD_URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE"
  CHECKSUM_URL="https://github.com/$REPO/releases/download/$TAG/checksums.txt"

  # 5. Temporary workspace
  TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'devyard-install')"
  cleanup() {
    rm -rf "$TMP_DIR"
  }
  trap cleanup EXIT INT TERM

  echo "Downloading devyard $TAG ($OS/$ARCH)..."
  download "$DOWNLOAD_URL" "$TMP_DIR/$ARCHIVE"
  download "$CHECKSUM_URL" "$TMP_DIR/checksums.txt"

  # 6. Verify SHA256 checksum
  echo "Verifying checksum..."
  cd "$TMP_DIR"
  EXPECTED_SHA="$(grep "$ARCHIVE" checksums.txt | awk '{print $1}')"
  if [ -z "$EXPECTED_SHA" ]; then
    echo "Warning: Checksum for $ARCHIVE not found in checksums.txt, skipping verification." >&2
  else
    if command -v sha256sum >/dev/null 2>&1; then
      ACTUAL_SHA="$(sha256sum "$ARCHIVE" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      ACTUAL_SHA="$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')"
    else
      ACTUAL_SHA=""
    fi

    if [ -n "$ACTUAL_SHA" ]; then
      if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
        echo "Error: Checksum mismatch for $ARCHIVE" >&2
        echo "Expected: $EXPECTED_SHA" >&2
        echo "Actual:   $ACTUAL_SHA" >&2
        exit 1
      fi
      echo "Checksum verified."
    fi
  fi

  # 7. Unpack
  tar -xzf "$ARCHIVE"

  # 8. Determine destination directory
  if [ -n "${DEVYARD_INSTALL_DIR:-}" ]; then
    DEST_DIR="$DEVYARD_INSTALL_DIR"
  elif [ -w "/usr/local/bin" ] || [ "$(id -u)" -eq 0 ]; then
    DEST_DIR="/usr/local/bin"
  else
    DEST_DIR="$HOME/.local/bin"
  fi

  mkdir -p "$DEST_DIR"
  cp "$TMP_DIR/devyard" "$DEST_DIR/devyard"
  chmod +x "$DEST_DIR/devyard"

  echo ""
  echo "Successfully installed devyard to $DEST_DIR/devyard"

  # Check PATH
  case ":$PATH:" in
    *":$DEST_DIR:"*) ;;
    *)
      echo ""
      echo "Note: $DEST_DIR is not in your PATH."
      echo "To add it, append the following line to your shell configuration file (~/.bashrc, ~/.zshrc, etc.):"
      echo "  export PATH=\"\$PATH:$DEST_DIR\""
      ;;
  esac

  echo ""
  "$DEST_DIR/devyard" version || true
}

main "$@"
