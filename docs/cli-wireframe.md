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
│   └── delete <id>                              Delete a sandbox
│
│       $ boxy sandbox delete sb-a1b2c3
│         deleted sandbox sb-a1b2c3
│
│
├── debug
│   └── provider                               Exercise devfactory provider
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
└── [future] agent                             Planned — not yet implemented
    ├── list                                     List agents and connection status
    ├── token create                             Create registration token
    └── revoke <id>                              Revoke an agent


Global flags (on root command):
  --log-level debug|info|warn|error              (default info)
  --log-file <path>                              Write structured logs to file


Output conventions:
  - Human-friendly text by default -> stdout
  - Structured slog logs -> stderr (or --log-file)
  - Errors -> stderr
```
