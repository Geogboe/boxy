# Diagnose A Failed Or Stuck Sandbox

Use this workflow when sandbox fulfillment is slow, stuck in `pending`, or ends in `failed`.

## Steps

1. Check top-level daemon health with `boxy status`.
2. Inspect the specific sandbox with `boxy sandbox get <id>`.
3. Check whether the backing pools have ready capacity or are continuously failing to warm.
4. Inspect daemon state in `.boxy/state.json` when runtime state details matter.
5. Review daemon logs, including `--log-level debug` or `--log-file` output when available.
6. For provider-specific issues, use:
   - `boxy debug provider list`
   - `boxy debug provider get <id>`
   - `boxy debug provider exec <id> -- <cmd>`
   - `boxy debug provider set-state <id> <state>` only when the task is explicit debugging
   - `boxy debug provider delete <id>` for cleanup of broken test resources

## Symptom Mapping

- `status` cannot reach the server: daemon is not running or address resolution is wrong.
- Sandbox stuck in `pending`: pool warm-up or provider provisioning problem.
- Sandbox `failed`: inspect request shape, pool names, provider errors, and runtime state.
- Provider object exists but is unhealthy: use `debug provider get` and `debug provider exec` before forcing state changes.