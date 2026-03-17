#!/usr/bin/env bash
# e2e-serve.sh — manual validation of boxy serve (API + UI).
#
# Builds the binary, starts the server, runs curl assertions against every
# endpoint, then repeats with --ui=false to verify the toggle works.
#
# Usage:
#   bash scripts/e2e-serve.sh                       # default config
#   bash scripts/e2e-serve.sh examples/docker-pool-basic/boxy.yaml

set -euo pipefail

CONFIG="${1:-examples/docker-pool-basic/boxy.yaml}"
LISTEN=":19090"          # non-standard port to avoid conflicts
BASE="http://localhost:19090"
BINARY="./boxy"
PID=""

pass=0
fail=0

# ── helpers ──────────────────────────────────────────────────────────────

cleanup() {
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

start_server() {
  local extra_flags="${1:-}"
  "$BINARY" serve --config "$CONFIG" --listen "$LISTEN" $extra_flags &
  PID=$!

  # Wait for the server to be ready (up to 5s).
  for i in $(seq 1 50); do
    if curl -sf "$BASE/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "FAIL: server did not become ready"
  exit 1
}

stop_server() {
  if [[ -n "$PID" ]]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    PID=""
  fi
}

assert_status() {
  local label="$1" url="$2" expect="$3"
  local got
  got=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>&1)
  if [[ "$got" == "$expect" ]]; then
    echo "  PASS  $label → $got"
    pass=$((pass + 1))
  else
    echo "  FAIL  $label → $got (expected $expect)"
    fail=$((fail + 1))
  fi
}

assert_json_array() {
  local label="$1" url="$2"
  local body ct code
  body=$(curl -s "$url")
  code=$(curl -s -o /dev/null -w "%{http_code}" "$url")
  ct=$(curl -s -o /dev/null -w "%{content_type}" "$url")

  local ok=true
  [[ "$code" == "200" ]] || ok=false
  [[ "$ct" == *"application/json"* ]] || ok=false
  # Body should start with [ (JSON array).
  [[ "$body" == "["* ]] || ok=false

  if $ok; then
    echo "  PASS  $label → 200, json array"
    pass=$((pass + 1))
  else
    echo "  FAIL  $label → code=$code ct=$ct body=${body:0:40}"
    fail=$((fail + 1))
  fi
}

assert_html_contains() {
  local label="$1" url="$2" needle="$3"
  local body code
  body=$(curl -s "$url")
  code=$(curl -s -o /dev/null -w "%{http_code}" "$url")

  if [[ "$code" == "200" ]] && echo "$body" | grep -q "$needle"; then
    echo "  PASS  $label → 200, contains '$needle'"
    pass=$((pass + 1))
  else
    echo "  FAIL  $label → code=$code, missing '$needle'"
    fail=$((fail + 1))
  fi
}

# ── build ────────────────────────────────────────────────────────────────

echo "Building..."
go build -o "$BINARY" ./cmd/boxy

# ── test 1: UI enabled (default) ────────────────────────────────────────

echo ""
echo "=== UI enabled (default) ==="
start_server

assert_status   "GET /healthz"            "$BASE/healthz"            200
assert_json_array "GET /api/v1/pools"     "$BASE/api/v1/pools"
assert_json_array "GET /api/v1/resources" "$BASE/api/v1/resources"
assert_json_array "GET /api/v1/sandboxes" "$BASE/api/v1/sandboxes"

assert_html_contains "GET /"              "$BASE/"                   "Overview"
assert_html_contains "GET /ui/pools"      "$BASE/ui/pools"           "All Pools"
assert_html_contains "GET /ui/sandboxes"  "$BASE/ui/sandboxes"       "All Sandboxes"

assert_html_contains "fragment: stats"          "$BASE/ui/fragments/stats"           "stat-card"
assert_html_contains "fragment: pools-table"    "$BASE/ui/fragments/pools-table"     "No pools configured"
assert_html_contains "fragment: sandboxes-table" "$BASE/ui/fragments/sandboxes-table" "No sandboxes created"

assert_status   "GET /static/style.css"   "$BASE/static/style.css"   200
assert_status   "GET /static/htmx.min.js" "$BASE/static/htmx.min.js" 200

stop_server

# ── test 2: UI disabled (--ui=false) ────────────────────────────────────

echo ""
echo "=== UI disabled (--ui=false) ==="
start_server "--ui=false"

assert_status   "GET /healthz"            "$BASE/healthz"            200
assert_json_array "GET /api/v1/pools"     "$BASE/api/v1/pools"
assert_json_array "GET /api/v1/sandboxes" "$BASE/api/v1/sandboxes"

assert_status   "GET / (expect 404)"            "$BASE/"                   404
assert_status   "GET /ui/pools (expect 404)"    "$BASE/ui/pools"           404
assert_status   "GET /static/style.css (expect 404)" "$BASE/static/style.css" 404

stop_server

# ── summary ──────────────────────────────────────────────────────────────

echo ""
echo "=== Results: $pass passed, $fail failed ==="
if [[ "$fail" -gt 0 ]]; then
  exit 1
fi
