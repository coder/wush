#!/usr/bin/env sh

set -eu

GITHUB_REPO="coder/wush"
BINARY_NAME="wush"
INSTALL_DIR="/usr/local/bin"

# Function to determine the platform
detect_platform() {
  OS=$(uname -s)
  ARCH=$(uname -m)

  case $OS in
    Linux)
      PLATFORM="linux"
      ;;
    Darwin)
      PLATFORM="darwin"
      ;;
    FreeBSD)
      PLATFORM="freebsd"
      ;;
    CYGWIN*|MINGW*|MSYS*)
      PLATFORM="windows"
      ;;
    *)
      echo "Unsupported OS: $OS"
      exit 1
      ;;
  esac

  case $ARCH in
    x86_64|amd64)
      ARCH="amd64"
      ;;
    i386|i686)
      ARCH="386"
      ;;
    armv7l|armv6l)
      ARCH="armv7"
      ;;
    aarch64|arm64)
      ARCH="arm64"
      ;;
    *)
      echo "Unsupported architecture: $ARCH"
      exit 1
      ;;
  esac

  echo "${PLATFORM}_${ARCH}"
}

# Function to determine the preferred archive format
select_archive_format() {
  PLATFORM_ARCH=$1

  case "$PLATFORM_ARCH" in
    windows-*)
      echo "zip"
      ;;
    *)
      if command -v tar >/dev/null 2>&1; then
        echo "tar.gz"
      elif command -v unzip >/dev/null 2>&1; then
        echo "zip"
      else
        echo "Unsupported: neither tar nor unzip are available."
        exit 1
      fi
      ;;
  esac
}

main() {
  PLATFORM_ARCH=$(detect_platform)

  # Determine preferred archive format
  FILE_EXT=$(select_archive_format "$PLATFORM_ARCH")

  # Get the latest release download URL from GitHub API
  LATEST_RELEASE_URL=$(curl -fsSL \
    "https://api.github.com/repos/$GITHUB_REPO/releases/latest" \
        | grep "browser_download_url" \
        | grep "$PLATFORM_ARCH.$FILE_EXT" \
        | cut -d '"' -f 4 | head -n 1)

  if [ -z "$LATEST_RELEASE_URL" ]; then
    echo "No release found for platform $PLATFORM_ARCH with format $FILE_EXT"
    exit 1
  fi

  # Download the release archive
  TMP_DIR=$(mktemp -d)
  ARCHIVE_PATH="$TMP_DIR/$BINARY_NAME.$FILE_EXT"

  echo "Downloading $BINARY_NAME from $LATEST_RELEASE_URL..."
  curl -L -o "$ARCHIVE_PATH" "$LATEST_RELEASE_URL"


  # Extract the archive
  echo "Extracting $BINARY_NAME..."
  if [ "$FILE_EXT" = "zip" ]; then
    unzip -d "$TMP_DIR" "$ARCHIVE_PATH"
  elif [ "$FILE_EXT" = "tar.gz" ]; then
    tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"
  else
    echo "Unsupported file extension: $FILE_EXT"
    exit 1
  fi

  # Find the binary (assuming it's in the extracted files)
  BINARY_PATH=$(find "$TMP_DIR" -type f -name "$BINARY_NAME")

  # Make the binary executable
  chmod +x "$BINARY_PATH"

  # Install the binary
  if [ "$PLATFORM_ARCH" = "windows-amd64" ] || [ "$PLATFORM_ARCH" = "windows-386" ]; then
    INSTALL_DIR="$HOME/bin"
    mkdir -p "$INSTALL_DIR"
    mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY_NAME.exe"
  else
    # Run using sudo if not root
    if [ "$(id -u)" -ne 0 ]; then
      sudo sh <<EOF
        [ "$(uname -s)" = "Linux" ] && command -v setcap >/dev/null 2>&1 && setcap cap_net_admin=eip "$BINARY_PATH"
        mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY_NAME"
EOF
    else
        [ "$(uname -s)" = "Linux" ] && command -v setcap >/dev/null 2>&1 && setcap cap_net_admin=eip "$BINARY_PATH"
        mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY_NAME"
    fi
  fi

  # Clean up
  rm -rf "$TMP_DIR"

  echo "$BINARY_NAME installed successfully!"
}

# Run the installation
main
