# 13: Documentation Updates

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/13-documentation-updates.md` for planning and migration updates.

## History

```yaml
Origin: "docs/v1-prerelease/13-documentation-updates.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning copy created in `docs/planning/v1-prerelease/13-documentation-updates.md`."
```

---

## Metadata

```yaml
feature: "Documentation Consistency Review"
slug: "documentation-updates"
status: "not-started"
priority: "low"
type: "documentation"
effort: "small"
depends_on: ["all-features"]
enables: ["consistency", "clarity"]
testing: ["manual"]
breaking_change: false
week: "9"
related_docs:
  - "03-terminology-updates.md"
```

---

## Overview

Final consistency pass across all documentation:

- Update CLAUDE.md with new architecture
- Update ADRs with v1 decisions
- Update README.md with v1 features
- Update QUICKSTART.md with new workflows
- Consistency check (terminology, examples)

---

## Files to Update

- `CLAUDE.md` - Core concepts, terminology
- `README.md` - Feature list, quick start
- `docs/QUICKSTART.md` - Getting started guide
- `docs/architecture/*.md` - Architecture docs
- `docs/decisions/*.md` - ADRs
- `examples/*.yaml` - Example configs

---

## Consistency Checks

- [ ] All hooks use new names (on_provision, on_allocate)
- [ ] "Preheated resources" not "warm pools"
- [ ] Config location is ./boxy.yaml
- [ ] All examples include preheating config
- [ ] All agent examples include mTLS

---

## Success Criteria

- ✅ All docs consistent with v1 terminology
- ✅ All examples updated
- ✅ No references to old architecture
- ✅ All cross-references valid
- ✅ README accurately reflects v1 features

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: All features
**Blocking**: None
