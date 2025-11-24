# workspacefs

Helpers for filesystem-backed scratch workspaces:

- Create and lay out resource directories
- Optional helper paths for connect/env artifacts
- Lightweight health checks (exists, rw, free space, required files)
- Cleanup helpers

Scope: facilitator utilities only; callers control what files they create and their semantics. No strong isolation is implied.
