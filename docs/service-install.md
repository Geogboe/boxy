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
# Agent — real service, requires elevation
boxy agent service install --server boxy-server:9091 --providers docker --token <token> --ca-cert ca.crt

# Agent — unprivileged fallback
boxy agent service install --user --server boxy-server:9091 --providers docker --token <token> --ca-cert ca.crt

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

The agent's `service.yaml` briefly holds the single-use bootstrap
`--token`: encrypted at rest with DPAPI (machine-scope) on Windows, and
protected by `0600` file permissions on Linux. Once the agent registers
successfully, the token is scrubbed from the file — it's single-use and
worthless after that point regardless.

## Single instance per host

v1 supports exactly one installed agent and one installed server per
host, under the fixed names `boxy-agent` and `boxy-serve`. `install`
errors if one is already registered rather than creating a second one.
Multi-instance support is tracked as a follow-up issue.
