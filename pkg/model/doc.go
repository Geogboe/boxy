// Package model contains Boxy's core domain data models.
//
// Documentation pillars for this package:
// 1) What the thing is (definition)
// 2) Why it exists (the problem it solves)
// 3) When to use it (where it belongs in the system)
// 4) How to use it correctly (invariants and intended mutation points)
//
// Design intent:
//   - Keep these types "data-first": plain structs and small enums.
//   - Prefer explicit domain types (PoolName, SandboxID, ResourceType) over raw
//     strings when it prevents mixing up identifiers.
//   - Keep behavior and side effects out of the model layer. Orchestrators,
//     pool controllers, providers, agents, and storage adapters should depend on
//     this package, not the other way around.
//
// ---
//
// # Provider
//
// What:
//   - A provider instance is an external system Boxy can provision resources on.
//     Examples: a specific Docker engine, or a specific Hyper-V host.
//
// Why:
//   - Boxy must be able to route lifecycle operations (create/inspect/destroy)
//     to the correct external system over time.
//
// When to use:
// - Use ProviderRef on domain objects that need routing ("which provider owns this?").
// - Use the runtime config layer (providersdk.Instance) for provider connection/config.
//
// How to use correctly:
//   - ProviderRef.Name must match a configured provider instance name.
//   - Avoid using the word "provider" to mean both the code and the external
//     system. In this repo:
//   - provider instance = external configured target (providersdk.Instance)
//   - ProviderRef = reference from core domain objects
//   - driver/adapter = code that talks to a provider type
//
// ---
//
// # Pool
//
// What:
// - Pool is a user-facing container of Resources.
//
// Why:
//   - Pools give Boxy a place to keep "ready-to-allocate" inventory, which is the
//     foundation of preheating (min_ready) and fast sandbox allocation.
//
// When to use:
//   - Use Pool when you want the primary CLI/config noun to be something humans
//     can name ("dev-docker", "win11-vms") and reason about as a bucket.
//   - Use Pool inventory when you need to allocate a Resource into a Sandbox or
//     decide whether to provision more inventory.
//
// How to use correctly:
//   - Pools should be homogeneous: all Resources in a Pool inventory should share
//     the same ResourceType. (See ResourceCollection.)
//   - Resources are single-use: once a Resource leaves a Pool for a Sandbox, it is
//     not returned to any Pool.
//   - The model layer does not replenish pools. A controller/reconciler should
//     enforce preheat policy and perform lifecycle transitions.
//
// ---
//
// # Resource
//
// What:
// - Resource is a tracked, provisioned instance (VM/container/share/etc).
//
// Why:
//   - Boxy needs a stable, backend-agnostic representation of "a thing that exists"
//     so it can allocate it, track its lifecycle, and eventually clean it up.
//
// When to use:
//   - Use Resource for anything that can be allocated to a Sandbox and has a
//     lifecycle state.
//
// How to use correctly:
//   - Resource.Type is intrinsic: a resource "is a VM" regardless of which pool or
//     sandbox currently references it.
//   - If you want homogeneous containers, enforce that at the container boundary
//     (ResourceCollection.Add / Validate) rather than trying to infer a resource's
//     kind from the container it happens to be in.
//
// ---
//
// # Sandbox
//
// What:
// - Sandbox is a user-facing environment over 1..N resources, treated as "one thing".
//
// Why:
//   - Users want one handle they can name and destroy even when the
//     environment contains multiple resources (VM + DB + share).
//   - A sandbox can be as small as a single resource ("container sandbox") or as
//     large as a full lab ("3 VM lab sandbox").
//
// When to use:
// - Use Sandbox as the primary lifecycle object in the system.
//
// How to use correctly:
//   - Sandboxes should reference concrete allocated items; avoid hiding "what was
//     allocated from where" since that is required for cleanup and troubleshooting.
package model
