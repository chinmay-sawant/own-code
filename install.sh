#!/bin/sh
# Install own-code from source: shallow-clone, build with the vendor tree,
# and install the binary as $PREFIX/bin/own-code (default $HOME/.local/bin).
set -eu

REPO="https://github.com/chinmay-sawant/own-code.git"
BRANCH="master"

if ! command -v git >/dev/null 2>&1; then
  echo "own-code: git not found; install git first" >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "own-code: go not found; install Go >= 1.26 first" >&2
  exit 1
fi

ver=$(go version | sed -n 's/.* go\([0-9]*\)\.\([0-9]*\).*/\1 \2/p')
if [ -z "$ver" ]; then
  echo "own-code: could not parse go version: $(go version)" >&2
  exit 1
fi
# shellcheck disable=SC2086
set -- $ver
major=$1
minor=$2
ok=0
if [ "$major" -gt 1 ]; then
  ok=1
elif [ "$major" -eq 1 ] && [ "$minor" -ge 26 ]; then
  ok=1
fi
if [ "$ok" -ne 1 ]; then
  echo "own-code: need go >= 1.26, found: $(go version)" >&2
  exit 1
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

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t own-code)
trap 'rm -rf "$tmp"' EXIT INT TERM

git clone --depth 1 --branch "$BRANCH" "$REPO" "$tmp/src"
cd "$tmp/src"
go build -mod=vendor -o own-code ./cmd/own-code
cp "$tmp/src/own-code" "$bindir/own-code"
chmod +x "$bindir/own-code"

echo "Installed own-code to $bindir/own-code"
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "Add $bindir to your PATH, e.g. export PATH=\"$bindir:\$PATH\"" ;;
esac
