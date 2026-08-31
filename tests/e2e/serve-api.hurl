# serve-api.hurl — JSON REST API contract tests for boxy serve.
#
# Run: hurl --test --variable base_url=http://localhost:9090 --variable api_key=... tests/e2e/serve-api.hurl

# ── Health check ─────────────────────────────────────────────────────────

GET {{base_url}}/healthz
HTTP 200
[Asserts]
body == "ok"


# ── GET /api/v1/pools ────────────────────────────────────────────────────

GET {{base_url}}/api/v1/pools
Authorization: Bearer {{api_key}}
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"


# ── GET /api/v1/resources ────────────────────────────────────────────────

GET {{base_url}}/api/v1/resources
Authorization: Bearer {{api_key}}
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"


# ── GET /api/v1/sandboxes ───────────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes
Authorization: Bearer {{api_key}}
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"


# ── POST /api/v1/sandboxes ───────────────────────────────────────────────

POST {{base_url}}/api/v1/sandboxes
Authorization: Bearer {{api_key}}
Content-Type: application/json
{"name": "hurl-test", "requests": [{"type": "container", "profile": "demo", "count": 1}]}

HTTP 202
[Captures]
sandbox_id: jsonpath "$.id"
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.name" == "hurl-test"
jsonpath "$.id" isString
jsonpath "$.status" == "pending"


# ── POST /api/v1/sandboxes validation ───────────────────────────────────

POST {{base_url}}/api/v1/sandboxes
Authorization: Bearer {{api_key}}
Content-Type: application/json
{"requests": [{"type": "container", "profile": "demo", "count": 1}]}

HTTP 400
[Asserts]
jsonpath "$.error" == "sandbox name is required"


POST {{base_url}}/api/v1/sandboxes
Authorization: Bearer {{api_key}}
Content-Type: application/json
{"name": "legacy-shape", "resources": [{"pool": "demo", "count": 1}]}

HTTP 400
[Asserts]
jsonpath "$.error" == "invalid request body"


# ── GET /api/v1/sandboxes/{id} ──────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
Authorization: Bearer {{api_key}}
HTTP 200
[Asserts]
jsonpath "$.id" == {{sandbox_id}}
jsonpath "$.status" exists


# ── DELETE /api/v1/sandboxes/{id} ───────────────────────────────────────

DELETE {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
Authorization: Bearer {{api_key}}
HTTP 204


# ── GET after delete → 404 ──────────────────────────────────────────────

GET {{base_url}}/api/v1/sandboxes/{{sandbox_id}}
Authorization: Bearer {{api_key}}
HTTP 404


# ── GET /api/v1/pools/{name} → 404 (empty server) ───────────────────────

GET {{base_url}}/api/v1/pools/nonexistent
Authorization: Bearer {{api_key}}
HTTP 404


# ── GET /api/v1/resources/{id} → 404 (empty server) ────────────────────

GET {{base_url}}/api/v1/resources/nonexistent
Authorization: Bearer {{api_key}}
HTTP 404


# ── Method not allowed ───────────────────────────────────────────────────

POST {{base_url}}/api/v1/pools
Authorization: Bearer {{api_key}}
HTTP 405


# ── Unknown API route → 404 ─────────────────────────────────────────────

GET {{base_url}}/api/v1/notreal
Authorization: Bearer {{api_key}}
HTTP 404
