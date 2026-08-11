# Design: `boxy agent service` / `boxy serve service`

Status: Approved for planning
Date: 2026-08-10

## Problem

`boxy agent serve` and `boxy serve` are long-running foreground processes. On
Windows in particular, keeping an agent alive across reboots or logouts means
someone has to remember to reopen a terminal and re-run the command — there's
no OS-managed way to have it start automatically. This is the main pain
point for Windows agent hosts (e.g. Hyper-V pool hosts), but the same gap
exists on Linux.

## Goal

Add first-class OS service lifecycle management for both `boxy agent serve`
and `boxy serve`, so either can be installed as a background process that
starts automatically and survives logout/reboot, on Windows and Linux.

## Non-goals (v1)

- **macOS/launchd support.** This design hand-rolls per-OS service
  integration rather than using a cross-platform abstraction library (see
  "Library choice" below), which means macOS needs its own real
  implementation, not a checkbox. It is explicitly deferred and tracked as a
  follow-up issue (see "Follow-ups"). `service install` must fail with a
  clear, explicit error on darwin — it must never silently no-op or attempt
  a best-effort install that doesn't actually work.
- Named multi-instance support (running more than one agent on the same
  host under this mechanism). Single fixed-name instance only for v1.
- `boxy update` automatically detecting and restarting an installed service.

Both are tracked as follow-up issues, not silently dropped.

## Library choice

Hand-rolled per-OS integration, not a cross-platform service-manager library
(e.g. `kardianos/service`). Rationale: the candidate library is effectively
in maintenance mode, and the project's dependency philosophy prefers
well-maintained libraries or, when the trade-off is close, first-party/
standard tooling over a third-party abstraction. `golang.org/x/sys/windows/
svc` and `svc/mgr` (used for the real Windows Service path) are official
`golang.org/x` packages, not third-party. The Task Scheduler path shells out
to `schtasks.exe`, which ships on every Windows install — no COM wrapper
dependency. The Linux path shells out to `systemctl`/`loginctl` and writes
unit files directly.

## Command surface

Parallel subtrees under the existing `agent` and `serve` command groups,
sharing a common internal implementation:

```
boxy agent service install [--user] --server ... --providers ... [--token ...] ...
boxy agent service uninstall [--purge]
boxy agent service start
boxy agent service stop
boxy agent service status

boxy serve service install [--user] --config ... --listen ...
boxy serve service uninstall [--purge]
boxy serve service start
boxy serve service stop
boxy serve service status
```

- `install` accepts the same flags as the `serve`/`agent serve` command it
  wraps (not a `--` passthrough) — `boxy agent service install --server
  host:9091 --providers docker` mirrors `boxy agent serve --server
  host:9091 --providers docker`.
- Default is the privileged/real service route: Windows Service via SCM,
  Linux system unit under `/etc/systemd/system`. `install` checks privilege
  up front and fails fast with a clear message ("re-run as Administrator" /
  "re-run with sudo, or pass --user") rather than partially installing.
- `--user` opts into the unprivileged fallback: Windows → a Task Scheduler
  at-logon task; Linux → a user systemd unit plus `loginctl enable-linger`.
- Single instance per host for v1: fixed service/task/unit name (`boxy-agent`,
  `boxy-serve`). `install` errors if one is already registered rather than
  creating a second one.

## Architecture

New package `internal/svcmgr`, with platform-specific files behind Go build
tags (`_windows.go`, `_linux.go`) implementing a shared interface:

```go
type Manager interface {
    Install(cfg ServiceConfig) error
    Uninstall(name string, purge bool) error
    Start(name string) error
    Stop(name string) error
    Status(name string) (Status, error)
}
```

`ServiceConfig` carries the resolved (absolute) invocation: executable path,
service/unit name, description, the args needed to relaunch, and the
resolved log file path.

### Path resolution at install time

A service has no predictable working directory — SCM starts services in
`system32`, systemd's default working directory is `/`. `install` therefore
resolves every relative path it's given (`--data-dir`, `--config`,
`--ca-cert`, the log file path) to an absolute path **before** writing them
into the persisted service config. Nothing in the service invocation may
depend on the process's cwd at runtime.

### Persisted service config

`install` writes a small YAML file capturing the resolved flags, placed next
to that process's existing state directory: `.boxy-agent/service.yaml` for
the agent (alongside its credential files), and `<dir>/.boxy/service.yaml`
for `serve` (alongside `state.json`, using the same directory resolution as
`serveStatePath`). A new `--service-config <path>` flag on both `agent
serve` and `serve` loads opts from this file instead of requiring them all
on the command line at boot; the installed service/task/unit invocation is
simply:

```
<exe> agent serve --service-config <path>
```

(and the `serve` equivalent).

### Secret handling

This section applies to the **agent's** service config only — `serve`'s
config has no bootstrap-token equivalent, so its service config file is
never sensitive and needs no encryption/scrubbing.

The only sensitive field in the persisted agent service config is `token`
(the
single-use registration token), and only until first successful
registration — the server burns it on use, and the long-lived credential
(the issued client cert/key) is already handled by the existing
`persistAgentCredentials` path with `0600` file permissions.

- **Windows**: `token` is encrypted with DPAPI machine-scope
  (`CRYPTPROTECT_LOCAL_MACHINE`) before being written to the service config
  file. Machine-scope (rather than user-scope) matches a service, which may
  run as `LocalSystem` or a dedicated service account rather than an
  interactive user profile.
- **Linux**: the service config file is written `0600`, owned by the unit's
  `User=`.
- **Both platforms**: once `OnRegistered` fires and the client cert is
  persisted, the running process overwrites `token` in the service config
  with an empty value — nothing sensitive lingers past the bootstrap
  connection.

## Windows implementation

- **Real service** (default; requires an elevated/Administrator install):
  `golang.org/x/sys/windows/svc/mgr` registers the service with SCM. The
  process itself detects `svc.IsWindowsService()` at startup and, if true,
  wraps `runAgentServe`/`runServe` in a `svc.Handler`
  (`golang.org/x/sys/windows/svc`) that translates SCM stop/shutdown
  control requests into context cancellation.
- **Task Scheduler fallback** (`--user`; no admin required): shells out to
  `schtasks.exe /create /tn boxy-agent /tr "<exe> agent serve
  --service-config <path>" /sc onlogon /rl limited /f`, with hidden-window
  and restart-on-failure settings configured via the same `schtasks`
  invocation.
  - This is a real behavioral difference from the SCM path: it starts at
    **user logon**, not at machine boot before any login. Both the design
    and the `--user` flag's CLI help text state this explicitly so it's
    never mistaken for a true boot-time service.

## Linux implementation

- **System unit** (default; requires root): writes
  `/etc/systemd/system/boxy-agent.service`, then runs `systemctl
  daemon-reload && systemctl enable --now boxy-agent`.
- **User unit** (`--user`): writes `~/.config/systemd/user/boxy-agent.
  service`, runs `systemctl --user daemon-reload && systemctl --user enable
  --now boxy-agent`, then `loginctl enable-linger $USER` so the unit starts
  at boot without an active login session.
  - `enable-linger` can be polkit-restricted on some hardened/managed
    hosts; `install` surfaces this in its output rather than failing
    silently if linger can't be enabled.

## Uninstall / status

- `uninstall` stops and deregisters (removes the SCM entry / scheduled task
  / unit file) but leaves `--data-dir` (credentials, state) untouched by
  default, matching boxy's existing non-destructive conventions elsewhere.
  `--purge` additionally removes the data directory.
- `status` reports running/stopped, which mode is installed (service vs.
  task; system vs. user unit), and the resolved log file path.

## Logging

`install` always resolves and forces a `--log-file` path under the (now
absolute) data directory, consistent with the existing `--log-file` flag
and `serve.go`'s existing behavior of silencing the interactive `pterm` UI
and switching to `slog` when `--log-file` is set — a service has no
attached terminal, so the interactive UI path is never appropriate here.

## Testing

- `internal/svcmgr` gets unit tests per platform behind build tags, using
  fakes for the OS-level calls so tests don't require real admin/root.
- Command-layer tests (`agent service install/uninstall/...`) inject a fake
  `Manager`, following the same pattern as `update_test.go`'s injectable
  updater.
- Real SCM install, Task Scheduler, and systemd install are best exercised
  in a manual/integration pass rather than CI — GitHub Actions runners have
  their own constraints here (e.g. no real systemd PID 1 in some
  containers), consistent with testing CI/GoReleaser-adjacent changes
  locally before pushing.

## Follow-ups (separate issues, not in this spec)

1. macOS/launchd support.
2. Named multi-instance support (multiple agents registered as services on
   one host).
3. `boxy update` detecting and restarting an installed service
   automatically.
