# Atomic multi-pool fulfillment

## Goal

Extract the all-or-nothing claim/fulfill sequence from sandbox orchestration so
the transaction boundary is explicit and can be reused by other workflows.

## Contract

`pkg/fulfillment` will provide a keyed group transaction with four stages:

1. prepare each group, normally by ensuring capacity is ready;
2. capture a caller-owned snapshot after preparation;
3. fulfill each group in caller order; and
4. roll back the snapshot when fulfillment fails.

Groups must have positive counts and unique comparable keys. Preparation and
snapshot failures occur before any claim and do not invoke rollback. A
fulfillment failure invokes rollback exactly once. A caller may explicitly
abort without rollback for lifecycle cancellation such as a sandbox entering
deletion.

The package owns sequencing and failure semantics. The caller owns storage,
resource selection, status transitions, and snapshot contents through narrow
callbacks. This keeps Boxy-specific persistence out of the reusable primitive.

## Compatibility

Sandbox reconciliation will use the primitive without changing its public API
or status behavior. In particular, allocation failures still restore pools and
resources and mark the sandbox failed; deletion cancellation still exits
without restoring a snapshot owned by the deletion workflow.

## Verification

- unit tests cover ordering, validation, preparation/snapshot failures,
  rollback, rollback errors, and explicit no-rollback aborts;
- sandbox fulfillment regression tests remain green;
- full `task ci:validate` includes the WSL race pass on this Windows ARM64 host.
