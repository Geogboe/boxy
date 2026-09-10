# Windows allocation performance

Warmed allocation should not wait for the daemon's recovery ticker or start a
second PowerShell process inside an established PSRP runspace. Measure warmed
allocation and full refill separately with unchanged pool size and VM resources.

The implementation uses bounded, coalesced notifications after durable state
changes, retaining serialized reconciliation and periodic crash recovery.
Trusted provider scripts may run directly in PSRP with structured arguments;
executors without that optional capability retain the command fallback.

Credential rotation still requires a separate connection authenticated with the
new credential. Admission rotation, allocation-time networking, address checks,
single-use inventory, and quarantine on uncertain outcomes remain mandatory.
No credentials, deployment identifiers, or raw operational logs belong in
published benchmark evidence; report aggregate durations and sample counts.
