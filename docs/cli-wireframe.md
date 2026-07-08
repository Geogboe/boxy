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
│   ├── --log-level debug|info|warn|error        Log verbosity (default info)
│   └── --log-file <path>                        Write structured logs to file
│
│   $ boxy serve
│     Boxy server running
│
│       Dashboard:  http://127.0.0.1:9090/
│       API:        http://127.0.0.1:9090/api/v1/
│       Health:     http://127.0.0.1:9090/healthz
│
│     Pools: 2 configured
│     Press Ctrl+C to stop
│
│   (with --ui=false, Dashboard line is omitted)
│
│
├── status                                     Check server health and summary
│   ├── --server <addr>                          Server address (default 127.0.0.1:9090)
│   └── --config <path>                          Config to resolve server address
│
│   $ boxy status
│     Server:     http://127.0.0.1:9090 (healthy)
│     Pools:      2 configured, 5 resources ready
│     Sandboxes:  1 active
│
│   $ boxy status  (server not running)
│     Error: cannot reach server at 127.0.0.1:9090
│     Is `boxy serve` running?
│
│
├── config
│   └── validate                               Validate config file and exit
│       └── --config <path>
│
│       $ boxy config validate
│         config OK
│
│
├── sandbox                                    Manage sandboxes
│   ├── --server <addr>                          Server address (default 127.0.0.1:9090)
│   │
│   ├── create -f <spec>                         Create sandbox from spec file
│   │   ├── -f, --file <path>                      Sandbox spec file (required)
│   │   └── --no-wait                              Return after request is accepted
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
│   │
│   │   $ boxy sandbox list
│   │     ID         NAME         STATUS       RESOURCES
│   │     sb-a1b2c3  pentest-lab  ready        3
│   │     sb-d4e5f6  warmup-lab   pending      0
│   │
│   ├── get <id>                                 Get sandbox details
│   │
│   │   $ boxy sandbox get sb-a1b2c3
│   │     {"id":"sb-a1b2c3","name":"pentest-lab","status":"ready",...}
│   │
│   ├── delete <id>                              Delete a sandbox
│   │   └── --no-wait                            Return after delete request is accepted
│   │
│   │   $ boxy sandbox delete sb-a1b2c3
│   │     deleted sandbox sb-a1b2c3
│   │
│   │   $ boxy sandbox delete sb-a1b2c3 --no-wait
│   │     accepted deletion of sandbox sb-a1b2c3
│   │
│   └── extend <id> <duration>                   Push a sandbox's auto-destroy expiry out
│                                                   (only works if policies.auto_destroy_after
│                                                    was set at creation; fails with 409 otherwise)
│
│       $ boxy sandbox extend sb-a1b2c3 15m
│         extended sandbox sb-a1b2c3, expires at 2026-07-08T14:00:00Z
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
│   │   ├── --server <addr>                      Server address (default 127.0.0.1:9090)
│   │   ├── drain <pool>                         Drain unused ready inventory
│   │   │   $ boxy debug pool drain win-vm
│   │   │     drained pool win-vm
│   │   └── fill <pool>                          Reconcile to configured min_ready
│   │       $ boxy debug pool fill win-vm
│   │         filled pool win-vm
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
└── agent                                      Manage remote agents and registration tokens
    ├── --server <addr>                          Server address (default 127.0.0.1:9090)
    │
    ├── token                                    Manage single-use registration tokens
    │   ├── create                                 Mint a token (raw value shown once, never stored)
    │   │   ├── --label <note>                       Optional operator note (e.g. target host)
    │   │   ├── --ttl <duration>                     Validity as a Go duration (default 1h)
    │   │   │
    │   │   $ boxy agent token create --label lab-hv-1 --ttl 2h
    │   │     token: 4f9c…e2a1
    │   │     id: 6b1f0e7c-…
    │   │     label: lab-hv-1
    │   │     expires: 2026-07-08T18:00:00Z
    │   │     The token is shown once and never stored — pass it to `boxy agent serve --token <token>` before it expires.
    │   │
    │   ├── list                                   List tokens (id, label, unused/used/expired, expiry)
    │   │   $ boxy agent token list
    │   │     6b1f0e7c-…	lab-hv-1	unused	expires 2026-07-08T18:00:00Z
    │   │
    │   └── revoke <id>                            Revoke an unredeemed token
    │       $ boxy agent token revoke 6b1f0e7c-…
    │         revoked token 6b1f0e7c-…
    │
    ├── list                                     List registered agents and availability
    │   $ boxy agent list
    │     0d9a…	lab-hv-1	[hyperv]	available
    │
    └── revoke <id>                              Revoke an agent's identity (deny-lists its mTLS cert
        │                                          and tears down any live connection)
        ├── --reason <text>                        Optional reason recorded with the revocation
        │
        $ boxy agent revoke 0d9a… --reason "host decommissioned"
          revoked agent 0d9a…


Global flags (on root command):
  --log-level debug|info|warn|error              (default info)
  --log-file <path>                              Write structured logs to file

Bundled skill notes:
  - Canonical skill copy lives at ~/.config/boxy/skills/boxy-cli on all platforms.
  - Agent-specific locations should point at that canonical copy, usually via symlink.
  - On Windows, skill install may fall back to a managed copy if symlinks are unavailable.


Output conventions:
  - Human-friendly text by default -> stdout
  - Structured slog logs -> stderr (or --log-file)
  - Errors -> stderr
```
