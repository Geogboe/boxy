# Boxy CLI UX

Single entrypoint: `boxy`. All functionality hangs off subcommands; no alternate binaries.

## Global Patterns

- Config: `--config <path>` (defaults to `./boxy.yaml` if present).
- Output: `--json` for machine-readable responses where supported.
- Logging: `BOXY_LOG_LEVEL=debug` or `--log-level debug`.
- Context: Commands are designed to mirror the REST API (when present) 1:1.

## Service Lifecycle (`boxy serve`)

- `boxy serve [--config path] [--pprof-addr :6060] [--grpc-addr :50051]`  
  Runs the control-plane service (allocators, schedulers, HTTP/gRPC frontends).

## Agents (`boxy agent ...`)

- `boxy agent serve --listen :50051 --providers <csv> [--use-tls] [--config agent.yaml]`  
  Start an agent that executes provisioning operations for its providers.
- `boxy agent status --agent-id <id> | --agent-addr <host:port>`  
  Quick health probe for a specific agent instance.
- `boxy agent install|uninstall|start|stop|restart|run`  
  Host-level lifecycle helpers (per OS scripts).

## Administration (`boxy admin ...`)

- PKI & identity:  
  - `boxy admin init-ca --output <dir>`  
  - `boxy admin issue-cert --ca-cert <path> --ca-key <path> --cn <name>`  
  - `boxy admin revoke-cert --cn <name>`  
  - `boxy admin generate-agent --providers <csv> --expires <dur>` (issues agent token/cert bundle)
- Tokens & users (for API/CLI auth):  
  - `boxy admin create-token --name <user> --expires <dur>`  
  - `boxy admin revoke-token --token <id>`  
  - `boxy admin list-tokens`
- Ops & diagnostics:  
  - `boxy admin health-check`  
  - `boxy admin export-logs --output <path>`  
  - `boxy admin cleanup-orphans [--dry-run]`

## Pools (`boxy pool ...`)

- `boxy pool ls` — list pools and readiness.
- `boxy pool stats <pool>` — counts by state (provisioned/ready/allocated).
- `boxy pool inspect <pool>` — detailed config + metadata.
- `boxy pool resources <pool>` — per-resource view.
- `boxy pool scale <pool> --min-ready <n> [--preheated <n>]` — adjust targets.
- `boxy pool recycle <pool> [--confirm]` — force refresh now.
- Maintenance: `boxy pool drain|refill <pool>`; create/start/stop/delete planned per roadmap.

## Sandboxes (`boxy sandbox ...`)

- `boxy sandbox create -p <pool:count>[,...] -d <duration> [-n name] [--json]`  
  Allocate resources and return sandbox details.
- `boxy sandbox ls` — list active sandboxes.
- `boxy sandbox get <id>` — describe sandbox (and resources).
- `boxy sandbox extend <id> -d <duration>` — bump expiration.
- `boxy sandbox destroy <id>` — tear down immediately.
- Planned: `boxy sandbox exec`, `boxy sandbox wait`, `boxy sandbox resources`.

## Admin vs Operator Surfaces

- Operators primarily use `boxy serve`, `boxy pool`, `boxy sandbox`.
- Platform admins handle PKI/tokens via `boxy admin` and agent lifecycle via `boxy agent`.

## Roadmap Notes

- HTTP/REST API will mirror these verbs; CLI will remain a thin client over that API.
- No additional entrypoints are planned—`boxy` stays the single binary.
