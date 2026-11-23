#!/usr/bin/env bash
# Check out the sibling repos that go.mod (replace => ../ownframe) and go.work
# (use ../ownframe ../blinkless) expect, at the versions go.mod records, so CI
# never drifts from go.mod. Run from the own-code checkout root.
set -euo pipefail

parent="$(cd .. && pwd)"

# ownframe: the tag in the `require` line, e.g. v0.0.5.
of_ver="$(awk '$1 == "github.com/chinmay-sawant/ownframe" && $2 ~ /^v/ {print $2; exit}' go.mod)"
# blinkless: the 12-char commit of the pseudo-version, e.g. ...-5dea7a1f2019.
bl_ver="$(awk '$1 == "github.com/chinmay-sawant/blinkless" && $2 ~ /^v/ {print $2; exit}' go.mod)"

fetch() { # name ref
  local name="$1" ref="$2" dir="$parent/$1"
  [ -d "$dir/.git" ] || git init -q "$dir"
  git -C "$dir" remote remove origin 2>/dev/null || true
  git -C "$dir" remote add origin "https://github.com/chinmay-sawant/$name.git"
  if ! git -C "$dir" fetch -q --depth 1 origin "$ref"; then
    # Short SHAs cannot be fetched directly; fall back to a full fetch.
    git -C "$dir" fetch -q origin
  fi
  git -C "$dir" checkout -q -f "$ref" 2>/dev/null || git -C "$dir" checkout -q -f FETCH_HEAD
  echo "$name at $(git -C "$dir" rev-parse --short HEAD) (wanted $ref)"
}

[ -n "$of_ver" ] || { echo "ownframe version not found in go.mod" >&2; exit 1; }
fetch ownframe "$of_ver"

if [ -n "$bl_ver" ]; then
  fetch blinkless "${bl_ver##*-}"
else
  echo "no blinkless requirement in go.mod; skipping"
fi
