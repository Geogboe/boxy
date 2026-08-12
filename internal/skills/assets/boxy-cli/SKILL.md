---
name: boxy-cli
description: 'Use when working with boxy CLI, sandboxes, pools, serve daemon, sandbox spec authoring, provider debugging, and failed sandbox diagnosis. Relevant for GitHub Copilot, Claude Code, Codex, and other coding agents that need to operate boxy safely and efficiently.'
---

# Boxy CLI

Use this skill when the task is to operate Boxy itself rather than editing Boxy internals.

## Command Discovery

Do not guess command syntax.

Run `boxy help all` first when you need the current command surface.
Run `boxy <command> --help` before using a command you have not executed yet.

Core commands you will commonly use:
- `boxy init`
- `boxy serve`
- `boxy login`
- `boxy logout`
- `boxy admin api-key bootstrap`
- `boxy admin api-key create`
- `boxy admin api-key list`
- `boxy admin api-key revoke`
- `boxy config validate`
- `boxy sandbox create`
- `boxy sandbox list`
- `boxy sandbox get`
- `boxy sandbox delete`
- `boxy sandbox extend`
- `boxy sandbox exec`
- `boxy agent serve`
- `boxy agent service install`
- `boxy agent service uninstall`
- `boxy agent service start`
- `boxy agent service stop`
- `boxy agent service status`
- `boxy serve service install`
- `boxy serve service uninstall`
- `boxy serve service start`
- `boxy serve service stop`
- `boxy serve service status`
- `boxy agent token create`
- `boxy agent token list`
- `boxy agent token revoke`
- `boxy agent list`
- `boxy agent revoke`
- `boxy status`
- `boxy debug pool drain`
- `boxy debug pool fill`
- `boxy debug provider list`
- `boxy debug provider get`
- `boxy debug provider exec`
- `boxy debug provider set-state`
- `boxy debug provider delete`
- `boxy version`
- `boxy update`
- `boxy skills install`
- `boxy skills uninstall`

## Operating Rules

- Validate config with `boxy config validate` before telling the user to run `boxy serve`.
- Boxy serves REST and agent gRPC over TLS by default. Use `--ca-cert` for the generated Boxy CA; use `--insecure` only for explicit local development.
- Run `boxy admin api-key bootstrap` locally once to create the first administrator key, then `boxy login --server <url> --ca-cert <path>` to store it in the OS keyring. Raw keys are shown once and must not be written to config or state files.
- Prefer `boxy serve --once` for smoke checks and `boxy serve` for daemon usage.
- Sandbox creation is asynchronous. Use `boxy sandbox create --no-wait` if you need the request accepted quickly, then poll with `boxy sandbox get`.
- Sandbox deletion is asynchronous. `boxy sandbox delete <id>` waits until the daemon finishes cleanup; use `--no-wait` to return after acceptance.
- `boxy sandbox extend <id> <duration>` only works on sandboxes created with `policies.auto_destroy_after` set; it pushes that expiry out by `<duration>` and fails if the sandbox has no expiry to extend or is already being deleted.
- `boxy sandbox exec <id> -- <command> [args...]` runs a one-shot command only in a ready sandbox. It selects the only resource automatically; pass `--resource` for multi-resource sandboxes. Use `--stream` for live NDJSON events. Interactive stdin/PTY sessions are not supported.
- Use `boxy debug pool drain <pool>` and `boxy debug pool fill <pool>` for daemon-backed operator maintenance of unused ready pool inventory.
- Remote agents register with a single-use token: `boxy agent token create` prints the raw token exactly once (never stored or retrievable again); the agent redeems it on first connect and authenticates with an issued mTLS client certificate afterward. `boxy agent list` shows registered agents and availability; `boxy agent revoke <id>` deny-lists an agent's certificate and tears down its live connection. If the agent is permanently gone, `boxy agent revoke <id> --force-orphan-resources` also detaches every resource still attributed to it (pool inventory + store) without contacting the agent — resources otherwise stay un-`Destroy`able through the normal path since `Destroy` always routes to the exact agent that created them.
- `boxy agent serve --server <host:port> --providers <list> --token <token> --ca-cert <path>` runs a host as a remote agent. The `--server` address is the daemon's gRPC listener (default `:9091`), not its REST port. `--ca-cert` (the server's `.boxy/ca.crt`, copied out-of-band) is required for the first connection; after registration the issued credentials in `.boxy-agent/` are used automatically and neither flag is needed again.
- `boxy agent service install --server <host:port> --providers <list> --token <token> --ca-cert <path>` installs the agent as a background service instead of a foreground process — a real service by default (requires an elevated process), or `--user` for an unprivileged Task Scheduler/systemd-user fallback. `boxy serve service install` does the same for the daemon. See docs/service-install.md.
- If a sandbox fails or stalls, use the diagnosis workflow instead of guessing.
- Boxy persists daemon runtime state in `.boxy/state.json` near the active config or working directory; use that fact in diagnosis, not as the primary control interface.

## Workflows

- [Bootstrap a project](./references/bootstrap-project.md)
- [Author a sandbox spec](./references/sandbox-authoring.md)
- [Diagnose a failed or stuck sandbox](./references/diagnose-sandbox.md)

## When Not To Use This Skill

- When modifying Boxy source code itself, use the repository instructions and inspect the codebase directly.
- When command syntax is unclear and you have not run `boxy help all` yet.