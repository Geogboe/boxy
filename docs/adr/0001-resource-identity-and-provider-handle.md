# ADR 0001: Resource Identity and Provider Handle

Status: Deprecated

## Context

Boxy tracks and manages resources that exist on external systems (Docker engines,
Hyper-V hosts, etc). A core question is how Boxy should identify a resource over
time, and what minimum information Boxy must persist to perform lifecycle
operations later (inspect, destroy, etc).

The design needs to:

- keep Boxy internals stable across different provider types
- avoid embedding provider-specific configuration everywhere
- allow providers/drivers to be largely stateless (no hidden in-memory mapping)

## Summary

This ADR captured an early discussion about resource identity and a provider-local
addressing token. The project later punted on the provider-local identifier
field naming and temporarily removed it from the model.

Keep this document for historical context; revisit with a new ADR if/when a
provider-local addressing field is reintroduced.

   - `Resource.Provider` (a `ProviderRef` identifying the provider instance)
   - an opaque provider-local reference (called a "handle" in discussion)

3. Provider-specific semantics stay in the provider driver code. Boxy core stores
   and round-trips the handle, but does not interpret it.

## Notes

"Handle" means: a provider-local token that identifies a concrete external
instance on a specific provider instance.

Examples:

- Docker: container ID (immutable) or container name (mutable/reusable)
- Hyper-V: VM GUID (preferred) or VM name

The project does still expect that provider implementations will eventually need
some way to address external instances, but the exact model field(s) are deferred.

## Consequences

- Boxy can refer to resources consistently even if provider naming differs.
- Providers/drivers can remain stateless; Boxy persistence becomes the source of
  truth for "what resource should be managed".
- If the external instance is renamed out-of-band and the stored handle is a
  name rather than an immutable ID, Boxy may lose the ability to address it. For
  providers that support immutable IDs, prefer using those as the handle.

## Alternatives Considered

1. Use provider-native identifiers as the only identity.

Pros:
- fewer IDs in the model

Cons:
- leaks provider quirks into Boxy core
- makes cross-provider tooling harder (storage, APIs, UI)

2. Do not store any provider-local handle on Resource.

Pros:
- "cleaner" separation on paper

Cons:
- requires hidden state elsewhere (provider/agent must persist a mapping), which
  complicates operation, recovery, and debugging
