#!/usr/bin/env bash
# e2e-serve.sh — manual validation of boxy serve (API + UI).
#
# Builds the binary, starts the server, runs curl assertions against every
# endpoint, then repeats with --ui=false to verify the toggle works.
#
# Usage:
#   bash scripts/e2e-serve.sh                       # default config
#   bash scripts/e2e-serve.sh examples/devfactory-containers/boxy.yaml

set -euo pipefail

SOURCE_CONFIG="${1:-examples/devfactory-containers/boxy.yaml}"
LISTEN=":19090"          # non-standard port to avoid conflicts
BASE="http://localhost:19090"
BINARY="./boxy"
PID=""
TMP_DIR=""
CONFIG=""
COOKIE_JAR=""
API_KEY=""
CURL_ARGS=()

pass=0
fail=0

# ── helpers ──────────────────────────────────────────────────────────────

cleanup() {
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  if [[ -n "$TMP_DIR" ]]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

start_server() {
  local extra_flags=("$@")
  "$BINARY" serve --config "$CONFIG" --listen "$LISTEN" --insecure "${extra_flags[@]}" &
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
  got=$(curl -s -o /dev/null -w "%{http_code}" "${CURL_ARGS[@]}" "$url" 2>&1)
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
  body=$(curl -s "${CURL_ARGS[@]}" "$url")
  code=$(curl -s -o /dev/null -w "%{http_code}" "${CURL_ARGS[@]}" "$url")
  ct=$(curl -s -o /dev/null -w "%{content_type}" "${CURL_ARGS[@]}" "$url")

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
  body=$(curl -s "${CURL_ARGS[@]}" "$url")
  code=$(curl -s -o /dev/null -w "%{http_code}" "${CURL_ARGS[@]}" "$url")

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
SOURCE_CONFIG="$(realpath "$SOURCE_CONFIG")"
TMP_DIR="$(mktemp -d)"
CONFIG="$TMP_DIR/boxy.yaml"
COOKIE_JAR="$TMP_DIR/cookies.txt"
cp "$SOURCE_CONFIG" "$CONFIG"

# ── test 1: UI enabled (default) ────────────────────────────────────────

echo ""
echo "=== UI enabled (default) ==="
start_server

bootstrap_response=$(curl -sS -X POST "$BASE/api/v1/api-keys/bootstrap")
API_KEY=$(printf '%s' "$bootstrap_response" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
if [[ -z "$API_KEY" ]]; then
  echo "FAIL: local API-key bootstrap did not return a key"
  exit 1
fi

password_response=$("$BINARY" admin bootstrap-password --config "$CONFIG")
admin_password=$(printf '%s\n' "$password_response" | sed -n 's/^password: //p')
if [[ -z "$admin_password" ]]; then
  echo "FAIL: local admin bootstrap did not return a password"
  exit 1
fi
login_code=$(curl -sS -o /dev/null -w "%{http_code}" -c "$COOKIE_JAR" \
  -X POST --data-urlencode "username=admin" --data-urlencode "password=$admin_password" \
  --data-urlencode "next=/" "$BASE/login")
if [[ "$login_code" != "302" ]]; then
  echo "FAIL: local admin login returned $login_code (expected 302)"
  exit 1
fi
CURL_ARGS=(-H "Authorization: Bearer $API_KEY" -b "$COOKIE_JAR")

assert_status   "GET /healthz"            "$BASE/healthz"            200
assert_json_array "GET /api/v1/pools"     "$BASE/api/v1/pools"
assert_json_array "GET /api/v1/resources" "$BASE/api/v1/resources"
assert_json_array "GET /api/v1/sandboxes" "$BASE/api/v1/sandboxes"
assert_status "GET /api/v1/diagnostics/logs" "$BASE/api/v1/diagnostics/logs?limit=2" 200

assert_html_contains "GET /"              "$BASE/"                   "Overview"
assert_html_contains "GET /ui/pools"      "$BASE/ui/pools"           "All Pools"
assert_html_contains "GET /ui/sandboxes"  "$BASE/ui/sandboxes"       "All Sandboxes"
assert_html_contains "GET /ui/agents"     "$BASE/ui/agents"           "Agents"
assert_html_contains "GET /ui/diagnostics" "$BASE/ui/diagnostics"     "Diagnostics"
assert_html_contains "GET /ui/catalog"   "$BASE/ui/catalog"           "Catalog"
assert_html_contains "GET /ui/help"      "$BASE/ui/help"              "Boxy package help"

assert_html_contains "fragment: stats"          "$BASE/ui/fragments/stats"           "stat-card"
assert_status "fragment: pools-table"    "$BASE/ui/fragments/pools-table"     200
assert_status "fragment: sandboxes-table" "$BASE/ui/fragments/sandboxes-table" 200
assert_status "fragment: agents-table"   "$BASE/ui/fragments/agents-table"    200

assert_status   "GET /static/style.css"   "$BASE/static/style.css"   200
assert_status   "GET /static/htmx.min.js" "$BASE/static/htmx.min.js" 200

if command -v hurl >/dev/null 2>&1; then
  hurl --test --variable base_url="$BASE" --variable api_key="$API_KEY" tests/e2e/serve-api.hurl
else
  echo "  SKIP  Hurl contract tests (hurl is not installed in this environment)"
fi

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
