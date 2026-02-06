# ADR-013: Configuration Schema Standardization

**Date**: 2025-11-23
**Status**: Partially Implemented

## Context

The v1-prerelease proposed a more formal configuration schema for Boxy, to validate and document all top-level configuration options, pools, providers, and hooks. A schema enables validation (before runtime), better UX for editors, and consistent migration across versions.

## Decision

1. Standardize configuration schema as a JSON Schema (draft 2020-12) file embedded in the binary at `internal/config/schema.json`.
2. The `boxy init` command writes `.boxy-schema.json` alongside `boxy.yaml` and prepends a `# yaml-language-server` directive so VS Code (with the YAML extension) provides autocompletion and validation automatically.
3. A `boxy schema` subcommand prints or writes the schema for use outside of `boxy init`.
4. Add validation tests for the configuration schema using `google/jsonschema-go`.

### Schema Location

The schema file (`.boxy-schema.json`) is written to the **current directory** alongside the config file rather than a central location like `~/.config/boxy/`. Rationale:

- Relative path in the YAML comment is portable across machines
- Schema is always version-matched to the binary that wrote it
- No dependency on XDG directories
- Teams can commit the schema to source control if they want

### JSON Schema Library

We chose [`google/jsonschema-go`](https://github.com/google/jsonschema-go) (v0.4.2) for schema validation in tests. This is used only in test code to validate that the example config passes the schema.

**Why `google/jsonschema-go`:**

- Official Google project (MIT licensed)
- Zero external dependencies (stdlib only)
- Supports draft 2020-12
- Clean API: unmarshal schema JSON, resolve, validate `map[string]any`

**Known limitations (acceptable for our use case):**

- Pre-v1 API — used only in tests, so breaking changes are low-risk
- `format` keyword is ignored during validation — we use `pattern` for duration strings instead
- Must validate `map[string]any`, not Go structs directly — this is how we use it (YAML → map → validate)

## Implementation

### Phase 1 (Implemented)

- `internal/config/schema.json` — JSON Schema (draft 2020-12) describing all config types
- `config.SchemaJSON` — embedded schema bytes via `//go:embed`
- `config.SchemaFileName` — constant for the file name (`.boxy-schema.json`)
- `boxy init` writes the schema file and adds the YAML language server comment
- `boxy schema` subcommand prints to stdout or writes to a directory (`--output`)
- `boxy.example.yaml` includes the schema comment
- Test coverage for schema validity, structure, and example config validation

### Phase 2 (Future)

- `boxy config validate` command that checks a config file against the schema and returns errors
- Incorporate schema validation into `boxy serve` startup path for fail-fast on malformed config
- Schema generation from Go types (if/when the library API stabilizes)

## Consequences

- Users get immediate feedback for config issues via editor integration.
- Editor autocompletion and hover docs are available with zero additional setup beyond `boxy init`.
- This adds maintenance overhead for schema updates across versions — the schema must be kept in sync with changes to config structs.

## History

```yaml
Origin: "docs/v1-prerelease/08-config-schema.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "partially-implemented"
Notes: >
  Phase 1 implemented: JSON Schema file, go:embed, boxy init/schema commands,
  editor integration via yaml-language-server directive, tests using
  google/jsonschema-go. Phase 2 (runtime validation, boxy config validate)
  is deferred.
```
