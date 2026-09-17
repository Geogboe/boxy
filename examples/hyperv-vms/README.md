# hyperv-vms

A real Hyper-V pool: 2 VMs of capacity, 1 kept preheated and ready, with
automatic per-sandbox network isolation
([ADR-0021](../../docs/adr/0021-network-isolation-driver-capability.md)).
Every sandbox created from `win-lab` gets its own Internal vSwitch + NAT —
two sandboxes on this host cannot reach each other, even though they come
from the same pool and the same template.

**Validated end to end on real Hyper-V hardware (wks01, 2026-09-17):**
three full create → exec → delete cycles, each one producing a distinctly
named per-sandbox vSwitch/NAT (`boxy-sb-<sandbox-id>`), a guest addressed
from its own `/29` segment (confirmed via `ipconfig` inside the guest),
successful command execution under the freshly rotated credential
(`whoami`), and complete cleanup with no leftover VM/switch/NAT after each
delete. The pool replenished back to `min_ready` between cycles every time.

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
   local Administrator password you set during OOBE — that is what you'll
   hand to `boxy pool set-guest-credential` below.

## Run it

```powershell
# From an elevated (Administrator) PowerShell:
.\serve.ps1
```

Leave that running. In a second terminal, build the CLI once
(`task build` from the repo root, or `go build -o boxy ./cmd/boxy`) and
point it at the daemon:

```powershell
$env:BOXY_SERVER = "https://127.0.0.1:9090"
$env:BOXY_API_INSECURE = "true"   # self-signed dev CA; use --ca-cert .boxy/ca.crt instead for anything longer-lived
```

**First-time setup — get an admin API key and configure the pool's guest
credential.** These only need to run once per fresh `.boxy/state.json`:

```powershell
boxy admin api-key bootstrap --name example-admin
# copy the printed "key: boxy_..." value -- shown once
boxy login --api-key boxy_...
"<the template's local Administrator password>" | boxy pool set-guest-credential win-lab --value -
```

`boxy login` stores the key in the OS keyring (Windows Credential Manager /
DPAPI). That requires a real interactive desktop logon session — it does
**not** work from a plain non-interactive SSH exec session or a
WMI-launched process on Windows (both lack a loadable user profile for
DPAPI); run it from an actual console/RDP session, or through an SSH
port-forward tunnel to a locally-run CLI, not `ssh host "boxy login ..."`
directly.

## Manual end-to-end validation

This loop exercises preheat replenishment, allocation, real command
execution inside an isolated guest, and teardown, repeated a few times to
catch anything that only shows up on a second or third cycle (a stale
segment, a resource the reconciler doesn't recycle, credentials that don't
rotate cleanly twice).

```powershell
# 0. Confirm the pool warmed up one VM before touching sandboxes.
boxy status

# Repeat the next four steps 3+ times:

# 1. Create a sandbox -- claims the preheated VM (or provisions one if none
#    is ready yet), applies its own network segment, rotates credentials.
#    --save-guest-cred is required for step 2 below to authenticate --
#    without it, `sandbox exec` fails with an opaque "provider execution
#    failed" (see #351: this should fail fast with a clearer message, but
#    doesn't yet).
boxy sandbox create -f demo.sandbox.yaml --save-guest-cred
# note the printed sandbox ID, e.g. sbx_XXXXXXXX

# 2. Use it for something real -- proves exec, not just "ready".
boxy sandbox exec sbx_XXXXXXXX -- whoami
boxy sandbox exec sbx_XXXXXXXX -- ipconfig
#    the printed address should be inside the segment's own /29 (10.250.x.x)
#    -- confirms allocation-time NetworkIsolator.AttachToSegment actually ran.

# 3. Tear it down.
boxy sandbox delete sbx_XXXXXXXX

# 4. Confirm the pool replenished back toward min_ready before the next
#    cycle, and that the VM/segment from step 1 are actually gone.
boxy status
Get-VM | Where-Object { $_.Name -like 'boxy-*' }
Get-VMSwitch | Where-Object { $_.Name -like 'boxy-sb-*' }
```

What to watch for across repeats, not just the first cycle:

- **A distinctly-named switch/NAT each time** (`boxy-sb-<sandbox-id>`) —
  confirmed present during step 2 and gone after step 3, every cycle.
- **`boxy status` returns to `min_ready` after each delete**, without
  manual intervention — this is the pool reconciler, not something the
  CLI commands above trigger directly. Expect a short window (a few
  reconcile ticks, ~10-20s) where it briefly reads 0 ready before the
  replacement VM finishes personalizing.
- **No leftover `Get-VM`/`Get-VMSwitch` entries** after step 3 on any
  cycle. A resource or segment stuck in a transient state (`recycling`,
  `destroying`) is visible via `GET /api/v1/resources` even mid-teardown —
  see AGENTS.md's Sandboxes notes — so a leftover after teardown completes
  is a real bug, not a timing artifact.
- **Credential rotation succeeds on every cycle, not just the first** —
  `sandbox exec ... whoami` authenticating at all in step 2 is the proof;
  a stale cached credential from a previous cycle would fail here
  specifically.

## What this does not cover

Everything above is single-host. Cross-host mesh peering
([ADR-0022](../../docs/adr/0022-cross-host-mesh-peering.md)) needs a
second physical Hyper-V host reachable from the first and is out of scope
for this example.
