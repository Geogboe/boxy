# hyperv-vms

A real Hyper-V pool: 2 VMs of capacity, 1 kept preheated and ready, with
automatic per-sandbox network isolation
([ADR-0021](../../docs/adr/0021-network-isolation-driver-capability.md)).
Every sandbox created from `win-lab` gets its own Internal vSwitch + NAT —
two sandboxes on this host cannot reach each other, even though they come
from the same pool and the same template.

This example needs a real Windows machine with Hyper-V enabled and
Administrator rights. It cannot be run on a machine without Hyper-V (WSL,
Linux, macOS, or a Windows box with Hyper-V disabled) — `boxy serve`'s
Hyper-V provider drives real `Get-VM`/`New-VM`/`New-NetNat` PowerShell
commands on the host it runs on.

## Preparing the template

`config.template_vhd` in `boxy.yaml` must point at a **generalized
(sysprepped)** VHDX — a template still mid-way through its own first-boot
out-of-box setup will make every cloned VM fail personalization
unpredictably; this was the most common real failure mode found validating
this feature on real hardware (see AGENTS.md's Hyper-V notes).

1. Install Windows into a VHDX from ISO, complete OOBE once, install updates.
2. Inside the guest, run `C:\Windows\System32\Sysprep\sysprep.exe
   /generalize /oobe /shutdown`. Wait for the VM to shut itself down —
   do **not** boot it again afterward; booting a generalized image
   un-generalizes it.
3. Copy the resulting VHDX to the path `template_vhd` names, and note the
   local Administrator password you set during OOBE — that is what
   `BOXY_EXAMPLE_HYPERV_PASSWORD` below must match.

## Run it

```powershell
# From an elevated (Administrator) PowerShell:
$env:BOXY_EXAMPLE_HYPERV_PASSWORD = "<the template's local Administrator password>"
.\serve.ps1
```

Leave that running. In a second terminal, build the CLI once
(`task build` from the repo root, or `go build -o boxy ./cmd/boxy`) and
point it at the daemon:

```powershell
$env:BOXY_SERVER = "http://127.0.0.1:9090"
```

## Manual end-to-end validation

This loop exercises preheat replenishment, allocation, real command
execution inside an isolated guest, and teardown, repeated a few times to
catch anything that only shows up on a second or third cycle (a stale
segment, a resource the reconciler doesn't recycle, credentials that don't
rotate cleanly twice).

```powershell
# 0. Confirm the pool warmed up one VM before touching sandboxes.
boxy status
boxy debug pool fill win-lab   # no-op if already at min_ready; harmless either way

# Repeat the next four steps 3+ times:

# 1. Create a sandbox -- claims the preheated VM (or provisions one if none
#    is ready yet), applies its own network segment, rotates credentials.
boxy sandbox create -f demo.sandbox.yaml
# note the printed sandbox ID, e.g. sb-XXXXXXXX

# 2. Use it for something real -- proves exec, not just "ready".
boxy sandbox exec sb-XXXXXXXX -- hostname
boxy sandbox exec sb-XXXXXXXX -- ipconfig
#    the printed address should be inside the segment's own /29 (10.250.x.x)
#    -- confirms allocation-time NetworkIsolator.AttachToSegment actually ran.

# 3. Tear it down.
boxy sandbox delete sb-XXXXXXXX

# 4. Confirm the pool replenished back toward min_ready before the next
#    cycle, and that the VM/segment from step 1 are actually gone.
boxy status
Get-VM | Where-Object { $_.Name -like 'boxy-*' }
Get-VMSwitch | Where-Object { $_.Name -like 'boxy-sb-*' }
```

What to watch for across repeats, not just the first cycle:

- **Different segment each time.** Each `sandbox create` should produce a
  differently-addressed guest (a fresh `/29` block) — if two consecutive
  sandboxes land on the same address, the segment ledger isn't releasing
  freed blocks.
- **`boxy status` returns to `min_ready` after each delete**, without
  manual intervention — this is the pool reconciler, not something the
  CLI commands above trigger directly.
- **No leftover `Get-VM`/`Get-VMSwitch` entries** after step 3 on any
  cycle. A resource or segment stuck in a transient state (`recycling`,
  `destroying`) is visible via `GET /api/v1/resources` even mid-teardown —
  see AGENTS.md's Sandboxes notes — so a leftover after teardown completes
  is a real bug, not a timing artifact.
- **Credential rotation succeeds on every cycle, not just the first** —
  `sandbox exec` authenticating at all in step 2 is the proof; a stale
  cached credential from a previous cycle would fail here specifically.

## What this does not cover

Everything above is single-host. Cross-host mesh peering
([ADR-0022](../../docs/adr/0022-cross-host-mesh-peering.md)) needs a
second physical Hyper-V host reachable from the first and is out of scope
for this example.
