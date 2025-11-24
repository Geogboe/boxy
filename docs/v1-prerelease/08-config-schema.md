# 08: Config Schema

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/08-config-schema.md` for planning and schema definition.

## History

```yaml
Origin: "docs/v1-prerelease/08-config-schema.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning copy created in `docs/planning/v1-prerelease/08-config-schema.md`."
```

---

## Metadata

```yaml
feature: "Config File Schema"
slug: "config-schema"
status: "not-started"
priority: "high"
type: "feature"
effort: "medium"
depends_on: []
enables: ["validation", "ide-support", "documentation"]
testing: ["unit"]
breaking_change: false
week: "5-6"
related_docs:
  - "11-config-location.md"
```

---

## Overview

Define formal YAML schema using JSON Schema for:

- Config validation (catch errors early)
- IDE autocomplete/validation (VSCode, IntelliJ)
- Auto-generated documentation
- Version management

---

## Schema Location

`docs/config-schema.json` - JSON Schema definition

---

## Key Benefits

- **Validation**: `boxy config validate` catches typos/errors
- **IDE Support**: Autocomplete in VSCode with yaml-language-server
- **Documentation**: Auto-generate docs from schema
- **Versioning**: Track config format changes

---

## Example Usage

```yaml
# boxy.yaml with schema reference
# yaml-language-server: $schema=https://raw.githubusercontent.com/geogboe/boxy/main/docs/config-schema.json

storage:
  type: sqlite
  path: ./data/boxy.db

pools:
  - name: ubuntu-containers
    type: container  # IDE suggests: "vm", "container", "process"
    backend: docker  # IDE suggests: "docker", "hyperv", "kvm"
```

---

## CLI Command

```bash
# Validate config
boxy config validate

# Output:
✓ Config is valid
✓ All pools have required fields
✓ All hooks are well-formed
```

---

## Success Criteria

- ✅ JSON Schema defined
- ✅ Schema covers all config fields
- ✅ Validation tool implemented
- ✅ IDE integration documented
- ✅ Example configs updated with schema reference

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None
**Blocking**: None
