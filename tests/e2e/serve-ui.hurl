# serve-ui.hurl — Web dashboard content tests for boxy serve (UI enabled).
#
# Run: hurl --test --variable base_url=http://localhost:9090 tests/e2e/serve-ui.hurl

# ── Home page ────────────────────────────────────────────────────────────

GET {{base_url}}/
HTTP 200
[Asserts]
header "Content-Type" contains "text/html"
body contains "Boxy"
body contains "Overview"
body contains "stat-card"
body contains "hx-get"
body contains "htmx.min.js"
body contains "style.css"


# ── Pools page ───────────────────────────────────────────────────────────

GET {{base_url}}/ui/pools
HTTP 200
[Asserts]
header "Content-Type" contains "text/html"
body contains "Pools"
body contains "All Pools"
body contains "No pools configured"
body contains "hx-trigger"


# ── Sandboxes page ──────────────────────────────────────────────────────

GET {{base_url}}/ui/sandboxes
HTTP 200
[Asserts]
header "Content-Type" contains "text/html"
body contains "Sandboxes"
body contains "All Sandboxes"
body contains "No sandboxes created"


# ── HTMX fragment: stats ────────────────────────────────────────────────

GET {{base_url}}/ui/fragments/stats
HTTP 200
[Asserts]
header "Content-Type" contains "text/html"
body contains "stat-card"
body contains "Pools"
body contains "Sandboxes"
body contains "Resources"
# Fragment should NOT contain the full layout
body not contains "<!DOCTYPE html>"


# ── HTMX fragment: pools table ──────────────────────────────────────────

GET {{base_url}}/ui/fragments/pools-table
HTTP 200
[Asserts]
body contains "No pools configured"
body not contains "<!DOCTYPE html>"


# ── HTMX fragment: sandboxes table ──────────────────────────────────────

GET {{base_url}}/ui/fragments/sandboxes-table
HTTP 200
[Asserts]
body contains "No sandboxes created"
body not contains "<!DOCTYPE html>"


# ── Static assets ────────────────────────────────────────────────────────

GET {{base_url}}/static/style.css
HTTP 200
[Asserts]
header "Content-Type" contains "text/css"
body contains "--bg:"


GET {{base_url}}/static/htmx.min.js
HTTP 200
[Asserts]
bytes count > 1000


# ── Nav active states ────────────────────────────────────────────────────

# Home has active on Home link
GET {{base_url}}/
HTTP 200
[Asserts]
body contains "<a href=\"/\" class=\"active\">Home</a>"

# Pools page has active on Pools link
GET {{base_url}}/ui/pools
HTTP 200
[Asserts]
body contains "<a href=\"/ui/pools\" class=\"active\">Pools</a>"

# Sandboxes page has active on Sandboxes link
GET {{base_url}}/ui/sandboxes
HTTP 200
[Asserts]
body contains "<a href=\"/ui/sandboxes\" class=\"active\">Sandboxes</a>"
