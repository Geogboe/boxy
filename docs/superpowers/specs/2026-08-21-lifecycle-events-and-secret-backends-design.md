# Lifecycle Events and Secret Backends Design

**Date:** 2026-08-21
**Status:** Accepted for implementation
**Issue:** #197

## Summary

Boxy needs a reusable response to domain events: something happens, a policy
decides what should happen next, and an action performs it. The first concrete
use is pool admission. A resource created from a VM image must not enter the
ready pool with its bootstrap credential unchanged. The existing allocation-time
guest credential rotation remains in place for caller isolation; this design
adds a separate pool-admission rotation.

The lifecycle package is a public generic contract. It is deliberately separate
from `pkg/policycontroller`, which remains the synchronous desired-state
reconciler used by pool and agent reconciliation, and from `pkg/eventstream`,
which is bounded live command output.

## Event model

`pkg/lifecycle` defines an event envelope with a stable ID, type, subject,
recorded UTC time, and opaque JSON payload. Payloads contain identifiers and
non-secret state only. A policy maps an event to an action and an outcome; an
action is responsible for idempotency using the event ID and subject.

The event store is a separate narrow interface in `pkg/store`. MemoryStore and
DiskStore implement it so tests and the current JSON state backend have the
same behavior. The queue supports append, claim with a lease, acknowledgement,
retry scheduling, and compaction of acknowledged operational events. It is not
an audit log and does not require SQLite, Postgres, or another database driver.

Delivery is at least once. A worker reclaims expired leases after a restart.
Infrastructure failures retry with bounded backoff. Domain actions can return a
terminal outcome when retrying the same resource would be unsafe.

## Pool admission flow

1. A provisioner creates a resource and returns it as `provisioning`.
2. The pool manager persists the resource and publishes one
   `resource.provisioned` event keyed by the resource ID.
3. The lifecycle worker evaluates the pool admission policy.
4. For a guest-personalizable resource, the policy invokes the existing
   `PersonalizeGuest` contract using the pool bootstrap credential from the
   configured secret backend. The returned rotated credential is stored under
   the resource ID in that backend; it is the bootstrap for the later
   allocation-time rotation. Other resource types complete admission without a
   guest action.
5. Success persists the resource as `ready`; the next reconciliation rebuilds
   ready inventory from persisted resources. Allocation-time personalization
   consumes the resource-scoped credential and removes it after successful
   allocation.
6. Failure persists an observable `error` state and uses the existing pool
   quarantine/destroy path and capped provisioning backoff to replace the
   resource. The failed resource is never personalized again in place.

`internal/sandbox` continues to select only `ResourceStateReady`, so a pending
or failed lifecycle event is an allocation guard without introducing a second
scheduler or a new protobuf state. Reconciliation republishes a missing
`resource.provisioned` event when it finds a persisted provisioning resource.

## Secret backends

The server-owned secret store is explicit and separate from the JSON event/state
log:

- `dpapi`: Windows machine-scope DPAPI encryption for server services.
- `keyring`: the existing OS keyring integration for local development.
- `file`: a portable ACL/mode-protected secret file for Linux and containers.

There is no universal default and no silent fallback. A credential-dependent
configuration must select a backend. File and DPAPI backends require a path;
keyring does not. The file backend uses atomic replacement and validates the
parent/file permissions before use. DPAPI protects the value and still applies
file permissions to the ciphertext container.

The existing `pool_guest_credentials` state field is legacy input only. Runtime
credential lookup uses the selected backend, with separate pool-bootstrap and
resource-scoped keys. `boxy doctor` reports backend availability, unsafe
permissions, and legacy plaintext values. `boxy migrate secrets` performs an
explicit, idempotent migration and removes the legacy value only after the new
backend has accepted it. No automatic migration occurs at daemon startup.

Kubernetes, OKD, CSI, Vault, and external-secret APIs are not runtime
integrations in this milestone. Compose and K3s examples demonstrate explicit
backend configuration and protected storage. They must not imply that a mounted
Kubernetes Secret is encrypted in etcd unless the cluster operator enabled
encryption at rest.

## Compatibility and non-goals

- No protobuf changes.
- No changes to existing authentication or REST paths.
- Existing allocation-time credential delivery remains unchanged.
- No audit/event history, database migration, or scheduler redesign.
- No automatic secret migration and no keychain requirement.
