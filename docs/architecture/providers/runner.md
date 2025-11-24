# Runner Provider Design

## Purpose

Provide a compiled-in provider that mimics full Boxy lifecycle without external virtualization, enabling reliable dev/CI testing on both Linux and Windows.  
Note: this design was superseded by ADR-007 (Scratch Provider); helper packages referenced here are not implemented.

## Goals

- First-class Linux (`sh`) and Windows (`pwsh`) support. `os` is required (`linux`|`windows`|`auto`); agents skip pools whose `os` doesn’t match the host unless set to `auto`.
- Parity with other providers: provision → ready → allocate → destroy, hooks, preheating.  
- Safe-by-default containment: temp dirs, timeouts, output caps, best-effort network deny, env allowlist.  
- Keep provider interface unchanged; register under key `runner`.
- Process containment would require a small helper library (not implemented).

## Non-Goals

- Strong OS-level isolation or security boundary (future flags may add nsjail/bwrap/WDAC).  
- Remote “connect to existing Docker/Hyper-V” behavior (this is an in-host runner).

## Resource Model

- Resource = temp directory + runner context + log buffers.  
- Status: Provisioned (temp dir created) → Ready (preheat health check) → Allocated (hooks/commands) → Destroyed (kill processes, remove dir).  
- ConnectionInfo: local paths to temp dir and logs; future optional local socket for streaming.

## Configuration

```yaml
pools:
  - name: runner-linux
    type: runner
    os: linux            # required: linux | windows | auto (follow host)
    min_ready: 3
    preheating:
      enabled: true
      count: 2
    runner:
      mode: inproc         # future: subprocess
      allow_network: false
      max_cpu_time: 30s
      max_memory: 256Mi
      stdout_limit: 1Mi
      env_allowlist: ["PATH"]
    hooks:
      on_provision:
        - type: script
          shell: sh        # auto-switch to pwsh on Windows
          inline: "echo provision > state.txt"
      on_allocate:
        - type: script
          shell: sh
          inline: "echo allocate >> state.txt"
```

## Execution Model

- **Mode**: start with in-process execution. Context deadline enforces timeouts; process tree killed on expiry. Subprocess helper may be added later behind `mode: subprocess`. Keep implementation Go-native unless limits enforcement becomes too complex.  
- **Shells**: Linux uses `/bin/sh`; Windows uses `pwsh -NoLogo -NonInteractive -Command`.  
- **Limits**:  
  - Timeouts per hook/command.  
  - Stdout/stderr size caps with truncation marker.  
  - Memory/CPU: ulimit/cgroups on Linux; job objects on Windows (best-effort).  
  - Network: default deny; Linux may use bwrap/nsjail flag later; Windows TBD (firewall/WDAC).  
- **Environment**: empty by default, plus explicitly allowed vars; secrets scrubbed from logs (helper library TBD).

## Preheating Behavior

- Preheated resources create temp dirs, run a short health script (`echo ok`), and sit in Ready.  
- Idle temp dirs cleaned on pool shrink; refresh interval re-creates them to avoid drift.

## Hooks

- Support both `on_provision` and `on_allocate`.  
- Enforce per-hook timeout and output caps.  
- Failures propagate to allocator and mark resource unhealthy.

## Observability

- Per-resource logs in temp dir; truncation indicated.  
- Structured events emitted for provision/allocate/destroy timing and limit breaches.

## Testing Strategy

- Unit: runner timeouts, output caps, env allowlist, cleanup.  
- Integration: pool replenish + preheat + allocate + destroy across both OS runners (Windows in CI matrix if available, else marked optional).  
- E2E: boxyd + agent (runner) + simple sandbox request; verify hooks and cleanup.

## Open Questions

- Network enforcement on Windows: firewall rules vs WDAC vs none (document gap).  
- Should subprocess mode become default once helper exists?  
- ConnectionInfo socket support: useful for streaming logs or unnecessary complexity?
- Process containment library choice: would start with a small Go-only helper (env + dir + limits) if revived; external tools later only if needed for stricter isolation; keep the runner provider API stable.

## Future Work

- Optional strong isolation flag (`runner.isolation: nsjail|bwrap|wdac`).  
- Cached dependencies within preheated dirs for faster hook start.  
- Cross-platform log streaming over a local socket.
