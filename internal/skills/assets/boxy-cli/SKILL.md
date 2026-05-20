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
- `boxy config validate`
- `boxy sandbox create`
- `boxy sandbox list`
- `boxy sandbox get`
- `boxy sandbox delete`
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
- Prefer `boxy serve --once` for smoke checks and `boxy serve` for daemon usage.
- Sandbox creation is asynchronous. Use `boxy sandbox create --no-wait` if you need the request accepted quickly, then poll with `boxy sandbox get`.
- Sandbox deletion is asynchronous. `boxy sandbox delete <id>` waits until the daemon finishes cleanup; use `--no-wait` to return after acceptance.
- Use `boxy debug pool drain <pool>` and `boxy debug pool fill <pool>` for daemon-backed operator maintenance of unused ready pool inventory.
- If a sandbox fails or stalls, use the diagnosis workflow instead of guessing.
- Boxy persists daemon runtime state in `.boxy/state.json` near the active config or working directory; use that fact in diagnosis, not as the primary control interface.

## Workflows

- [Bootstrap a project](./references/bootstrap-project.md)
- [Author a sandbox spec](./references/sandbox-authoring.md)
- [Diagnose a failed or stuck sandbox](./references/diagnose-sandbox.md)

## When Not To Use This Skill

- When modifying Boxy source code itself, use the repository instructions and inspect the codebase directly.
- When command syntax is unclear and you have not run `boxy help all` yet.