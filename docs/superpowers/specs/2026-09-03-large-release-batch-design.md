# Large release batch: package graphs, artifacts, and dashboard

## Scope

This release combines explicit immutable package dependencies, S3-compatible
artifact stores, direct provider source ingestion, and the operational
dashboard refresh.

## Package graph contract

Package manifests use `dependencies: [name@version]`. References are exact;
there is no range or mutable tag resolution. The planner discovers roots in
caller order, dependencies in declaration order, and emits a dependency before
its dependent. A reference is deduplicated at first discovery. Missing
references and cycles are errors; cycle errors print the full deterministic
chain. Startup validates configured graphs, while the planner validates again
before producing operations. Apply remains fail-fast and records only the
successful prefix.

## Source delivery contract

Stores own source bytes. Boxy stores only source metadata and uses a named
store adapter to create a short-lived provider-neutral descriptor. A
descriptor contains exactly one local path or HTTPS/HTTP URL, a SHA-256 digest,
format/provider metadata, and an expiry. Providers pull and verify directly;
the authenticated agent channel carries the descriptor but never becomes a
byte transport. Hyper-V supports VHD/VHDX descriptors, Docker rejects raw
sources in favor of image references, and simulation providers validate the
same descriptor contract.

## Dashboard contract

The UI includes repository/version branding and a persisted light/dark theme.
Admin-only navigation is consistent on every full-page route. Pool inventory
defaults to active resources, exposes historical resources through an explicit
filter, and uses expandable compact rows. Dashboard reads are bounded before
rendering large inventories, and the home page provides status cards plus
direct links to the primary operational surfaces.
