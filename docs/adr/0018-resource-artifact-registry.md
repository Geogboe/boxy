# ADR-0018: Typed artifacts behind one resource artifact registry

- **Status:** Accepted
- **Date:** 2026-08-28

## Context

Templates need immutable guest sources and resource packages. A source may be
an externally owned S3 object or local file, while a package is a published
manifest plus content-addressed blobs. Treating these as unrelated registries
would duplicate resolution and access behavior, but treating them as one
untyped configuration blob would hide important validation and lifecycle
semantics.

## Decision

Expose one `pkg/artifact.Registry` facade with typed source and package
operations. The implementation may use separate store/cache components behind
that facade. Source and package records share common immutable artifact
identity and retrieval behavior, while retaining typed manifests and
validation.

Stores remain externally owned. Boxy records a locator, digest, format, and
metadata; it does not automatically copy every source into managed storage.
When a store supports a provider/agent-readable direct pull, the registry
returns that descriptor. Otherwise the server verifies and stages the bytes
through the provider's ingestion seam. S3-compatible signed pull URLs are
object-specific and expire after 15 minutes; they are never persisted, logged,
or copied into resource properties. Providers download and verify the
declared SHA-256 digest themselves.
The first adapters are local filesystem and S3-compatible stores.

Credentials are references, never raw persisted values. A package build creates
an immutable artifact; publish is explicit. Inline content is normalized and
digested locally without an implicit publish.

## Consequences

- Resourcepack planning can resolve both source and package references without
  knowing their physical store.
- Agents receive only opaque materialization/execution data, not registry
  policy or package lineage.
- Additional stores can be added behind the same facade without changing
  template configuration.
