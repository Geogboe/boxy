# Boxy CLI Wireframe

> **This is the canonical CLI reference.** All CLI changes should reference and
> update this document. If it's not in the wireframe, it shouldn't be in the CLI.

```
boxy
│
├── init                                       Create starter config in cwd
│   └── --force                                  Overwrite existing boxy.yaml
│
│   $ boxy init
│     Created boxy.yaml
│
│     Next steps:
│       1. Edit boxy.yaml to define your pools
│       2. boxy config validate     Validate your config
│       3. boxy serve               Start the daemon
│
│   $ boxy init
│     Error: boxy.yaml already exists (use --force to overwrite)
│
│
├── help
│   └── all                                    Print help for every command
│
│       $ boxy help all
│         # boxy
│         # boxy serve
│         # boxy sandbox create
│         # boxy skills install
│         ...
│
│
├── serve                                      Start the Boxy daemon
│   ├── --config <path>                          Config file (.yaml/.yml/.json)
│   ├── --listen <addr>                          HTTP listen address (default :9090)
│   ├── --ui true|false                          Enable web dashboard (default true)
│   ├── --insecure                                Disable REST and agent-gRPC TLS (local development only)
│   ├── --log-level debug|info|warn|error        Log verbosity (default info)
│   └── --log-file <path>                        Write structured logs to file
│
│   $ boxy serve
│     Boxy server running
│
│       Dashboard:  https://127.0.0.1:9090/
│       API:        https://127.0.0.1:9090/api/v1/
│       Health:     https://127.0.0.1:9090/healthz
│
│     Pools: 2 configured
│     Press Ctrl+C to stop
│
│   (with --ui=false, Dashboard line is omitted)
│
│   └── service                                 Install boxy serve as an OS-managed background service
│       │                                         (real service by default — Windows SCM / Linux systemd
│       │                                         system unit, requires an elevated process; --user for an
│       │                                         unprivileged Task Scheduler/systemd-user fallback)
│       ├── install                                Install and start the service
│       │   ├── --user                               Install the unprivileged fallback instead
│       │   ├── --instance-name <name>                Named instance -> boxy-serve-<name>, .boxy-<name>/
│       │   ├── --config <path>                       Config file for the service to run against
│       │   ├── --listen <addr>                       HTTP listen address (default :9090)
│       │   ├── --ui true|false                       Enable web dashboard (default true)
│       │   ├── --grpc-listen <addr>                  Agent gRPC listen address (default :9091)
│       │   ├── --grpc-cert-san <name>                 Extra SAN for the agent gRPC cert (repeatable)
│       │   └── --insecure                            Serve agent gRPC without TLS/mTLS (dev only)
│       │
│       │   $ boxy serve service install --config boxy.yaml
│       │     ✓ boxy-serve installed and started (config: .boxy/service.yaml, log: .boxy/service.log)
│       │
│       │   $ boxy serve service install --instance-name test1 --user --config test1.yaml
│       │     ✓ boxy-serve-test1 installed and started (config: .boxy-test1/service.yaml, log: .boxy-test1/service.log)
│       │
│       ├── uninstall                              Remove the installed service
│       │   ├── --instance-name <name>                Target a named instance (default: the unnamed instance)
│       │   ├── --user                                Target the --user instance (must match how it was installed)
│       │   ├── --purge                               Also remove the .boxy[-<name>]/ state directory
│       │   └── --boxy-dir <path>                     State dir to purge (default: resolved like `boxy serve`)
│       │
│       │   (--purge refuses to delete a directory that has no service.yaml in it — a mismatched
│       │    --instance-name/--boxy-dir fails loudly instead of silently deleting the wrong instance's state)
│       │
│       ├── start                                  Start the installed service
│       │   ├── --instance-name <name>
│       │   └── --user
│       │
│       ├── stop                                   Stop the installed service
│       │   ├── --instance-name <name>
│       │   └── --user
│       │
│       └── status                                 Show the installed service's status
│           ├── --instance-name <name>
│           └── --user
│
│           $ boxy serve service status
│             boxy-serve: running (system-service)
│           (Mode is system-service/user-task on Windows, system-unit/user-unit on Linux)
│
│           $ boxy serve service status --instance-name test1 --user
│             boxy-serve-test1: running (user-task)
│
│
├── status                                     Check server health and summary
│   ├── --server <addr>                          Server address (overrides env/global defaults)
│   └── --config <path>                          Config to resolve server address
│
│   $ boxy status
│     Server:     https://127.0.0.1:9090 (healthy)
│     Pools:      2 configured, 5 resources ready
│     Sandboxes:  1 active, 0 failed
│
│   $ boxy status  (server not running)
│     Error: cannot reach server at 127.0.0.1:9090
│     Is `boxy serve` running?
│
│
├── login                                      Store an operator API key in the OS keyring
│   ├── --server <url>                           Server URL (overrides env/global defaults)
│   ├── --api-key <key>                          Optional non-interactive key input
│   ├── --ca-cert <path>                         Trust a Boxy self-signed CA
│   ├── --insecure                               Skip HTTPS verification (development only)
│   ├── --oidc                                   Log in via the server's configured OIDC provider
│   │                                             (device-code grant) instead of a static API key
│   └── --web                                    With --oidc, use loopback-redirect (auto-launch
│                                                 a browser) instead of the default device-code grant
│
│   $ boxy login --server https://boxy.example.test:9090 --ca-cert .boxy/ca.crt
│     API key (hidden; Ctrl+C to cancel): ********
│     Logged in to https://boxy.example.test:9090
│
│   $ boxy login --server https://boxy.example.test:9090 --oidc
│     Open https://idp.example.test/device and enter code: ABCD-EFGH
│     Waiting for login to complete...
│     Logged in to https://boxy.example.test:9090 via OIDC
│
│
├── logout                                     Remove the stored operator credential
│   └── --server <url>                           Server URL (overrides env/global defaults)
│
│
├── admin                                      Manage operator access and shared state
│   ├── --server <url>                           Server URL (overrides env/global defaults)
│   ├── --ca-cert <path>                         Trust a Boxy self-signed CA
│   ├── --insecure                               Skip HTTPS verification (development only)
│   │
│   ├── api-key
│   │   ├── bootstrap [--name <name>]             First local administrator key; shown once
│   │   ├── create --role user|auditor|admin      Create a key; shown once
│   │   │   ├── --name <name>
│   │   │   └── --expires <duration>
│   │   ├── list                                  List key metadata, never raw keys
│   │   └── revoke <id>                            Revoke a key
│   │
│   └── bootstrap-password                        Show/clear the one-time web-UI local admin password
│       └── --config <path>                         Must match the config `boxy serve` was started with
│
│       $ boxy admin bootstrap-password
│         username: admin
│         password: <one-time generated password>
│         This password will not be shown again.
│
│   ├── pool                                     Run administrator pool maintenance
│   │   ├── drain <pool>                         Drain unused ready inventory
│   │   │     $ boxy admin pool drain win-vm
│   │   │       drained pool win-vm
│   │   └── fill <pool>                          Reconcile to configured min_ready
│   │         $ boxy admin pool fill win-vm
│   │           filled pool win-vm
│   │
│   └── resource                                 Run administrator resource maintenance
│       └── purge [--dry-run|--force]             Preview or force stale-resource cleanup
│             $ boxy admin resource purge --dry-run
│               resource purge: candidates=2 cleaned=0 skipped=4 errors=0 dry-run=true force=false
│
│
├── diagnostics                                Inspect safe administrator diagnostics
│   ├── --server <url>                           Server URL (overrides env/global defaults)
│   ├── --ca-cert <path>                         Trust a Boxy self-signed CA
│   ├── --insecure                               Skip HTTPS verification (development only)
│   │
│   └── logs                                    Query bounded, redacted diagnostic events
│       ├── --since <timestamp>                   RFC3339 lower time bound
│       ├── --level debug|info|warn|error         Exact level filter
│       ├── --component <name>                    Component filter
│       ├── --pool <name>                         Pool filter
│       ├── --agent <id>                          Agent filter
│       ├── --resource <id>                       Resource filter
│       ├── --limit <1-1000>                      Events per page (default 100)
│       ├── --cursor <value>                      Opaque pagination cursor
│       └── --format table|json                   Output format (default table)
│
│       $ boxy diagnostics logs --level error
│         2026-08-31T14:30:00Z  ERROR  reconciler  provider listing failed
│
│       Only administrators can query diagnostics. Raw credentials and signed
│       URL query values are redacted before events are persisted.
│
│   └── collect <agent-id>                      Pull retained logs once from a connected agent
│       ├── --since <timestamp>                   RFC3339 lower time bound for an incremental pull
│       └── --limit <1-1000>                      Maximum events (default 1000)
│
│
├── config                                      (alias: cfg)
│   ├── validate                               Validate config file and exit
│   │   └── --config <path>
│   └── client
│       ├── show                               Show the global CLI server default
│       └── set-server <url>                   Set the global CLI server default
│
│       $ boxy config validate
│         config OK
│
│       $ boxy config client set-server boxy.example:9090
│         server: https://boxy.example:9090
│
│
├── doctor                                    Check local state and secret backend readiness
│   ├── --config <path>                         Config file (.yaml/.yml/.json)
│   ├── --backend file|keyring|dpapi            Explicit backend override
│   ├── --path <path>                            Secret file path override for file/dpapi
│   └── --service <name>                         Keyring service name override
│
│   $ boxy doctor --config boxy.yaml
│     secret backend: dpapi (ready)
│     legacy plaintext credentials: none
│
│
├── migrate
│   └── secrets                                Explicitly migrate legacy pool credentials
│       ├── --config <path>
│       ├── --backend file|keyring|dpapi        Required unless configured in boxy.yaml
│       ├── --path <path>                       Secret file path override for file/dpapi
│       └── --service <name>                    Keyring service name override
│
│       $ boxy migrate secrets --config boxy.yaml --backend file --path .boxy/secrets.json
│         migrated pool win-vm
│
│       Migration is explicit and verified before the legacy plaintext value is removed.
│       Boxy never silently migrates or falls back between secret backends.
│
│
├── sandbox                                    Manage sandboxes (alias: sbx)
│   ├── --server <addr>                          Server address (overrides env/global defaults)
│   │
│   ├── create -f <spec>                         Create sandbox from spec file
│   │   ├── -f, --file <path>                      Sandbox spec file (required)
│   │   ├── --no-wait                              Return after request is accepted
│   │   └── --save-guest-cred                      Save one-time guest credentials in the OS keyring
│   │
│   │   $ boxy sandbox create -f lab.sandbox.yaml
│   │     Waiting for sandbox "pentest-lab"  sb-a1b2c3 · 3 resource(s)
│   │
│   │   $ boxy sandbox create -f lab.sandbox.yaml --no-wait
│   │     Sandbox accepted
│   │       id: sb-a1b2c3
│   │       name: pentest-lab
│   │       status: pending
│   │
│   ├── list                                     List all sandboxes
│   │   └── --format <json|table>                  Output format (default table)
│   │
│   │   $ boxy sandbox list
│   │     ID         NAME         STATUS       RESOURCES
│   │     sb-a1b2c3  pentest-lab  ready        3
│   │     sb-d4e5f6  warmup-lab   pending      0
│   │
│   │   $ boxy sandbox list --format json
│   │     [{"id":"sb-a1b2c3","name":"pentest-lab","status":"ready",...}]
│   │
│   ├── get <id>                                 Get sandbox details
│   │
│   │   $ boxy sandbox get sb-a1b2c3
│   │     {"id":"sb-a1b2c3","name":"pentest-lab","status":"ready",...}
│   │
│   ├── delete <id>                              Delete a sandbox
│   │   └── --no-wait                            Return after delete request is accepted
│   │                                               (also removes any --save-guest-cred keyring
│   │                                                entry for each of the sandbox's resources;
│   │                                                a cleanup failure prints a warning, it does
│   │                                                not fail the delete)
│   │
│   │   $ boxy sandbox delete sb-a1b2c3
│   │     1/3 resource(s) destroyed
│   │     2/3 resource(s) destroyed
│   │     3/3 resource(s) destroyed
│   │     deleted sandbox sb-a1b2c3
│   │
│   │   $ boxy sandbox delete sb-a1b2c3 --no-wait
│   │     accepted deletion of sandbox sb-a1b2c3
│   │
│   ├── extend <id> <duration>                   Push a sandbox's auto-destroy expiry out
│   │                                               (only works if policies.auto_destroy_after
│   │                                                was set at creation; fails with 409 otherwise)
│   │
│   │   $ boxy sandbox extend sb-a1b2c3 15m
│   │     extended sandbox sb-a1b2c3, expires at 2026-07-08T14:00:00Z
│   │
│   └── exec <id> [-- <command> [args...]]       Execute a durable one-shot command
│       ├── --resource <id>                        Required for multi-resource sandboxes
│       ├── --timeout <duration>                   Default 30s, maximum 5m
│       ├── --command <text>                       Send opaque command text
│       ├── --stdin                                Read opaque command text from stdin
│       ├── --script-file <path>                   Stage a local script; use `-- [args...]`
│       ├── --interpreter auto|powershell|sh       Select script interpreter (`auto` by default)
│       ├── --detach                               Submit and print the execution ID
│       ├── --attach <exec-id>                     Reconnect to an existing execution
│       ├── --events                               Write structured lifecycle/chunk NDJSON
│       ├── --buffered                             Wait for completion before writing output
│       └── --guest-password-stdin                 Read a guest password from stdin (never a flag value)
│
│       $ boxy sandbox exec sb-a1b2c3 -- hostname
│         sandbox output...
│       $ boxy sandbox exec sb-a1b2c3 -- @setup.ps1 --mode ci
│         sandbox output...
│       $ boxy sandbox exec sb-a1b2c3 --command "Get-Date" --detach
│         01J...
│       $ boxy sandbox exec sb-a1b2c3 --attach 01J...
│         sandbox output...
│
│
├── pool                                      Manage pools
│   ├── --server <url>                           Server URL (default 127.0.0.1:9090)
│   ├── --ca-cert <path>                         Trust a Boxy self-signed CA
│   ├── --insecure                               Skip HTTPS verification (development only)
│   │
│   └── set-guest-credential <pool> --value -    Set a pool bootstrap credential from stdin
│       │                                          (the raw value is never returned)
│       │
│       │   $ boxy pool set-guest-credential win-vm --value - < bootstrap.txt
│       │     guest bootstrap credential configured for pool win-vm
│
│
├── skills                                     Install bundled coding-agent skills
│   │
│   ├── install                                Install or refresh bundled skill assets
│   │   ├── --user                               Link skill into ~/.agents/skills (default target)
│   │   ├── --project                            Link skill into ./.agents/skills in cwd
│   │   ├── --path <dir>                         Additional directory to receive boxy-cli
│   │   └── --force                              Replace conflicting targets and refresh canonical copy
│   │
│   │   $ boxy skills install
│   │     Canonical: ~/.config/boxy/skills/boxy-cli
│   │     Linked: ~/.agents/skills/boxy-cli
│   │
│   │   $ boxy skills install --project --path ./.claude/skills
│   │     Canonical: ~/.config/boxy/skills/boxy-cli
│   │     Linked: ./.agents/skills/boxy-cli
│   │     Linked: ./.claude/skills/boxy-cli
│   │
│   └── uninstall                              Remove installed skill links/copies
│       ├── --user                               Remove ~/.agents/skills target (default target)
│       ├── --project                            Remove ./.agents/skills target in cwd
│       ├── --path <dir>                         Additional directory to remove boxy-cli from
│       └── --purge                              Also remove ~/.config/boxy/skills/boxy-cli
│
│       $ boxy skills uninstall
│         Removed: ~/.agents/skills/boxy-cli
│
│       $ boxy skills uninstall --purge
│         Removed: ~/.agents/skills/boxy-cli
│         Purged: ~/.config/boxy/skills/boxy-cli
│
│
├── debug
│   ├── pool                                   Run daemon-backed pool maintenance
│   │   ├── --server <addr>                      Server address (overrides env/global defaults)
│   │   ├── drain <pool>                         Drain unused ready inventory
│   │   │   $ boxy debug pool drain win-vm
│   │   │     drained pool win-vm
│   │   └── fill <pool>                          Reconcile to configured min_ready
│   │       $ boxy debug pool fill win-vm
│   │         filled pool win-vm
│   │
│   ├── resource                               Run resource maintenance
│   │   ├── --server <addr>                      Server address (overrides env/global defaults)
│   │   └── purge [--dry-run|--force]             Preview or force stale-resource cleanup
│   │       $ boxy debug resource purge --dry-run
│   │         resource purge: candidates=2 cleaned=0 skipped=4 errors=0 dry-run=true force=false
│   │
│   └── provider                               Exercise devfactory provider (devtools build tag only —
│       │                                        absent from release binaries; build with
│       │                                        `-tags devtools` to get this subcommand)
│       ├── --data-dir <path>                    (default .devfactory/)
│       ├── --profile container|vm|share         (default container)
│       ├── create [--label key=value ...]
│       ├── list
│       ├── get <id>
│       ├── exec <id> -- <cmd> [args...]
│       ├── set-state <id> <state>
│       └── delete <id>
│
│
├── agent                                      Manage remote agents and registration tokens
│   ├── --server <addr>                          Server address (overrides env/global defaults)
│   │
│   ├── serve                                    Run this host as a remote agent (dials the server's
│   │   │                                          gRPC listener, executes provider operations)
│   │   ├── --server <host:port>                   Server gRPC address (required; note: gRPC port,
│   │   │                                            default :9091 — not the REST port)
│   │   ├── --providers <list>                     Provider types this agent hosts (optional with --config)
│   │   ├── --config <path>                        Boxy config supplying provider instances (optional)
│   │   ├── --token <token>                        Single-use registration token (first connection only)
│   │   ├── --ca-cert <path>                       Server CA cert, required for the first (token)
│   │   │                                            connection unless --insecure
│   │   ├── --name <name>                          Agent name (default: hostname)
│   │   ├── --data-dir <path>                      Issued-credential dir (default .boxy-agent in cwd)
│   │   ├── --insecure                             Connect without TLS (local development only)
│   │   │
│   │   $ boxy agent serve --config boxy.yaml --server boxy.example.test:9091 \
│   │       --token 4f9c…e2a1 --ca-cert ./ca.crt
│   │     (runs until stopped; reconnects with backoff; after the first
│   │      registration the provider instances from boxy.yaml are used; pass
│   │      --providers to select a subset. After registration the issued mTLS
│   │      client cert in .boxy-agent/ is used and --token is no longer needed)
│   │
│   ├── service                                  Install boxy agent as an OS-managed background service
│   │   │                                          (real service by default — Windows SCM / Linux systemd
│   │   │                                          system unit, requires an elevated process; --user for an
│   │   │                                          unprivileged Task Scheduler/systemd-user fallback)
│   │   ├── install                                Install and start the service
│   │   │   ├── --user                               Install the unprivileged fallback instead
│   │   │   ├── --instance-name <name>                Named instance -> boxy-agent-<name>, .boxy-agent-<name>/
│   │   │   ├── --server, --providers, --config, --token,
│   │   │   │   --name, --ca-cert, --data-dir,
│   │   │   │   --insecure                            Same as `boxy agent serve` (above)
│   │   │
│   │   │   $ boxy agent service install --config boxy.yaml --server s:9091 \
│   │   │       --token 4f9c…e2a1 --ca-cert ./ca.crt
│   │   │     ✓ boxy-agent installed and started (config: .boxy-agent/service.yaml, log: .boxy-agent/service.log)
│   │   │
│   │   │   $ boxy agent service install --instance-name test1 --user --config boxy.yaml --server s:9091
│   │   │     ✓ boxy-agent-test1 installed and started (config: .boxy-agent-test1/service.yaml, log: .boxy-agent-test1/service.log)
│   │   │
│   │   ├── uninstall                              Remove the installed service
│   │   │   ├── --instance-name <name>                Target a named instance (default: the unnamed instance)
│   │   │   ├── --user                                Target the --user instance (must match how it was installed)
│   │   │   ├── --purge                               Also remove the .boxy-agent[-<name>]/ data directory
│   │   │   └── --data-dir <path>                     Data dir to purge (default: .boxy-agent[-<name>] in cwd)
│   │   │
│   │   │   (--purge refuses to delete a directory that has no service.yaml in it — a mismatched
│   │   │    --instance-name/--data-dir fails loudly instead of silently deleting the wrong instance's data)
│   │   │
│   │   ├── start                                  Start the installed service
│   │   │   ├── --instance-name <name>
│   │   │   └── --user
│   │   │
│   │   ├── stop                                   Stop the installed service
│   │   │   ├── --instance-name <name>
│   │   │   └── --user
│   │   │
│   │   └── status                                 Show the installed service's status
│   │       ├── --instance-name <name>
│   │       └── --user
│   │
│   │       $ boxy agent service status
│   │         boxy-agent: running (system-service)
│   │       (Mode is system-service/user-task on Windows, system-unit/user-unit on Linux)
│   │
│   │       $ boxy agent service status --instance-name test1 --user
│   │         boxy-agent-test1: running (user-task)
│   │
│   ├── token                                    Manage single-use registration tokens
│   │   ├── create                                 Mint a token (raw value shown once, never stored)
│   │   │   ├── --label <note>                       Optional operator note (e.g. target host)
│   │   │   ├── --ttl <duration>                     Validity as a Go duration (default 1h)
│   │   │   │
│   │   │   $ boxy agent token create --label lab-hv-1 --ttl 2h
│   │   │     token: 4f9c…e2a1
│   │   │     id: 6b1f0e7c-…
│   │   │     label: lab-hv-1
│   │   │     expires: 2026-07-08T18:00:00Z
│   │   │     The token is shown once and never stored — pass it to `boxy agent serve --token <token>` before it expires.
│   │   │
│   │   ├── list                                   List tokens (id, label, unused/used/expired, expiry)
│   │   │   $ boxy agent token list
│   │   │     6b1f0e7c-…	lab-hv-1	unused	expires 2026-07-08T18:00:00Z
│   │   │
│   │   └── revoke <id>                            Revoke an unredeemed token
│   │       $ boxy agent token revoke 6b1f0e7c-…
│   │         revoked token 6b1f0e7c-…
│   │
│   ├── list                                     List registered agents, connection liveness, and
│   │   │                                          availability
│   │   └── --format <json|table>                  Output format (default table)
│   │
│   │   $ boxy agent list
│   │     ID     NAME       PROVIDERS    CONNECTION   SCHEDULING    LAST HEARTBEAT
│   │     0d9a…  lab-hv-1   [hyperv]     connected    available     2026-08-21 14:30:00 UTC
│   │
│   ├── status <id>                              Show one agent's connection liveness, scheduling
│   │   │                                          eligibility, and reported capacity
│   │   └── --format <json|table>                  Output format (default table)
│   │
│   │   $ boxy agent status 0d9a…
│   │     ID               0d9a…
│   │     Name             lab-hv-1
│   │     Providers        [hyperv]
│   │     Connection       connected
│   │     Scheduling       available
│   │     Last heartbeat   2026-08-21 14:30:00 UTC
│   │     Capacity (hyperv)  4,096 MB free
│   │
│   └── revoke <id>                              Revoke an agent's identity (deny-lists its mTLS cert
│       │                                          and tears down any live connection)
│       ├── --reason <text>                        Optional reason recorded with the revocation
│       ├── --force-orphan-resources                Force-orphan resources still attributed to this
│       │                                          agent (never contacts the agent; use only when it
│       │                                          is permanently gone — see ADR-0005)
│       │
│       $ boxy agent revoke 0d9a… --reason "host decommissioned"
│         revoked agent 0d9a…
│       $ boxy agent revoke 0d9a… --reason "host decommissioned" --force-orphan-resources
│         revoked agent 0d9a… (resources force-orphaned)
│
│
└── update                                     Update boxy to the latest release
    ├── --check                                  Check for updates without installing
    ├── --version <ver>                          Install a specific version (e.g. v0.1.9)
    ├── --proxy <url>                             HTTP proxy URL (overrides HTTPS_PROXY env var)
    ├── --prerelease                              Consider prerelease/draft releases as "latest"
    │                                             (default: stable releases only)
    └── --skip-service-restart                    Don't check for/restart an installed
                                                  boxy-agent/boxy-serve service after updating

    $ boxy update
      ==> Checking for updates...
          Current version: v0.1.34
          Latest version:  v0.1.34
      ✓ Already up to date (v0.1.34)

    $ boxy update --check
      ==> Checking for updates...
          Current version: v0.1.34
          Latest version:  v0.1.35
      update available — run 'boxy update' to install

    $ boxy update  (every published release is prerelease-marked, no stable release exists)
      Error: check for updates: Geogboe/boxy: no stable release found (re-run with
      --prerelease to update to a prerelease build)

    $ boxy update --prerelease  (boxy-agent installed as a service and currently running)
      ==> Checking for updates...
          Current version: v0.1.34
          Latest version:  v0.1.35
      ==> Downloading boxy v0.1.35...
      ✓ boxy updated to v0.1.35
      ✓ restarted boxy-agent service

    (the restart check only covers the default-named boxy-agent/boxy-serve instances —
     named --instance-name instances, see `boxy agent service`, are not restarted
     automatically; --skip-service-restart disables the check entirely)


Global flags (on root command):
  --log-level debug|info|warn|error              (default info)
  --log-file <path>                              Write structured logs to file
  --print-curl                                   Print the curl(1) equivalent of each REST
                                                  request this command makes to stderr
                                                  (Authorization is redacted; see #237)

Bundled skill notes:
  - Canonical skill copy lives at ~/.config/boxy/skills/boxy-cli on all platforms.
  - Agent-specific locations should point at that canonical copy, usually via symlink.
  - On Windows, skill install may fall back to a managed copy if symlinks are unavailable.


Output conventions:
  - Human-friendly text by default -> stdout
  - Structured slog logs -> stderr (or --log-file)
  - Errors -> stderr
  - --print-curl lines -> stderr (alongside structured logs, not stdout)
```
