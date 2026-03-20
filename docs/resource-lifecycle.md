# Resource Lifecycle

Resources move through two distinct phases: **supply side** (pool management) and
**demand side** (sandbox allocation). Credentials and connection details are only
generated when a resource is allocated — the pool carries only generic location info.

## Full Lifecycle Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│  SUPPLY SIDE  (pool.Manager + Provisioner)                          │
│                                                                     │
│  pool.Reconcile()                                                   │
│    │                                                                │
│    ├─ driver.Create(cfg)          provisioning                      │
│    │       │                           │                            │
│    │       └── resource born ──────► ready                         │
│    │              Properties: host, IP, type                        │
│    │              (generic — no credentials)                        │
│    │                                                                │
│    └─ driver.Delete(id)            ready ──► destroyed              │
│         (MaxAge exceeded or pool oversize)                          │
└─────────────────────────────────────────────────────────────────────┘
                         │
                         │  sandbox.Manager.AddFromPool() / CreateFromPool()
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  DEMAND SIDE  (sandbox.Manager + SandboxAllocator)                  │
│                                                                     │
│  1. Take() — move resource from pool inventory → sandbox            │
│  2. SandboxAllocator.Allocate() fires per resource                  │
│                                                                     │
│     container → {"access": "docker-exec",                           │
│                   "exec": "docker exec -it <name> /bin/sh"}         │
│                                                                     │
│     vm        → RSA keypair written to /tmp/boxy/key_<id>           │
│                  {"access": "ssh", "ssh_user": "admin",             │
│                   "ssh_key": "<path>",                              │
│                   "ssh_cmd": "ssh -i <path> admin@<host>"}          │
│                                                                     │
│     share     → random password generated                           │
│                  {"access": "smb", "username": "svc_boxy",          │
│                   "password": "<random>", "unc_path": "...",        │
│                   "mount_path": "..."}                              │
│                                                                     │
│  3. Extra properties merged into resource.Properties                │
│  4. Resource state → allocated (never returns to pool, ADR-0002)    │
└─────────────────────────────────────────────────────────────────────┘
                         │
                         │  sandbox create completes
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  ENV FILE OUTPUT                                                    │
│                                                                     │
│  .sandbox-<name>.env written to cwd (unless --no-env-file)          │
│  prompted interactively: [e] save  [enter] skip                     │
│                                                                     │
│  SANDBOX_ID=sbx_abc123                                              │
│  SANDBOX_WEB_1_HOST=10.0.0.5                                        │
│  SANDBOX_WEB_1_ACCESS=docker-exec                                   │
│  SANDBOX_WEB_1_EXEC=docker exec -it boxy-a1b2 /bin/sh              │
│                                                                     │
│  ⚠  Add to .gitignore — may contain credentials                     │
└─────────────────────────────────────────────────────────────────────┘
```

## State Transitions

```
provisioning → ready → allocated → released → destroying → destroyed
                                                 ▲
                              (MaxAge / manual delete)
```

`ready`: resource is in the pool, available for allocation.
`allocated`: resource has been assigned to a sandbox; allocation hooks have run.
`released`: sandbox has been deleted; resource awaits cleanup.
`destroying` / `destroyed`: driver.Delete() called / completed.

See [ADR-0002](adr/0002-no-resource-recycling.md) for why allocated resources
never return to `ready`.

## SandboxAllocator Interface

```go
type SandboxAllocator interface {
    Allocate(ctx context.Context, pool model.Pool, res model.Resource) (map[string]any, error)
}
```

Implemented by both `pool.DriverProvisioner` and `pool.AgentProvisioner`.
Returns extra Properties to merge; `nil, nil` means no allocation work needed.
