#!/usr/bin/env bash
#
# Fresh-machine install check. The Dockerfile installs `ao` on PATH in a clean
# image and runs this; it proves a freshly installed binary actually works on a
# machine with no Go toolchain and no developer state. The COMPREHENSIVE,
# cross-platform behavioural suite lives in Go (backend/internal/cli/e2e_test.go,
# `go test -tags e2e`); this stays deliberately small and linear.

set -euo pipefail

AO_BIN="${AO_BIN:-ao}"
tmp="$(mktemp -d)"
export AO_RUN_FILE="$tmp/running.json"
export AO_DATA_DIR="$tmp/data"
export AO_PORT="${AO_PORT:-3001}"   # the container is isolated; 3001 is free
trap '"$AO_BIN" stop >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

echo "ao binary : $(command -v "$AO_BIN")"
"$AO_BIN" version            >/dev/null || fail "version"
"$AO_BIN" doctor             >/dev/null || fail "doctor"
"$AO_BIN" start              >/dev/null || fail "start"

"$AO_BIN" status --json | grep -q '"state": "ready"' || fail "daemon not ready after start"

# the /shutdown control endpoint rejects a cross-origin caller (403) and survives
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
         -H 'Origin: https://evil.example' "http://127.0.0.1:$AO_PORT/shutdown")"
[ "$code" = "403" ] || fail "cross-origin /shutdown returned $code, want 403"
"$AO_BIN" status --json | grep -q '"state": "ready"' || fail "daemon died after rejected shutdown"

"$AO_BIN" stop >/dev/null || fail "stop"
"$AO_BIN" status --json | grep -q '"state": "stopped"' || fail "daemon not stopped"

echo "fresh-install check: OK"
