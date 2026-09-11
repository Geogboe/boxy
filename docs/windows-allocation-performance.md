# Windows allocation performance

Warmed allocation should not wait for the daemon's recovery ticker or start a
second PowerShell process inside an established PSRP runspace. Measure warmed
allocation and full refill separately with unchanged pool size and VM resources.

The implementation uses bounded, coalesced notifications after durable state
changes, retaining serialized reconciliation and periodic crash recovery.
Trusted provider scripts may run directly in PSRP with structured arguments;
executors without that optional capability retain the command fallback.

Credential rotation requires successful command completion, including checked
PowerShell errors. Personalization does not reconnect with the new password.
Fresh-login checks belong in live validation rather than each allocation.
Admission rotation, allocation-time networking, address checks,
single-use inventory, and quarantine on uncertain outcomes remain mandatory.
No credentials, deployment identifiers, or raw operational logs belong in
published benchmark evidence; report aggregate durations and sample counts.

Windows address assignment queries the networking CIM provider directly and
uses the built-in `netsh` command to replace the static IPv4 address and gateway.
This avoids loading the NetAdapter and NetTCPIP PowerShell command wrappers for
each fresh runspace. Native command failures stop personalization, and CIM
readback checks the address, prefix, address state, and requested gateway.
DNS assignment uses native `netsh` commands for each supplied address family,
preserving server order within IPv4 and IPv6 lists. Families absent from the
request remain unchanged, matching `Set-DnsClientServerAddress`; an empty list
leaves all existing DNS configuration alone. Command exit codes and a CIM
read-back check verify configuration without probing DNS server reachability.

## Measured behavior

A deployed networking candidate completed ten of ten measured warmed
allocations with a median of 9.28 seconds, compared with the original
31.41-second median across ten successful allocations and two timeouts.
These small, sequential samples demonstrate an observed allocation improvement;
they do not establish a reliability guarantee or isolate each change's effect.
Pool refill measurements overlapped other probes and do not establish a refill
speedup.

A separate small Go probe opened three fresh connections to an already-running
guest in 272–349 milliseconds. After a controlled guest reboot, its first
connection took 3.40 seconds: 2.76 seconds in transport/authentication setup and
0.64 seconds opening the PSRP pool. The next two fresh connections took 329 and
346 milliseconds. All nine commands in each run completed successfully.
Fresh connection cost therefore depends strongly on guest state; warmed
connection timings must not be presented as first-boot performance.

The PSRP dependency changes remove a fixed 500-millisecond pre-connect wait and
a fixed 1.5-second shutdown wait, release the socket before waiting for adapter
cleanup, and correct command acknowledgement flow control in the core library.
Live lifecycle probes completed repeated commands in milliseconds and shutdown
in approximately 0–1 milliseconds. The final lifecycle candidate also passed
live network configuration, fresh login with rotated credentials, script
argument fidelity, nonzero exit status, and silent-command checks. The allocation
sample above predates that final lifecycle candidate and is not a new benchmark
of its exact commit.

Cold guest endpoint connection still takes roughly two seconds before PSRP
initialization begins. This change does not eliminate that delay. SSH, WinRS,
guest prefetch/compression experiments, and experimental socket cancellation
changes are outside this implementation.
