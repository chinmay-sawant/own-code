#!/bin/sh
# Build own-code from this checkout and install it into $PREFIX/bin.
# Unlike install.sh, which downloads a prebuilt binary from the release
# folders, this compiles the working tree (including uncommitted changes),
# so it needs Go and the ../ownframe and ../blinkless sibling checkouts.
# Works on Linux, WSL2, and macOS.
set -eu

usage() {
	cat <<'EOF'
Usage: ./install-local.sh [options]

Build own-code from this checkout and install it to $PREFIX/bin/own-code.

Options:
  -p, --prefix DIR   install prefix (default: $HOME/.local)
  -b, --binary FILE  install FILE as-is instead of building from source
  -h, --help         show this help

Environment:
  PREFIX             same as --prefix
EOF
}

prefix="${PREFIX:-}"
binary=""
while [ $# -gt 0 ]; do
	case "$1" in
		-p|--prefix)
			[ $# -ge 2 ] || { echo "own-code: $1 needs a directory" >&2; exit 2; }
			prefix=$2
			shift 2
			;;
		--prefix=*)
			prefix=${1#--prefix=}
			shift
			;;
		-b|--binary)
			[ $# -ge 2 ] || { echo "own-code: $1 needs a file" >&2; exit 2; }
			binary=$2
			shift 2
			;;
		--binary=*)
			binary=${1#--binary=}
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "own-code: unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

if [ -z "$prefix" ]; then
	if [ -z "${HOME:-}" ]; then
		echo "own-code: HOME is unset; use --prefix to choose an install prefix" >&2
		exit 1
	fi
	prefix="$HOME/.local"
fi
bindir="$prefix/bin"
output="$bindir/own-code"

if [ -n "$binary" ]; then
	case "$binary" in
		/*) ;;
		*) binary=$(pwd)/$binary ;;
	esac
	if [ ! -f "$binary" ]; then
		echo "own-code: binary not found: $binary" >&2
		exit 1
	fi
else
	repo=$(cd "$(dirname "$0")" && pwd)
	cd "$repo"

	if [ ! -f go.mod ] || [ ! -d cmd/own-code ]; then
		echo "own-code: run this script from a full own-code checkout" >&2
		exit 1
	fi

	if ! command -v go >/dev/null 2>&1; then
		echo "own-code: Go is not installed; install Go 1.26+ or use ./install.sh" >&2
		exit 1
	fi

	for dep in ../ownframe ../blinkless; do
		if [ ! -d "$dep" ]; then
			echo "own-code: missing $dep; go.mod and go.work need the sibling checkouts" >&2
			exit 1
		fi
	done

	os=$(uname -s)
	case "$os" in
		Linux|Darwin) ;;
		*) echo "own-code: unsupported OS: $os (use Linux, WSL2, or macOS)" >&2; exit 1 ;;
	esac
fi

mkdir -p "$bindir"
tmp="$bindir/.own-code.$$"
trap 'rm -f "$tmp"' EXIT INT TERM

if [ -n "$binary" ]; then
	echo "Installing $binary ..."
	cp "$binary" "$tmp"
else
	echo "Building own-code from $repo ..."
	go build -o "$tmp" ./cmd/own-code
fi

chmod +x "$tmp"
mv -f "$tmp" "$output"
trap - EXIT INT TERM

echo "Installed own-code to $output"
case ":$PATH:" in
	*":$bindir:"*) ;;
	*) echo "Add $bindir to your PATH, e.g. export PATH=\"$bindir:\$PATH\"" ;;
esac
