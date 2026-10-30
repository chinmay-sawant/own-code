#!/bin/sh
# Install own-code from source: shallow-clone own-code and the ownframe
# checkout it builds against, then build the binary into $PREFIX/bin.
# A sibling checkout is required because go.mod points at ../ownframe and
# the upstream module zip at the pinned commit is too large to download.
set -eu

REPO="https://github.com/chinmay-sawant/own-code.git"
OWNFRAME_REPO="https://github.com/chinmay-sawant/ownframe.git"
BRANCH="master"
# Master commit pinned in go.mod (pseudo-version
# v0.0.3-0.20261030080535-f2f079065cfa). Keep in sync with go.mod.
OWNFRAME_PIN="f2f079065cfa8e63a6a572fca9a4403e5f975e16"

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

git clone --depth 1 --branch "$BRANCH" "$REPO" "$tmp/own-code"
git clone --depth 1 --branch master "$OWNFRAME_REPO" "$tmp/ownframe"
if [ "$(git -C "$tmp/ownframe" rev-parse HEAD)" != "$OWNFRAME_PIN" ]; then
  git -C "$tmp/ownframe" fetch --depth 1 origin "$OWNFRAME_PIN"
  git -C "$tmp/ownframe" checkout "$OWNFRAME_PIN"
fi
cd "$tmp/own-code"
go build -o own-code ./cmd/own-code
cp "$tmp/own-code/own-code" "$bindir/own-code"
chmod +x "$bindir/own-code"

echo "Installed own-code to $bindir/own-code"
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "Add $bindir to your PATH, e.g. export PATH=\"$bindir:\$PATH\"" ;;
esac
