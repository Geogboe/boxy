# serve-ui-disabled.hurl — Verify UI routes return 404 when --ui=false.
#
# Run against a server started with --ui=false:
#   hurl --test --variable base_url=http://localhost:19090 tests/e2e/serve-ui-disabled.hurl

# ── API still works ─────────────────────────────────────────────────────

GET {{base_url}}/healthz
HTTP 200

GET {{base_url}}/api/v1/pools
HTTP 200
[Asserts]
header "Content-Type" contains "application/json"

GET {{base_url}}/api/v1/resources
HTTP 200

GET {{base_url}}/api/v1/sandboxes
HTTP 200


# ── UI routes are gone ──────────────────────────────────────────────────

GET {{base_url}}/
HTTP 404

GET {{base_url}}/ui/pools
HTTP 404

GET {{base_url}}/ui/sandboxes
HTTP 404

GET {{base_url}}/ui/fragments/stats
HTTP 404

GET {{base_url}}/static/style.css
HTTP 404

GET {{base_url}}/static/htmx.min.js
HTTP 404
