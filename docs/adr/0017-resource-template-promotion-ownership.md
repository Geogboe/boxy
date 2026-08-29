# ADR-0017: Resource template promotion and ownership

- **Status:** Accepted
- **Date:** 2026-08-28

## Context

Resource templates allow a derived pool to use a ready resource from an
ancestor pool and apply only the configuration package delta. Existing
resources retain immutable `OriginPool` provenance, and pool `max_total`
accounting currently relies on that field. Promotion needs a distinct current
ownership value without weakening provenance or guest credential authorization.

## Decision

Promotion is explicit, one-directional, and observable through a new
`promoting` resource state. Resources gain current and pending pool ownership
fields:

- current ownership controls inventory membership and capacity accounting;
- pending ownership is set while promotion is in progress;
- origin remains the immutable provisioning and authorization provenance.

The source resource is eligible only when ready and surplus relative to the
source pool's minimum-ready protection. If no eligible resource exists, the
destination provisions from scratch.

Promotion rotates the destination bootstrap credential before configuration.
Package application uses the resulting credential. Success commits destination
ownership and ready state. Failure uses the existing quarantine/destroy path;
the resource never returns to the source pool.

## Consequences

- A promoted resource no longer consumes the source pool's `max_total` after
  ownership is committed, while provenance remains queryable.
- Resource and pool APIs expose a transient promotion state.
- Promotion is not resource recycling and does not change ADR-0002's rule that
  allocated sandbox resources are never returned to a pool.
- Configurable promotion failure thresholds and forensic retention remain a
  future concern.
