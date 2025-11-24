# 09: CLI/API Schema Documentation

> ARCHIVED: This document has been moved to `docs/planning/v1-prerelease/09-cli-api-schemas.md`. See ADR-012 for decision on CLI & API schema.

## History

```yaml
Origin: "docs/v1-prerelease/09-cli-api-schemas.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning copy created in `docs/planning/v1-prerelease/09-cli-api-schemas.md`; ADR-012 introduced for decisions."
```

---

## Metadata

```yaml
feature: "CLI/API Schemas"
slug: "cli-api-schemas"
status: "not-started"
priority: "medium"
type: "documentation"
effort: "small"
depends_on: ["all-features"]
enables: ["regression-testing", "client-libraries"]
testing: ["manual"]
breaking_change: false
week: "7-8"
related_docs:
  - "08-config-schema.md"
```

---

## Overview

Document complete CLI command tree and API endpoints for:

- Regression testing (detect breaking changes)
- Client library development
- Integration documentation
- Support/troubleshooting

---

## CLI Command Reference

Complete tree of all commands with:

- Arguments
- Flags
- Output format
- Examples
- Exit codes

---

## API Endpoint Reference

Complete REST API with:

- Endpoints (GET/POST/DELETE/PATCH)
- Request schemas
- Response schemas
- Error codes
- Examples

---

## OpenAPI Specification

Generate `docs/openapi.yaml` for:

- API documentation (Swagger UI)
- Client generation (openapi-generator)
- Testing (Postman collections)

---

## Success Criteria

- ✅ CLI command tree documented
- ✅ All API endpoints documented
- ✅ OpenAPI spec generated
- ✅ Examples for all commands/endpoints
- ✅ Error codes documented

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: All features (documents final API)
**Blocking**: None
