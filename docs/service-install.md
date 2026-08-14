# Install boxy agent / boxy serve as a background service

`boxy agent service` and `boxy serve service` install boxy as an
OS-managed background process that starts automatically and survives
logout/reboot — no terminal needs to stay open.

## Platforms

- **Windows**: a real Windows Service (via the Service Control Manager)
  by default, or a Task Scheduler at-logon task with `--user`.
- **Linux**: a systemd system unit by default, or a systemd `--user` unit
  with `--user`.
- **macOS**: not supported yet. `service install` fails with a clear
  error rather than silently doing nothing. Tracked as a follow-up issue.

## Privileged vs. `--user`

By default, `install` registers the real service and requires an elevated
process:

- Windows: run from an Administrator PowerShell/terminal.
- Linux: run with `sudo`.

Pass `--user` to install the unprivileged fallback instead — no admin/root
needed:

- Windows: a Task Scheduler task triggered at your next logon. This is a
  real behavioral difference from a true service: it starts at **user
  logon**, not at machine boot before any login.
- Linux: a systemd `--user` unit, plus `loginctl enable-linger` so it
  keeps running (and starts at boot) without an active login session.
  Some hardened/managed hosts restrict `enable-linger` via polkit — if
  that happens, `install` still succeeds but prints a note; retry
  `loginctl enable-linger <user>` manually once permitted.

## Usage

```bash
# Agent — real service, requires elevation (provider instances from boxy.yaml)
boxy agent service install --config boxy.yaml --server boxy-server:9091 --token <token> --ca-cert ca.crt

# Agent — unprivileged fallback (select only hyperv, if desired)
boxy agent service install --user --config boxy.yaml --providers hyperv --server boxy-server:9091 --token <token> --ca-cert ca.crt

boxy agent service status
boxy agent service stop
boxy agent service start
boxy agent service uninstall           # keeps credentials/state
boxy agent service uninstall --purge   # also removes the data directory

# Server — same shape
boxy serve service install --listen :9090
boxy serve service status
boxy serve service uninstall
```

## What gets written

`install` resolves every path it's given (`--data-dir`, `--config`,
`--ca-cert`) to an absolute path and writes a small `service.yaml` file
next to the process's existing state directory (`.boxy-agent/` for the
agent, `.boxy/` for `serve`) — a running service has no predictable
working directory, so nothing in the installed invocation depends on cwd.

For an agent, `--config` selects provider instances from the normal Boxy
configuration and snapshots them into `service.yaml`; `--providers` may
filter that set by provider type. Without `--config`, the legacy
`--providers` list remains supported and creates zero-value provider configs.

The agent's `service.yaml` briefly holds the single-use bootstrap
`--token`: encrypted at rest with DPAPI (machine-scope) on Windows, and
protected by `0600` file permissions on Linux. Once the agent registers
successfully, the token is scrubbed from the file — it's single-use and
worthless after that point regardless.

## Single instance per host by default, named multi-instance via --instance-name

The default (unnamed) instance keeps the fixed names `boxy-agent` and
`boxy-serve` — `install` errors if one is already registered rather than
creating a second one. Pass `--instance-name <name>` to `install` to run
more than one agent or server side by side on the same host (e.g. to test
two provider configs at once): it produces a distinctly named service
(`boxy-agent-<name>` / `boxy-serve-<name>`) and, unless `--data-dir` /
`--boxy-dir` is also given, a distinctly named default data/state
directory (`.boxy-agent-<name>/` / `.boxy-<name>/`) so named instances
don't collide with the default instance or each other. `uninstall`,
`start`, `stop`, and `status` all take the same `--instance-name` (and
`--user`, if that's how the instance was installed) to target it.

`--instance-name` is restricted to letters, digits, and hyphens (max 32
characters) — it becomes a literal Windows service/task name, systemd
unit name, and directory-name suffix, so it needs to be safe everywhere
at once.

`uninstall --purge` refuses to delete a data/state directory that has no
`service.yaml` in it. Service names are host-global but data directories
are cwd-relative, so a mismatched `--instance-name`, wrong working
directory, or stale `--data-dir`/`--boxy-dir` override must fail loudly
instead of silently deleting whatever happens to exist at the computed
path.

## `boxy update` and installed services

`boxy update` replaces the binary in place but a running service keeps
whatever binary it already loaded in memory until restarted. After a
successful update, `boxy update` checks whether the default-named
`boxy-agent`/`boxy-serve` services (privileged or `--user`) are installed
and currently running, and restarts (stop then start) each one that is —
so you don't have to remember to do it manually. A service that's
installed but not currently running is left stopped; update doesn't start
it as a side effect.

This only covers the default (unnamed) instance — named `--instance-name`
instances aren't restarted automatically, since `svcmgr.Manager` has no
way to enumerate them. Restart those yourself:
`boxy agent service stop --instance-name <name> && boxy agent service
start --instance-name <name>` (same shape for `serve`).

Pass `--skip-service-restart` to disable the check entirely. A restart
failure (e.g. a permission issue) is reported as a warning, not a command
failure — the binary update itself already succeeded by that point.
