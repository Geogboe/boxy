# serve-api.hurl — JSON REST API contract tests for boxy serve.
#
# Run: hurl --test --variable base_url=http://localhost:9090 tests/e2e/serve-api.hurl

# ── Health check ─────────────────────────────────────────────────────────

GET {{base_url}}/healthz
HTTP 200
[Asserts]
body == "ok"


# ── GET /api/v1/pools ────────────────────────────────────────────────────

GET {{base_url}}/api/v1/pools
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$" count == 0


# ── GET /api/v1/resources ────────────────────────────────────────────────

GET {{base_url}}/api/v1/resources
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$" count == 0


# ── GET /api/v1/sandboxes ───────────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$" count == 0


# ── Method not allowed ───────────────────────────────────────────────────

POST {{base_url}}/api/v1/pools
HTTP 405


# ── Unknown API route → 404 ─────────────────────────────────────────────

GET {{base_url}}/api/v1/notreal
HTTP 404
