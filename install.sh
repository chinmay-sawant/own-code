#!/bin/sh
# Install own-code from a prebuilt binary: detect OS/arch, download the
# matching file from the latest GitHub Release, verify its SHA-256 against the
# release's SHA256SUMS, and install it into $PREFIX/bin.
# Works on Linux, WSL2, Ubuntu, and macOS. No Go toolchain required.
set -eu

# Set OWN_CODE_VERSION (e.g. v0.0.1) to pin a release instead of "latest".
if [ -n "${OWN_CODE_VERSION:-}" ]; then
  REL_URL="https://github.com/chinmay-sawant/own-code/releases/download/$OWN_CODE_VERSION"
else
  REL_URL="${REL_URL:-https://github.com/chinmay-sawant/own-code/releases/latest/download}"
fi

if [ -z "${PREFIX:-}" ]; then
  if [ -z "${HOME:-}" ]; then
    echo "own-code: HOME is unset; set PREFIX to choose an install prefix" >&2
    exit 1
  fi
  PREFIX="$HOME/.local"
fi
bindir="$PREFIX/bin"
mkdir -p "$bindir"

os=$(uname -s)
case "$os" in
  Linux) osname="linux" ;;
  Darwin) osname="macos" ;;
  *) echo "own-code: unsupported OS: $os (use Linux, WSL2/Ubuntu, or macOS)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  i386|i686) echo "own-code: 32-bit x86 releases are not published; build from source" >&2; exit 1 ;;
  *) echo "own-code: unsupported architecture: $arch" >&2; exit 1 ;;
esac

file="own-code-${osname}-${arch}"
url="$REL_URL/$file"

tmp=$(mktemp "$bindir/.own-code.XXXXXX")
sums=$(mktemp 2>/dev/null || mktemp -t own-code-sums)
trap 'rm -f "$tmp" "$sums"' EXIT INT TERM

fetch() { # fetch URL DEST
  if command -v curl >/dev/null 2>&1; then
    curl -fSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$2" "$1"
  else
    echo "own-code: need curl or wget to download $1" >&2
    exit 1
  fi
}

fetch "$url" "$tmp"
fetch "$REL_URL/SHA256SUMS" "$sums"

want=$(awk -v f="$file" '$2==f || $2=="*"f {print $1}' "$sums")
if [ -z "$want" ]; then
  echo "own-code: no checksum for $file in SHA256SUMS" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp" | awk '{print $1}')
else
  echo "own-code: need sha256sum or shasum to verify the download" >&2
  exit 1
fi
if [ "$got" != "$want" ]; then
  echo "own-code: checksum mismatch for $file (expected $want, got $got)" >&2
  exit 1
fi

chmod +x "$tmp"
mv -f "$tmp" "$bindir/own-code"

echo "Installed own-code ($file) to $bindir/own-code"
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "Add $bindir to your PATH, e.g. export PATH=\"$bindir:\$PATH\"" ;;
esac
