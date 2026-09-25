#!/usr/bin/env bash
# Install the acline CLI from GitHub releases.
#
#   curl -fsSL https://acline.nizaami.com/install.sh | bash
#
# Env overrides:
#   ACLINE_VERSION      release tag to install, e.g. v0.2.0 (default: latest)
#   ACLINE_INSTALL_DIR  where the binary goes (default: $HOME/.local/bin)
#   ACLINE_BACKUP       1 keeps the old binary as acline.bak-<timestamp> (default: 1)
set -euo pipefail

REPO="ows4444/cli-acline"
BIN_NAME="acline"
INSTALL_DIR="${ACLINE_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${ACLINE_VERSION:-latest}"
BACKUP="${ACLINE_BACKUP:-1}"

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found on PATH"; }
need curl
need tar
need mktemp

os="$(uname -s)"
case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) die "unsupported OS: $os (Windows isn't supported by this script; use WSL, or download a release archive manually)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

api_url="https://api.github.com/repos/$REPO/releases"
if [ "$VERSION" = "latest" ]; then
  api_url="$api_url/latest"
else
  api_url="$api_url/tags/$VERSION"
fi

log "resolving release ($VERSION)..."
release_json="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$api_url")" \
  || die "couldn't fetch release metadata from $api_url (no published release yet?)"

tag="$(printf '%s' "$release_json" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
[ -n "$tag" ] || die "couldn't determine release tag from GitHub API response"
ver="${tag#v}"

archive="${BIN_NAME}_${goos}_${goarch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$tag"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

log "downloading $archive ($tag)..."
curl -fsSL -o "$tmpdir/$archive" "$base_url/$archive" \
  || die "couldn't download $base_url/$archive"
curl -fsSL -o "$tmpdir/checksums.txt" "$base_url/checksums.txt" \
  || die "couldn't download checksums.txt for $tag"

log "verifying checksum..."
( cd "$tmpdir" && grep " $archive\$" checksums.txt | shasum -a 256 -c - ) \
  || die "checksum verification failed for $archive"

log "extracting..."
tar -xzf "$tmpdir/$archive" -C "$tmpdir"
[ -x "$tmpdir/$BIN_NAME" ] || die "archive didn't contain an executable named $BIN_NAME"

mkdir -p "$INSTALL_DIR"
if [ "$BACKUP" = "1" ] && [ -e "$INSTALL_DIR/$BIN_NAME" ]; then
  stamp="$(date +%Y%m%d-%H%M%S)"
  cp -p "$INSTALL_DIR/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME.bak-$stamp"
  log "kept the previous binary as $INSTALL_DIR/$BIN_NAME.bak-$stamp"
fi

install -m 0755 "$tmpdir/$BIN_NAME" "$INSTALL_DIR/.$BIN_NAME.new"
mv -f "$INSTALL_DIR/.$BIN_NAME.new" "$INSTALL_DIR/$BIN_NAME"

log "installed $BIN_NAME $ver to $INSTALL_DIR/$BIN_NAME"
"$INSTALL_DIR/$BIN_NAME" version >&2 || true

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) log "warning: $INSTALL_DIR is not on your PATH" ;;
esac
