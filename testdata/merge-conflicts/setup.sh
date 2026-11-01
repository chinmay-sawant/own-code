#!/bin/sh
# Builds testdata/merge-conflicts/demo and leaves it mid-merge.
# Rerunning resets the demo. Pass a directory as $1 to build elsewhere.
set -eu

TARGET="${1:-demo}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEMO="$SCRIPT_DIR/$TARGET"

case "$TARGET" in
  /*) DEMO="$TARGET" ;;
esac

rm -rf "$DEMO"
mkdir -p "$DEMO/config" "$DEMO/backend"

git -C "$DEMO" init -q -b main
git -C "$DEMO" config user.name "Code View Test"
git -C "$DEMO" config user.email "code-view@example.com"
git -C "$DEMO" config merge.conflictStyle diff3

cat > "$DEMO/config/services.yaml" <<'EOF'
# Beacon service catalog. Durations use Go syntax: 30s, 750ms, 4s.
interval: 30s
timeout: 4s
budget: 750ms

services:
  - name: checkout-api
    url: https://api.example.com/health
  - name: dashboard
    url: https://app.example.com/
EOF

cat > "$DEMO/backend/scheduler.go" <<'EOF'
package main

import "time"

// Round settings.
const workers = 4
const backoff = 150 * time.Millisecond
EOF

cat > "$DEMO/README.md" <<'EOF'
# Merge demo

Built by testdata/merge-conflicts/setup.sh. This repo sits mid-merge.
Open it with: go run ./cmd/own-code -root testdata/merge-conflicts/demo
EOF

git -C "$DEMO" add -A
git -C "$DEMO" commit -q -m "base"

git -C "$DEMO" checkout -q -b current
cat > "$DEMO/config/services.yaml" <<'EOF'
# Beacon service catalog. Durations use Go syntax: 30s, 750ms, 4s.
interval: 15s
timeout: 4s
budget: 750ms

services:
  - name: checkout-api
    url: https://api.example.com/health
  - name: dashboard
    url: https://app.example.com/
  - name: preview
    url: https://preview.example.com/
EOF

cat > "$DEMO/backend/scheduler.go" <<'EOF'
package main

import "time"

// Round settings, tuned for preview.
const workers = 2
const backoff = 100 * time.Millisecond
EOF

git -C "$DEMO" add -A
git -C "$DEMO" commit -q -m "current side"

git -C "$DEMO" checkout -q main
git -C "$DEMO" checkout -q -b incoming
cat > "$DEMO/config/services.yaml" <<'EOF'
# Beacon service catalog. Durations use Go syntax: 30s, 750ms, 4s.
interval: 60s
timeout: 4s
budget: 750ms

services:
  - name: checkout-api
    url: https://api.example.com/health
  - name: dashboard
    url: https://app.example.com/
  - name: analytics
    url: https://analytics.example.com/
EOF

cat > "$DEMO/backend/scheduler.go" <<'EOF'
package main

import "time"

// Round settings, tuned for analytics.
const workers = 8
const backoff = 300 * time.Millisecond
EOF

git -C "$DEMO" add -A
git -C "$DEMO" commit -q -m "incoming side"

git -C "$DEMO" checkout -q current
if git -C "$DEMO" merge --no-edit incoming > /dev/null 2>&1; then
  echo "setup: merge came out clean, expected a conflict" >&2
  exit 1
fi

if [ -z "$(git -C "$DEMO" ls-files -u)" ]; then
  echo "setup: no unmerged paths after merge" >&2
  exit 1
fi

echo "demo ready at $DEMO"
git -C "$DEMO" status --short
echo "open with: go run ./cmd/own-code -root testdata/merge-conflicts/$TARGET"
