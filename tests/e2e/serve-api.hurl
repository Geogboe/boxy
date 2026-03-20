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


# ── POST /api/v1/sandboxes ───────────────────────────────────────────────

POST {{base_url}}/api/v1/sandboxes
Content-Type: application/json
{"name": "hurl-test"}

HTTP 201
[Captures]
sandbox_id: jsonpath "$.id"
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.name" == "hurl-test"
jsonpath "$.id" isString


# ── GET /api/v1/sandboxes/{id} ──────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
HTTP 200
[Asserts]
jsonpath "$.id" == {{sandbox_id}}


# ── DELETE /api/v1/sandboxes/{id} ───────────────────────────────────────

DELETE {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
HTTP 204


# ── GET after delete → 404 ──────────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
HTTP 404


# ── GET /api/v1/pools/{name} → 404 (empty server) ───────────────────────

GET {{base_url}}/api/v1/pools/nonexistent
HTTP 404


# ── GET /api/v1/resources/{id} → 404 (empty server) ────────────────────

GET {{base_url}}/api/v1/resources/nonexistent
HTTP 404


# ── Method not allowed ───────────────────────────────────────────────────

POST {{base_url}}/api/v1/pools
HTTP 405


# ── Unknown API route → 404 ─────────────────────────────────────────────

GET {{base_url}}/api/v1/notreal
HTTP 404
