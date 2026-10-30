#!/bin/sh
# Install own-code from a prebuilt binary: detect OS/arch, download the
# matching file from the release folders, and install it into $PREFIX/bin.
# Works on Linux, WSL2, Ubuntu, and macOS. No Go toolchain required.
set -eu

REPO_RAW="${REPO_RAW:-https://raw.githubusercontent.com/chinmay-sawant/own-code/master}"

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
  Linux) dir="linux-release" ;;
  Darwin) dir="macos-release" ;;
  *) echo "own-code: unsupported OS: $os (use Linux, WSL2/Ubuntu, or macOS)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  i386|i686) arch="386" ;;
  *) echo "own-code: unsupported architecture: $arch" >&2; exit 1 ;;
esac

case "$dir" in
  linux-release) osname="linux" ;;
  macos-release) osname="macos" ;;
esac
file="own-code-${osname}-${arch}"
url="$REPO_RAW/$dir/$file"

tmp=$(mktemp 2>/dev/null || mktemp -t own-code)
trap 'rm -f "$tmp"' EXIT INT TERM

if command -v curl >/dev/null 2>&1; then
  curl -fSL -o "$tmp" "$url"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$tmp" "$url"
else
  echo "own-code: need curl or wget to download $url" >&2
  exit 1
fi

cp "$tmp" "$bindir/own-code"
chmod +x "$bindir/own-code"

echo "Installed own-code ($file) to $bindir/own-code"
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "Add $bindir to your PATH, e.g. export PATH=\"$bindir:\$PATH\"" ;;
esac
