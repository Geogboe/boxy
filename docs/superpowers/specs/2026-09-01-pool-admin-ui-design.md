# Pool administration dashboard (#287)

## Scope

The administrator Pools page is the operator view for pool capacity and safe
maintenance. Each pool shows its ready and total resource counts, effective
drain state, and resource rows grouped beneath the pool. The page links to
Diagnostics for detailed failures.

Drain and Fill are administrator-only POST actions. They use a session-bound
double-submit CSRF token, call the existing `PoolMaintenance` seam, and
redirect back to `/ui/pools` with a short result banner. There is no manual
recycle action: cleanup follows the explicit #292 purge contract.

## Cleanup workflow

The page offers a dry-run preview and an explicitly confirmed force-cleanup
action. Both are administrator-only POST actions protected by the same CSRF
token. Preview calls `ResourceCleanupService.Purge` with `DryRun: true`; force
calls it with `Force: true` and `DryRun: false`. Redirect banners contain
counts only, never provider properties, credentials, or resource payloads.

The server checks the role and CSRF token again for every mutation. Browser
confirmation is a usability guard, not an authorization boundary. Healthy
resources remain protected by the shared cleanup service.
