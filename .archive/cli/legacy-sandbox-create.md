# Legacy Sandbox Create Flow

Archived during `#67 Rewrite sandbox create CLI with async API flow`.

## What the legacy path did

Before `#67`, `boxy sandbox create -f ...` performed sandbox creation locally in
the CLI process instead of delegating to the daemon:

1. Loaded the sandbox spec YAML.
2. Resolved a local `boxy.yaml`.
3. Opened a local DiskStore state file.
4. Registered providers and built drivers.
5. Started an embedded agent in the CLI.
6. Upserted pools into local state.
7. Mutated pool preheat policy to satisfy the request.
8. Reconciled pools locally to provision resources.
9. Allocated resources into a locally-created sandbox.
10. Reconciled pools again to replenish preheat targets.

## Why it was retired

- It bypassed the daemon entirely.
- It produced sandboxes that were not visible to `boxy serve`.
- It duplicated orchestration logic that now belongs to the daemon.
- It blocked the API-first migration tracked by `#33` / `#67`.

## What replaced it

The live create path is now:

1. Load sandbox spec YAML.
2. Query daemon pools.
3. Compile spec pools into async API `requests`.
4. `POST /api/v1/sandboxes`.
5. Poll until `ready` or `failed` unless `--no-wait` is used.
6. Fetch resources from the daemon and show connection info.
