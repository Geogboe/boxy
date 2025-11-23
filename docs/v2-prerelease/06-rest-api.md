# 06: REST API

---

## Metadata

```yaml
feature: "REST API"
slug: "rest-api"
status: "not-started"
priority: "high"
type: "feature"
effort: "medium"
depends_on: ["v1-multi-tenancy"]
enables: ["programmatic-access", "integrations"]
testing: ["unit", "integration", "e2e"]
breaking_change: false
week: "1-3"
```

---

## Overview

Full HTTP REST API for programmatic access:

- Complements CLI (CLI can use API internally)
- JSON request/response
- RESTful design
- OpenAPI spec
- Client libraries (Go, Python, TypeScript)

---

## Key Endpoints

```text
# Sandboxes
POST   /api/v1/sandboxes          # Create
GET    /api/v1/sandboxes           # List
GET    /api/v1/sandboxes/:id       # Get
DELETE /api/v1/sandboxes/:id       # Destroy
PATCH  /api/v1/sandboxes/:id       # Update (extend duration)

# Pools
GET    /api/v1/pools               # List
GET    /api/v1/pools/:name         # Get stats
POST   /api/v1/pools/:name/scale   # Scale

# Resources
GET    /api/v1/sandboxes/:id/resources  # Get resources
```

---

## Example Usage

```bash
# Create sandbox
curl -X POST https://boxy-server:8443/api/v1/sandboxes \
  -H "Authorization: Bearer bxy_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-test-env",
    "resources": [{"pool": "ubuntu-containers", "count": 1}],
    "duration": "1h"
  }'

# Response
{
  "id": "sb-abc123",
  "name": "my-test-env",
  "state": "creating",
  "created_at": "2025-11-23T14:00:00Z",
  "expires_at": "2025-11-23T15:00:00Z"
}
```

---

## Success Criteria

- ✅ All endpoints implemented
- ✅ OpenAPI spec generated
- ✅ Authentication works
- ✅ Error responses consistent
- ✅ Client libraries generated
- ✅ API documentation published

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: v1-multi-tenancy
