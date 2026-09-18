# Overlay Network Fabric — Driver-Native Isolation (Plan 1a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Hyper-V and Docker drivers the ability to create/attach/destroy a per-sandbox private network segment, as a standalone, directly-testable driver capability — no wiring into sandbox allocation yet (that's Plan 1b).

**Architecture:** A new optional `providersdk` capability, `NetworkIsolator`, detected by type assertion like every other optional capability in that package (`NetworkRangeReporter`, `GuestPersonalizer`, `AvailabilityReporter`). Hyper-V implements it with a dedicated `Internal` vSwitch + `New-NetNat` per sandbox, using a small persisted subnet-allocation ledger (via `pkg/diskjson`, same primitive `devfactory`'s own store already uses) to avoid CIDR collisions between concurrently-created segments on one host. Docker implements it with a dedicated bridge network per sandbox, letting the Docker daemon auto-assign the subnet (no ledger needed — Docker's own default address pools already avoid collisions).

**Tech Stack:** Go 1.25, PowerShell (Hyper-V host management, existing `psExec`/`ps`/`psq` seam), Docker Engine API client (existing `dockerClient` interface), `pkg/diskjson`.

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan implements Decision 1's driver capability only (`NetworkIsolator`, Hyper-V + Docker implementations). It does **not** implement fulfiller/allocation wiring, remote-agent (gRPC) forwarding, or devfactory (deliberately not implemented — see the spec's Decision 1).

## Global Constraints

- Every sandbox gets isolation automatically — no opt-in flag exists anywhere in this capability (spec Decision 1).
- `AttachToSegment` must be fast — a single lightweight reconnect operation. Do not add retries-with-backoff, polling, or anything that could make sandbox readiness noticeably slower.
- Hyper-V segments use `Internal` switch type, never `Private` — `Private` would also cut off internet access, which is the wrong default until egress policy exists (spec Decision 1, Non-goals).
- Egress on both drivers is unrestricted NAT — do not add any allow-list/filtering logic; that's explicitly out of scope (spec Non-goals).
- devfactory does **not** implement `NetworkIsolator` in this plan (spec Decision 1) — do not add it.
- Follow existing driver conventions exactly: Hyper-V's `d.ps(ctx, script)` / `psq()` quoting seam, Docker's `dockerClient` interface + injected mock pattern. Do not introduce a second PowerShell-execution or Docker-client-access path.

---

### Task 1: `providersdk.NetworkIsolator` capability + `SegmentRef` type

**Files:**
- Create: `pkg/providersdk/network_isolation.go`
- Test: `pkg/providersdk/network_isolation_test.go`

**Interfaces:**
- Produces: `providersdk.SegmentRef` (a `string`), `providersdk.NetworkIsolator` interface with `CreateSegment(ctx context.Context, sandboxID string) (SegmentRef, error)`, `AttachToSegment(ctx context.Context, providerResourceID string, ref SegmentRef) error`, `DestroySegment(ctx context.Context, ref SegmentRef) error`.

This task only defines the interface and a compile-time capability-detection helper, mirroring `networkrange.go`'s shape exactly. There is no driver logic here — just the contract every implementation (Tasks 2 and 4) satisfies, and a test proving the type-assertion pattern works the way every other optional capability in this package already does.

- [ ] **Step 1: Write the failing test**

```go
// pkg/providersdk/network_isolation_test.go
package providersdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// fakeIsolatingDriver satisfies providersdk.Driver minimally (only what the
// type assertion below needs to compile) plus providersdk.NetworkIsolator,
// to prove a driver can be detected as a NetworkIsolator by type assertion —
// the same pattern every other optional capability in this package uses.
type fakeIsolatingDriver struct{ createCalls int }

func (f *fakeIsolatingDriver) Type() providersdk.Type { return "fake" }
func (f *fakeIsolatingDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Delete(ctx context.Context, id string) error { return nil }
func (f *fakeIsolatingDriver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	return nil, nil
}

func (f *fakeIsolatingDriver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	f.createCalls++
	return providersdk.SegmentRef("seg-" + sandboxID), nil
}
func (f *fakeIsolatingDriver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	return nil
}
func (f *fakeIsolatingDriver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	return nil
}

func TestNetworkIsolator_DetectedByTypeAssertion(t *testing.T) {
	var d providersdk.Driver = &fakeIsolatingDriver{}
	isolator, ok := d.(providersdk.NetworkIsolator)
	if !ok {
		t.Fatal("driver implementing NetworkIsolator's methods was not detected via type assertion")
	}
	ref, err := isolator.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "seg-sb-1" {
		t.Fatalf("ref = %q, want %q", ref, "seg-sb-1")
	}
}

func TestNetworkIsolator_NonImplementingDriverFailsAssertion(t *testing.T) {
	var d providersdk.Driver = &nonIsolatingDriver{}
	if _, ok := d.(providersdk.NetworkIsolator); ok {
		t.Fatal("a driver with no NetworkIsolator methods must not satisfy the interface")
	}
}

type nonIsolatingDriver struct{}

func (nonIsolatingDriver) Type() providersdk.Type { return "fake" }
func (nonIsolatingDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	return nil, nil
}
func (nonIsolatingDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (nonIsolatingDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (nonIsolatingDriver) Delete(ctx context.Context, id string) error                    { return nil }
func (nonIsolatingDriver) Allocate(ctx context.Context, id string) (map[string]any, error) { return nil, nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/providersdk/... -run TestNetworkIsolator -v`
Expected: compile failure — `providersdk.NetworkIsolator`, `providersdk.SegmentRef` undefined.

- [ ] **Step 3: Write the interface**

```go
// pkg/providersdk/network_isolation.go
package providersdk

import "context"

// SegmentRef is an opaque, provider-defined identifier for a per-sandbox
// network segment (a switch name for Hyper-V, a network ID for Docker).
// Callers never interpret it — they hold onto whatever CreateSegment
// returns and pass it back to AttachToSegment/DestroySegment unchanged.
type SegmentRef string

// NetworkIsolator is an optional provider capability for per-sandbox
// network isolation. Not every driver implements it — callers that care
// must type-assert, the same pattern as AvailabilityReporter,
// ResourceLister, GuestPersonalizer, and NetworkRangeReporter.
//
// Segment creation cannot happen at Driver.Create time: Boxy provisions
// resources into a pool's ready inventory ahead of any sandbox (preheat),
// and a sandbox later claims an already-created resource via a separate
// allocation step. CreateSegment is therefore called once, on a sandbox's
// first resource claim; AttachToSegment moves each claimed resource off
// whatever pool-level network it was created on and onto the sandbox's
// segment, at allocation time. AttachToSegment must be fast — a single
// lightweight reconnect, not a provisioning-style operation — because
// sandboxes need to be ready near-instantly.
type NetworkIsolator interface {
	// CreateSegment creates a new, empty private network segment for the
	// given sandbox. Called once per sandbox, on its first resource claim.
	CreateSegment(ctx context.Context, sandboxID string) (SegmentRef, error)

	// AttachToSegment moves an already-created resource (identified by the
	// driver's own provider-specific resource ID, as returned in
	// Resource.ID from Driver.Create) onto the given segment.
	AttachToSegment(ctx context.Context, providerResourceID string, ref SegmentRef) error

	// DestroySegment tears down a segment created by CreateSegment. Must be
	// idempotent for an already-gone segment, matching Driver.Delete's
	// idempotency contract.
	DestroySegment(ctx context.Context, ref SegmentRef) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/providersdk/... -run TestNetworkIsolator -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/providersdk/network_isolation.go pkg/providersdk/network_isolation_test.go
git commit -m "feat(providersdk): add NetworkIsolator optional driver capability

Closes part of #224."
```

---

### Task 2: Hyper-V `NetworkIsolator` implementation

**Files:**
- Create: `pkg/providersdk/providers/hyperv/network_isolation.go`
- Test: `pkg/providersdk/providers/hyperv/network_isolation_test.go`
- Modify: `pkg/providersdk/providers/hyperv/driver.go:33-` (add two new `Driver` fields: `segmentLedgerPath string` — resolved the same way `Config.DataDir` already is via `RelativePathResolver`, see Step 3 below — and no new mutex; `pkg/diskjson.Store` already guards its own concurrent access)
- Modify: `pkg/providersdk/providers/hyperv/config.go` (add `Config.DataDir`-relative segment ledger path resolution — see Step 3)

**Interfaces:**
- Consumes: `providersdk.SegmentRef` (Task 1), `d.ps(ctx, script) (string, error)` and `psq(s string) string` (existing, `driver.go`/`powershell.go`), `d.vmNameFromID(ctx, id) (string, error)` (existing, `driver.go:1641`), `diskjson.New[T](path string, newFunc func() T) *diskjson.Store[T]` and `(*Store[T]).Update(fn func(T) (T, error)) (T, error)` (existing, `pkg/diskjson`).
- Produces: `(*hyperv.Driver)` satisfies `providersdk.NetworkIsolator`.

This is the biggest task in this plan — it has its own persisted allocation ledger to avoid subnet collisions between concurrently-created segments on one host. Work through it in the four sub-steps below; each is its own red/green cycle, but all land in one commit since they're one cohesive feature.

- [ ] **Step 1: Write the failing test for the subnet ledger (pure allocation logic, no PowerShell)**

```go
// pkg/providersdk/providers/hyperv/network_isolation_test.go
package hyperv

import (
	"path/filepath"
	"testing"
)

func TestSegmentLedger_AllocateCIDR_FirstFitNoCollision(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)

	first, err := ledger.allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate sb-1: %v", err)
	}
	second, err := ledger.allocate("sb-2")
	if err != nil {
		t.Fatalf("allocate sb-2: %v", err)
	}
	if first.CIDR == second.CIDR {
		t.Fatalf("two sandboxes got the same CIDR: %q", first.CIDR)
	}
	if first.CIDR != "10.250.0.0/29" {
		t.Fatalf("first allocation = %q, want the base range's first /29", first.CIDR)
	}
	if second.CIDR != "10.250.0.8/29" {
		t.Fatalf("second allocation = %q, want the next /29", second.CIDR)
	}
}

func TestSegmentLedger_AllocateCIDR_IdempotentForSameSandbox(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)

	first, err := ledger.allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	again, err := ledger.allocate("sb-1")
	if err != nil {
		t.Fatalf("re-allocate: %v", err)
	}
	if first.CIDR != again.CIDR {
		t.Fatalf("re-allocating the same sandbox changed its CIDR: %q -> %q", first.CIDR, again.CIDR)
	}
}

func TestSegmentLedger_Release_FreesCIDRForReuse(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)

	first, err := ledger.allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if err := ledger.release("sb-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	reused, err := ledger.allocate("sb-2")
	if err != nil {
		t.Fatalf("allocate sb-2: %v", err)
	}
	if reused.CIDR != first.CIDR {
		t.Fatalf("released CIDR was not reused: got %q, want %q", reused.CIDR, first.CIDR)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run TestSegmentLedger -v`
Expected: compile failure — `newSegmentLedger` undefined.

- [ ] **Step 3: Implement the segment ledger and the driver methods**

```go
// pkg/providersdk/providers/hyperv/network_isolation.go
package hyperv

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/Geogboe/boxy/pkg/diskjson"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// segmentAllocation is one sandbox's assigned switch/NAT identity, persisted
// so restarts don't lose track of which CIDRs are in use.
type segmentAllocation struct {
	SwitchName string `json:"switch_name"`
	CIDR       string `json:"cidr"`
	Gateway    string `json:"gateway"`
}

type segmentLedgerState struct {
	// NextIndex is the next /29 block offset (0-based) to hand out from
	// segmentBaseCIDR. Only ever increases while entries exist; a released
	// CIDR is tracked in FreedIndexes and reused before NextIndex advances,
	// so long-running hosts don't walk the whole range needlessly.
	NextIndex     int            `json:"next_index"`
	FreedIndexes  []int          `json:"freed_indexes,omitempty"`
	BySandboxID   map[string]segmentAllocation `json:"by_sandbox_id"`
}

// segmentBaseCIDR is the private range this driver carves per-sandbox /29
// blocks (8 addresses: network, gateway, up to 5 usable hosts, broadcast —
// enough for a small sandbox lab) out of. Chosen from RFC 1918 space
// unlikely to collide with an operator's own LAN (10.250.0.0/16 is well
// outside common home/office 10.0.0.0/8 allocations that start near
// 10.0.x.x or 10.1.x.x).
const segmentBaseCIDR = "10.250.0.0/16"

type segmentLedger struct {
	store *diskjson.Store[segmentLedgerState]
}

func newSegmentLedger(path string) *segmentLedger {
	return &segmentLedger{
		store: diskjson.New(path, func() segmentLedgerState {
			return segmentLedgerState{BySandboxID: make(map[string]segmentAllocation)}
		}),
	}
}

// allocate returns the sandbox's existing CIDR/gateway if one is already
// recorded (idempotent — a retried CreateSegment call must not hand out a
// second block), or carves and persists a new /29 otherwise.
func (l *segmentLedger) allocate(sandboxID string) (segmentAllocation, error) {
	state, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		if s.BySandboxID == nil {
			s.BySandboxID = make(map[string]segmentAllocation)
		}
		if existing, ok := s.BySandboxID[sandboxID]; ok {
			return s, nil
		}
		index := s.NextIndex
		if n := len(s.FreedIndexes); n > 0 {
			index = s.FreedIndexes[n-1]
			s.FreedIndexes = s.FreedIndexes[:n-1]
		} else {
			s.NextIndex++
		}
		cidr, gateway, err := blockForIndex(index)
		if err != nil {
			return s, err
		}
		s.BySandboxID[sandboxID] = segmentAllocation{
			SwitchName: switchNameForSandbox(sandboxID),
			CIDR:       cidr,
			Gateway:    gateway,
		}
		return s, nil
	})
	if err != nil {
		return segmentAllocation{}, err
	}
	return state.BySandboxID[sandboxID], nil
}

func (l *segmentLedger) release(sandboxID string) error {
	_, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		if s.BySandboxID == nil {
			return s, nil
		}
		if _, ok := s.BySandboxID[sandboxID]; !ok {
			return s, nil
		}
		// Recover the index from the CIDR to free it for reuse: the block
		// size is fixed (/29 = 8 addresses), so the offset from
		// segmentBaseCIDR's base address, divided by 8, is the index.
		alloc := s.BySandboxID[sandboxID]
		idx, err := indexForBlock(alloc.CIDR)
		if err == nil {
			s.FreedIndexes = append(s.FreedIndexes, idx)
		}
		delete(s.BySandboxID, sandboxID)
		return s, nil
	})
	return err
}

// blockForIndex computes the index-th /29 block within segmentBaseCIDR,
// returning its network CIDR and the first usable address (used as the
// switch's gateway/host-side IP).
func blockForIndex(index int) (cidr string, gateway string, err error) {
	_, base, parseErr := net.ParseCIDR(segmentBaseCIDR)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse segmentBaseCIDR: %w", parseErr)
	}
	ip4 := base.IP.To4()
	if ip4 == nil {
		return "", "", fmt.Errorf("segmentBaseCIDR %q is not IPv4", segmentBaseCIDR)
	}
	offset := uint32(index) * 8
	baseInt := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
	blockStart := baseInt + offset
	blockIP := net.IPv4(byte(blockStart>>24), byte(blockStart>>16), byte(blockStart>>8), byte(blockStart))
	gatewayInt := blockStart + 1
	gatewayIP := net.IPv4(byte(gatewayInt>>24), byte(gatewayInt>>16), byte(gatewayInt>>8), byte(gatewayInt))
	return fmt.Sprintf("%s/29", blockIP.String()), gatewayIP.String(), nil
}

func indexForBlock(cidr string) (int, error) {
	_, block, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	_, base, err := net.ParseCIDR(segmentBaseCIDR)
	if err != nil {
		return 0, err
	}
	blockIP4, baseIP4 := block.IP.To4(), base.IP.To4()
	if blockIP4 == nil || baseIP4 == nil {
		return 0, fmt.Errorf("non-IPv4 CIDR")
	}
	blockInt := uint32(blockIP4[0])<<24 | uint32(blockIP4[1])<<16 | uint32(blockIP4[2])<<8 | uint32(blockIP4[3])
	baseInt := uint32(baseIP4[0])<<24 | uint32(baseIP4[1])<<16 | uint32(baseIP4[2])<<8 | uint32(baseIP4[3])
	return int((blockInt - baseInt) / 8), nil
}

func switchNameForSandbox(sandboxID string) string {
	return "boxy-sb-" + strings.ReplaceAll(sandboxID, " ", "")
}

// segmentLedgerPath returns d's ledger location, defaulting the same way
// Config.DataDir already does elsewhere in this package.
func (d *Driver) segmentLedgerPathOrDefault() string {
	if d.segmentLedgerPath != "" {
		return d.segmentLedgerPath
	}
	return "network-segments.json"
}

func (d *Driver) segments() *segmentLedger {
	return newSegmentLedger(d.segmentLedgerPathOrDefault())
}

// CreateSegment creates a dedicated Internal vSwitch + NAT for one sandbox.
// Internal, not Private: Private would also cut off the host-provided NAT
// (all internet access), which is the wrong default until egress policy
// exists to restrict it deliberately (see the design spec's Decision 1).
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	alloc, err := d.segments().allocate(sandboxID)
	if err != nil {
		return "", fmt.Errorf("allocate segment CIDR for sandbox %q: %w", sandboxID, err)
	}
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue)) {
    New-VMSwitch -SwitchName '%s' -SwitchType Internal | Out-Null
}
$adapter = Get-NetAdapter | Where-Object { $_.Name -like "*%s*" } | Select-Object -First 1
if (-not (Get-NetIPAddress -InterfaceAlias $adapter.Name -IPAddress '%s' -ErrorAction SilentlyContinue)) {
    New-NetIPAddress -IPAddress '%s' -PrefixLength 29 -InterfaceAlias $adapter.Name | Out-Null
}
if (-not (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue)) {
    New-NetNat -Name '%s' -InternalIPInterfaceAddressPrefix '%s' | Out-Null
}
`,
		psq(alloc.SwitchName), psq(alloc.SwitchName), psq(alloc.SwitchName),
		psq(alloc.Gateway), psq(alloc.Gateway),
		psq(alloc.SwitchName), psq(alloc.SwitchName), psq(alloc.CIDR)))
	if err != nil {
		_ = d.segments().release(sandboxID)
		return "", fmt.Errorf("create segment for sandbox %q: %w", sandboxID, err)
	}
	return providersdk.SegmentRef(alloc.SwitchName), nil
}

// AttachToSegment moves an already-created, already-running VM's network
// adapter onto the sandbox's segment. This is the same live-reconnect
// PowerShell Driver.Create already uses to attach a new VM to its
// configured switch (driver.go's Create) -- no VM restart, single fast
// call, matching the "must be fast" constraint.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	vmName, err := d.vmNameFromID(ctx, providerResourceID)
	if err != nil {
		return fmt.Errorf("resolve VM name for %q: %w", providerResourceID, err)
	}
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Connect-VMNetworkAdapter -VMName '%s' -SwitchName '%s' | Out-Null`,
		psq(vmName), psq(string(ref))))
	if err != nil {
		return fmt.Errorf("attach %q to segment %q: %w", providerResourceID, ref, err)
	}
	return nil
}

// DestroySegment removes the NAT and switch created by CreateSegment.
// Idempotent: a segment already gone (both Get- calls find nothing) is not
// an error, matching Driver.Delete's contract.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	switchName := string(ref)
	_, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-NetNat -Name '%s' -Confirm:$false | Out-Null
}
if (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-VMSwitch -Name '%s' -Force | Out-Null
}
`, psq(switchName), psq(switchName), psq(switchName), psq(switchName)))
	if err != nil {
		return fmt.Errorf("destroy segment %q: %w", ref, err)
	}
	// Best-effort: the ledger entry is keyed by sandbox ID, not switch name,
	// and DestroySegment only receives the SegmentRef (switch name). The
	// caller (Plan 1b's allocation-teardown wiring) is responsible for
	// calling ledger release via the sandbox ID it already has; this
	// method's job is the PowerShell teardown only. (No action needed here
	// -- documented so Plan 1b's author doesn't have to rediscover this.)
	return nil
}
```

Add the new field to `Driver` (`driver.go`, inside the existing field block starting line 33):

```go
	// segmentLedgerPath is where the per-sandbox network-segment CIDR
	// ledger is persisted (see network_isolation.go). Empty uses the
	// package default "network-segments.json", resolved the same way
	// Config.DataDir already is when a boxy config file's directory is
	// known (RelativePathResolver).
	segmentLedgerPath string
```

Wire `Config` → `Driver.segmentLedgerPath` in `New` (`driver.go`'s `New(cfg *Config)`, right after the existing `dataDir` resolution): set `d.segmentLedgerPath = filepath.Join(dataDir, "network-segments.json")` using the same `dataDir` variable `New` already computes for other per-driver state.

- [ ] **Step 4: Run the ledger tests to verify they pass**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run TestSegmentLedger -v`
Expected: PASS (all three).

- [ ] **Step 5: Write the failing tests for the driver methods (fake PowerShell executor)**

```go
// append to pkg/providersdk/providers/hyperv/network_isolation_test.go
func TestDriver_CreateSegment_RunsSwitchAndNatSetup(t *testing.T) {
	var scripts []string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		scripts = append(scripts, script)
		return "", nil
	})
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "boxy-sb-sb-1" {
		t.Fatalf("ref = %q, want %q", ref, "boxy-sb-sb-1")
	}
	if len(scripts) != 1 {
		t.Fatalf("expected exactly one PowerShell call, got %d", len(scripts))
	}
	for _, want := range []string{"New-VMSwitch", "SwitchType Internal", "New-NetNat", "10.250.0.0/29"} {
		if !strings.Contains(scripts[0], want) {
			t.Fatalf("script missing %q:\n%s", want, scripts[0])
		}
	}
}

func TestDriver_AttachToSegment_ResolvesVMNameThenConnects(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			if !strings.Contains(script, "Get-VM -Id") {
				t.Fatalf("first call should resolve VM name, got:\n%s", script)
			}
			return "boxy-vm-1\n", nil
		case 2:
			if !strings.Contains(script, "Connect-VMNetworkAdapter") || !strings.Contains(script, "boxy-vm-1") || !strings.Contains(script, "boxy-sb-sb-1") {
				t.Fatalf("second call should connect the adapter, got:\n%s", script)
			}
			return "", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	err := d.AttachToSegment(context.Background(), fakeGUID, providersdk.SegmentRef("boxy-sb-sb-1"))
	if err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if callNum != 2 {
		t.Fatalf("expected 2 PowerShell calls, got %d", callNum)
	}
}

func TestDriver_DestroySegment_RemovesNatThenSwitch(t *testing.T) {
	var script string
	d := mockDriver(func(_ context.Context, s string) (string, error) {
		script = s
		return "", nil
	})

	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("boxy-sb-sb-1")); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	for _, want := range []string{"Remove-NetNat", "Remove-VMSwitch", "boxy-sb-sb-1"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestDriver_CreateSegment_IsANetworkIsolator(t *testing.T) {
	var d providersdk.Driver = mockDriver(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := d.(providersdk.NetworkIsolator); !ok {
		t.Fatal("*hyperv.Driver must satisfy providersdk.NetworkIsolator")
	}
}
```

Add the needed imports (`providersdk`) to the test file's existing import block if not already present.

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestDriver_(CreateSegment|AttachToSegment|DestroySegment)' -v`
Expected: FAIL — methods not yet compiled in (should already be fixed by Step 3; if Step 3's code was written first this step instead verifies PASS directly — run it now regardless to confirm).

- [ ] **Step 7: Run all new tests together to verify they pass**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestSegmentLedger|TestDriver_CreateSegment|TestDriver_AttachToSegment|TestDriver_DestroySegment' -v`
Expected: PASS (all).

- [ ] **Step 8: Run the full hyperv package test suite to check for regressions**

Run: `go test ./pkg/providersdk/providers/hyperv/...`
Expected: PASS, no regressions in existing tests.

- [ ] **Step 9: Commit**

```bash
git add pkg/providersdk/providers/hyperv/network_isolation.go \
        pkg/providersdk/providers/hyperv/network_isolation_test.go \
        pkg/providersdk/providers/hyperv/driver.go
git commit -m "feat(hyperv): implement providersdk.NetworkIsolator

Per-sandbox Internal vSwitch + NAT, with a persisted /29-per-sandbox
CIDR ledger (pkg/diskjson) to avoid collisions between concurrently
created segments on one host.

Part of #224."
```

---

### Task 3: Docker `dockerClient` interface — add network methods + mock

**Files:**
- Modify: `pkg/providersdk/providers/docker/driver.go:44-58` (add four methods to the `dockerClient` interface)
- Modify: `pkg/providersdk/providers/docker/driver_test.go:123-186` (add matching fields + methods to `mockDockerClient`)

**Interfaces:**
- Produces: `dockerClient` interface gains `NetworkCreate`, `NetworkConnect`, `NetworkDisconnect`, `NetworkRemove`; `mockDockerClient` gains matching injectable function fields, for Task 4 to use.

This is pure plumbing — no behavior yet, just widening the seam Task 4 needs. It's its own task because it touches two files whose only relationship is "keep the interface and its test double in sync," and a reviewer can sanity-check "does the mock match the interface" as a unit before any real logic lands on top.

- [ ] **Step 1: Add the four methods to the `dockerClient` interface**

In `pkg/providersdk/providers/docker/driver.go`, inside the existing `dockerClient` interface (after `ContainerRemove`, before `Info`):

```go
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkConnect(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error
	NetworkDisconnect(ctx context.Context, networkID, containerID string, force bool) error
	NetworkRemove(ctx context.Context, networkID string) error
```

(`network.EndpointSettings` and `network.CreateOptions`/`CreateResponse` are already imported via the existing `"github.com/docker/docker/api/types/network"` import in `driver.go` — no new imports needed.)

- [ ] **Step 2: Add matching fields and methods to `mockDockerClient`**

In `pkg/providersdk/providers/docker/driver_test.go`, inside the `mockDockerClient` struct (after `info`):

```go
	networkCreate     func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	networkConnect    func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error
	networkDisconnect func(ctx context.Context, networkID, containerID string, force bool) error
	networkRemove     func(ctx context.Context, networkID string) error
```

And after the existing `Info` method:

```go
func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return m.networkCreate(ctx, name, options)
}
func (m *mockDockerClient) NetworkConnect(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
	return m.networkConnect(ctx, networkID, containerID, config)
}
func (m *mockDockerClient) NetworkDisconnect(ctx context.Context, networkID, containerID string, force bool) error {
	return m.networkDisconnect(ctx, networkID, containerID, force)
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return m.networkRemove(ctx, networkID)
}
```

- [ ] **Step 3: Build to verify the interface and mock stay in sync**

Run: `go build ./pkg/providersdk/providers/docker/...`
Expected: succeeds (this step has no new test of its own — it's verified by Task 4's tests exercising these fields; a plain build catches a mismatched signature immediately).

- [ ] **Step 4: Run the existing docker package test suite to check for regressions**

Run: `go test ./pkg/providersdk/providers/docker/...`
Expected: PASS, no regressions (existing tests don't set the new mock fields, so nothing exercises them yet — that's Task 4).

- [ ] **Step 5: Commit**

```bash
git add pkg/providersdk/providers/docker/driver.go pkg/providersdk/providers/docker/driver_test.go
git commit -m "chore(docker): widen dockerClient interface with network methods

Plumbing for the upcoming NetworkIsolator implementation -- no new
behavior yet.

Part of #224."
```

---

### Task 4: Docker `NetworkIsolator` implementation

**Files:**
- Create: `pkg/providersdk/providers/docker/network_isolation.go`
- Test: `pkg/providersdk/providers/docker/network_isolation_test.go`

**Interfaces:**
- Consumes: `dockerClient.NetworkCreate/NetworkConnect/NetworkDisconnect/NetworkRemove` (Task 3), `mockDockerClient` (Task 3), `providersdk.SegmentRef` (Task 1), the existing `managedLabel`/`managedLabelValue` constants (`driver.go:34-37`).
- Produces: `(*docker.Driver)` satisfies `providersdk.NetworkIsolator`.

Simpler than Hyper-V's: Docker's own daemon auto-assigns a non-overlapping subnet per network when none is specified, so there's no ledger to build here.

- [ ] **Step 1: Write the failing test**

```go
// pkg/providersdk/providers/docker/network_isolation_test.go
package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriver_CreateSegment_CreatesLabeledBridgeNetwork(t *testing.T) {
	var gotName string
	var gotOpts network.CreateOptions
	cli := &mockDockerClient{
		networkCreate: func(_ context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
			gotName = name
			gotOpts = options
			return network.CreateResponse{ID: "net-abc123"}, nil
		},
	}
	d := &Driver{cli: cli}

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "net-abc123" {
		t.Fatalf("ref = %q, want the network ID returned by NetworkCreate", ref)
	}
	if gotName != "boxy-sb-sb-1" {
		t.Fatalf("network name = %q, want %q", gotName, "boxy-sb-sb-1")
	}
	if gotOpts.Driver != "bridge" {
		t.Fatalf("Driver = %q, want %q", gotOpts.Driver, "bridge")
	}
	if gotOpts.Labels[managedLabel] != managedLabelValue {
		t.Fatalf("network missing managed label: %+v", gotOpts.Labels)
	}
}

func TestDriver_AttachToSegment_ConnectsContainerToNetwork(t *testing.T) {
	var gotNetworkID, gotContainerID string
	cli := &mockDockerClient{
		networkConnect: func(_ context.Context, networkID, containerID string, _ *network.EndpointSettings) error {
			gotNetworkID, gotContainerID = networkID, containerID
			return nil
		},
	}
	d := &Driver{cli: cli}

	err := d.AttachToSegment(context.Background(), "container-xyz", providersdk.SegmentRef("net-abc123"))
	if err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if gotNetworkID != "net-abc123" || gotContainerID != "container-xyz" {
		t.Fatalf("NetworkConnect called with (%q, %q)", gotNetworkID, gotContainerID)
	}
}

func TestDriver_DestroySegment_RemovesNetwork(t *testing.T) {
	var gotID string
	cli := &mockDockerClient{
		networkRemove: func(_ context.Context, networkID string) error {
			gotID = networkID
			return nil
		},
	}
	d := &Driver{cli: cli}

	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if gotID != "net-abc123" {
		t.Fatalf("NetworkRemove called with %q, want %q", gotID, "net-abc123")
	}
}

func TestDriver_DestroySegment_IdempotentWhenAlreadyGone(t *testing.T) {
	cli := &mockDockerClient{
		networkRemove: func(_ context.Context, _ string) error {
			return notFoundError{msg: "network not found"}
		},
	}
	d := &Driver{cli: cli}

	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("net-gone")); err != nil {
		t.Fatalf("DestroySegment on an already-gone network must be a no-op, got: %v", err)
	}
}

func TestDriver_IsANetworkIsolator(t *testing.T) {
	var d providersdk.Driver = &Driver{cli: &mockDockerClient{}}
	if _, ok := d.(providersdk.NetworkIsolator); !ok {
		t.Fatal("*docker.Driver must satisfy providersdk.NetworkIsolator")
	}
}
```

(`notFoundError` is the existing test-only error type already defined at `driver_test.go:269` — reused here, not redefined.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/docker/... -run 'TestDriver_(CreateSegment|AttachToSegment|DestroySegment|IsANetworkIsolator)' -v`
Expected: compile failure — `CreateSegment`/`AttachToSegment`/`DestroySegment` not defined on `*Driver`.

- [ ] **Step 3: Write the implementation**

Check how `Driver.Delete` already distinguishes a not-found error from a real one, to match that exact idiom for `DestroySegment`'s idempotency:

Run: `grep -n "cerrdefs.IsNotFound\|errdefs.IsNotFound" pkg/providersdk/providers/docker/driver.go`

Use whatever helper that search turns up (this driver already imports `cerrdefs "github.com/containerd/errdefs"` — the existing `Delete` method's not-found check is the exact pattern to copy here).

```go
// pkg/providersdk/providers/docker/network_isolation.go
package docker

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// CreateSegment creates a dedicated bridge network for one sandbox. No
// explicit IPAM/subnet is set -- the Docker daemon auto-assigns a
// non-overlapping subnet from its own default address pools, so unlike
// Hyper-V's implementation there is no collision-avoidance ledger to
// maintain here.
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	name := "boxy-sb-" + sandboxID
	resp, err := d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{managedLabel: managedLabelValue},
	})
	if err != nil {
		return "", fmt.Errorf("create docker network %q: %w", name, err)
	}
	return providersdk.SegmentRef(resp.ID), nil
}

// AttachToSegment connects an already-running container to the sandbox's
// network. No EndpointSettings are specified -- Docker assigns an address
// from the network's own auto-assigned subnet.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	if err := d.cli.NetworkConnect(ctx, string(ref), providerResourceID, nil); err != nil {
		return fmt.Errorf("connect container %q to segment %q: %w", providerResourceID, ref, err)
	}
	return nil
}

// DestroySegment removes the network created by CreateSegment. Idempotent
// for an already-gone network, matching Driver.Delete's contract.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	if err := d.cli.NetworkRemove(ctx, string(ref)); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove docker network %q: %w", ref, err)
	}
	return nil
}
```

If Step 3's `grep` found a different not-found idiom than `cerrdefs.IsNotFound` (e.g. a driver-local helper function), use that one instead and adjust the import accordingly — the point is matching `Delete`'s existing idiom exactly, not introducing a second one.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/providersdk/providers/docker/... -run 'TestDriver_(CreateSegment|AttachToSegment|DestroySegment|IsANetworkIsolator)' -v`
Expected: PASS (all five).

- [ ] **Step 5: Run the full docker package test suite to check for regressions**

Run: `go test ./pkg/providersdk/providers/docker/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/providersdk/providers/docker/network_isolation.go pkg/providersdk/providers/docker/network_isolation_test.go
git commit -m "feat(docker): implement providersdk.NetworkIsolator

Per-sandbox bridge network, letting the Docker daemon auto-assign a
non-overlapping subnet.

Closes part of #224."
```

---

## After This Plan

Both drivers can create/attach/destroy a private per-sandbox segment, fully tested in isolation, but **nothing calls any of this yet** — no fulfiller wiring, no remote-agent (gRPC) forwarding, no devfactory. That's Plan 1b (`agentsdk.NetworkIsolatingAgent` for `EmbeddedAgent`/`RemoteAgent`, plus the allocation-time hook into `AgentProvisioner.Allocate`/`DriverProvisioner.Allocate` and a `DestroySegment` call on the sandbox-deletion path), to be written as its own plan once this one lands and passes `task ci:validate`.
