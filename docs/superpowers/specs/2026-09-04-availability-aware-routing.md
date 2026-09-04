# Availability-aware agent routing (#179)

## Scope

When a pool can use more than one agent for the requested provider, agent
resolution should use the capacity information already reported through the
heartbeat. This keeps new provisioning work away from an agent with less
reported headroom without changing pinned-agent behavior.

## Routing contract

- A pinned agent is still selected only when it is registered, heartbeat
  available, and capable of the requested provider. Reported capacity does not
  override an explicit pin.
- For an unpinned request, the registry considers heartbeat-available agents
  that advertise the provider. If every candidate reports a provider-specific
  `ResourceAvailability`, the candidate with the greatest `MemoryMB` is
  selected.
- Equal-capacity candidates use the existing round-robin cursor as a
  deterministic tie-breaker. The cursor advances after every successful
  resolution, preserving fair distribution when capacities are equal.
- If any eligible candidate is an older agent, has not reported a snapshot, or
  has no entry for the requested provider, resolution falls back to the
  existing round-robin behavior. Partial telemetry must not make an otherwise
  usable agent disappear from routing.

The availability snapshot is intentionally treated as the latest heartbeat
observation. The existing heartbeat liveness gate remains responsible for
stale/disconnected agents; this change does not invent a second timeout or
couple routing to a scheduler policy.

## Verification

The agent-registry tests cover higher-headroom selection, fair equal-capacity
tie behavior through the existing round-robin coverage, and compatibility with
partially upgraded agents that do not report capacity.
