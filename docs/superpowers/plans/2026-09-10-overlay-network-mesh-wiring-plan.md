# Overlay Network Fabric — Mesh Wiring (Plan 2b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `pkg/meshnet` (Plan 2) into the Hyper-V and Docker drivers, both agent kinds, and `sandbox.Manager`, so that when a sandbox's resources land on more than one host, those hosts' segments automatically get peered into a full mesh over WireGuard.

**Architecture:** A new optional `providersdk.MeshPeerer` capability, implemented by each driver using `pkg/meshnet` — the driver owns exactly when to create a `meshnet.Interface` (lazily, on the first mesh call for a segment) and how to route that segment's subnet through it (host-specific: `New-NetRoute` on Hyper-V, `ip route add` on Docker/Linux). The agentsdk/proto forwarding layer mirrors Plan 1b's `NetworkIsolatingAgent` shape exactly. `sandbox.Manager` triggers peering whenever `ensureNetworkSegment` (Plan 1c) causes a sandbox's `NetworkSegments` to grow past one agent — at that point it asks every agent already in the list, plus the new one, to exchange identities and add each other as peers (full mesh).

**Tech Stack:** Go 1.25, `pkg/meshnet` (Plan 2), protobuf/gRPC (`task proto:generate`).

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan completes Decision 2 end to end.

## Global Constraints

- A single-host sandbox must never touch any of this plan's code paths — `sandbox.Manager`'s trigger condition is specifically "this sandbox's `NetworkSegments` just grew past length 1," not "every allocation." Re-verify this condition explicitly in Task 5's tests.
- Mesh peer count is per-sandbox, never global — this plan must not introduce anything resembling "every agent peers with every other agent" at startup or on any schedule. Peering is created only for the specific agents one specific sandbox's segments touch.
- The OS-route-installation commands in Tasks 2 and 3 (`New-NetRoute`, `ip route add`) cannot be validated against a live Hyper-V host or a live Linux Docker host from this development environment (an existing, standing constraint of this project — see AGENTS.md's "Lessons Learned"). Write them to the documented, standard syntax for each platform and cover them with fake-executor unit tests exactly like every other host-management call in these drivers; do not claim live validation.
- Every mesh method must accept and correctly no-op/error for a segment with no mesh interface yet, rather than panicking — a `MeshIdentity` call is always the first mesh-related call for a given segment (lazy creation happens there), so `AddMeshPeer`/`RemoveMeshPeer` on a segment that never had `MeshIdentity` called is a genuine caller error, not a case to silently swallow.

---

### Task 1: `providersdk.MeshPeerer` capability

**Files:**
- Create: `pkg/providersdk/mesh_peering.go`
- Test: `pkg/providersdk/mesh_peering_test.go`

**Interfaces:**
- Produces: `MeshPeerer` interface — `MeshIdentity(ctx, ref SegmentRef) (publicKey, endpoint, cidr string, err error)`, `AddMeshPeer(ctx, ref SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error`, `RemoveMeshPeer(ctx, ref SegmentRef, peerPublicKey string) error`.

- [ ] **Step 1: Write the failing test**

```go
// pkg/providersdk/mesh_peering_test.go
package providersdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeMeshPeeringDriver struct {
	*fakeIsolatingDriver // reuse this package's fake from network_isolation_test.go (Plan 1a Task 1) if it's accessible here; otherwise define a minimal driver-shaped fake alongside this test
	identityPublicKey string
	identityEndpoint  string
	identityCIDR      string
}

func (f *fakeMeshPeeringDriver) MeshIdentity(_ context.Context, ref providersdk.SegmentRef) (string, string, string, error) {
	return f.identityPublicKey, f.identityEndpoint, f.identityCIDR, nil
}
func (f *fakeMeshPeeringDriver) AddMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	return nil
}
func (f *fakeMeshPeeringDriver) RemoveMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey string) error {
	return nil
}

func TestMeshPeerer_DetectedByTypeAssertion(t *testing.T) {
	var d providersdk.Driver = &fakeMeshPeeringDriver{
		fakeIsolatingDriver: &fakeIsolatingDriver{},
		identityPublicKey:   "abc123",
		identityEndpoint:    "203.0.113.5:51820",
		identityCIDR:        "10.250.0.0/29",
	}
	peerer, ok := d.(providersdk.MeshPeerer)
	if !ok {
		t.Fatal("driver implementing MeshPeerer's methods was not detected via type assertion")
	}
	pub, endpoint, cidr, err := peerer.MeshIdentity(context.Background(), "boxy-sb-sb-1")
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub != "abc123" || endpoint != "203.0.113.5:51820" || cidr != "10.250.0.0/29" {
		t.Fatalf("got (%q, %q, %q)", pub, endpoint, cidr)
	}
}
```

If `fakeIsolatingDriver` from Plan 1a's `network_isolation_test.go` isn't directly reusable here (e.g. it's unexported to a different test binary), define a minimal standalone fake implementing just the base `providersdk.Driver` methods, following the exact same pattern that file already established.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/providersdk/... -run TestMeshPeerer -v`
Expected: compile failure — `providersdk.MeshPeerer` undefined.

- [ ] **Step 3: Write the interface**

```go
// pkg/providersdk/mesh_peering.go
package providersdk

import "context"

// MeshPeerer is an optional provider capability for cross-host connectivity
// between two sandboxes' segments on different hosts (spec Decision 2). Not
// every driver implements it -- callers must type-assert, the same pattern
// as every other optional capability in this package.
//
// MeshIdentity is the first call for a given segment: it lazily creates the
// segment's mesh interface if one doesn't exist yet, and returns what a
// remote peer needs to connect to it -- a public key (never a private key;
// see pkg/meshnet's key-handling guarantee), a host:port endpoint, and the
// segment's own subnet CIDR (so the *other* side knows what to route
// through this peer once it, in turn, calls AddMeshPeer).
//
// AddMeshPeer/RemoveMeshPeer operate on a segment that MUST have already
// had MeshIdentity called for it at least once -- calling either on a
// segment with no mesh interface yet is a caller error, not something to
// silently no-op.
type MeshPeerer interface {
	MeshIdentity(ctx context.Context, ref SegmentRef) (publicKey, endpoint, cidr string, err error)
	AddMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error
	RemoveMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey string) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/providersdk/... -run TestMeshPeerer -v`
Expected: PASS.

- [ ] **Step 5: Run the full providersdk package test suite**

Run: `go test ./pkg/providersdk/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/providersdk/mesh_peering.go pkg/providersdk/mesh_peering_test.go
git commit -m "feat(providersdk): add MeshPeerer optional driver capability

Part of #224."
```

---

### Task 2: Hyper-V `MeshPeerer` implementation

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/network_isolation.go` (add a `lookupBySwitchName` method to `segmentLedger`)
- Create: `pkg/providersdk/providers/hyperv/mesh_peering.go`
- Test: `pkg/providersdk/providers/hyperv/mesh_peering_test.go`
- Modify: `pkg/providersdk/providers/hyperv/driver.go` (add a `meshInterfaces map[providersdk.SegmentRef]*meshnet.Interface` field + mutex, and a `meshEndpoint string` config-derived field)
- Modify: `pkg/providersdk/providers/hyperv/config.go` (add `Config.MeshEndpoint string` — the operator-declared, reachable `host:port` this agent should be dialed at for mesh traffic; see Plan 2's spec-review note on why this is explicit config, not auto-detected)

**Interfaces:**
- Consumes: `meshnet.New`, `(*meshnet.Interface).PublicKeyHex/AddPeer/RemovePeer/Name` (Plan 2).
- Produces: `(*hyperv.Driver)` satisfies `providersdk.MeshPeerer`.

- [ ] **Step 1: Write the failing test for the ledger's reverse lookup**

```go
// append to pkg/providersdk/providers/hyperv/network_isolation_test.go

func TestSegmentLedger_LookupBySwitchName_FindsAllocatedCIDR(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)
	alloc, err := ledger.allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	found, ok := ledger.lookupBySwitchName(alloc.SwitchName)
	if !ok {
		t.Fatalf("lookupBySwitchName(%q) not found", alloc.SwitchName)
	}
	if found.CIDR != alloc.CIDR {
		t.Fatalf("found CIDR = %q, want %q", found.CIDR, alloc.CIDR)
	}
}

func TestSegmentLedger_LookupBySwitchName_NotFound(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)
	if _, ok := ledger.lookupBySwitchName("does-not-exist"); ok {
		t.Fatal("expected not found")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run TestSegmentLedger_LookupBySwitchName -v`
Expected: compile failure — `lookupBySwitchName` undefined.

- [ ] **Step 3: Implement the reverse lookup**

In `pkg/providersdk/providers/hyperv/network_isolation.go`, add to `segmentLedger`:

```go
// lookupBySwitchName finds a segment allocation by its switch name
// (providersdk.SegmentRef's underlying value), the reverse direction from
// allocate's sandboxID-keyed lookup. Used by MeshIdentity, which is only
// ever given a SegmentRef, never the originating sandbox ID.
func (l *segmentLedger) lookupBySwitchName(switchName string) (segmentAllocation, bool) {
	state, err := l.store.Load()
	if err != nil {
		return segmentAllocation{}, false
	}
	for _, alloc := range state.BySandboxID {
		if alloc.SwitchName == switchName {
			return alloc, true
		}
	}
	return segmentAllocation{}, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run TestSegmentLedger_LookupBySwitchName -v`
Expected: PASS.

- [ ] **Step 5: Add `Config.MeshEndpoint`**

In `pkg/providersdk/providers/hyperv/config.go`, add to `Config`:

```go
	// MeshEndpoint is the host:port this agent should be dialed at by a
	// peer agent's WireGuard interface for cross-host sandbox traffic.
	// Explicit, operator-declared -- not auto-detected -- matching this
	// package's existing posture for anything host-identity-shaped (see
	// DataDir's doc comment): a multi-homed host has no reliable way for
	// this code to guess which of its addresses another host can actually
	// reach. Required only when MeshPeerer is actually used (a sandbox
	// spanning this host and another); a single-host-only deployment never
	// needs it set.
	MeshEndpoint string `json:"mesh_endpoint,omitempty" yaml:"mesh_endpoint,omitempty"`
```

- [ ] **Step 6: Write the failing tests for the driver methods**

```go
// pkg/providersdk/providers/hyperv/mesh_peering_test.go
package hyperv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriver_MeshIdentity_CreatesInterfaceLazilyAndReturnsCIDR(t *testing.T) {
	var script string
	d := mockDriver(func(_ context.Context, s string) (string, error) {
		script = s
		return "", nil
	})
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	d.meshEndpoint = "203.0.113.5:51820"
	alloc, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	ref := providersdk.SegmentRef(alloc.SwitchName)

	pub, endpoint, cidr, err := d.MeshIdentity(context.Background(), ref)
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	if endpoint != "203.0.113.5:51820" {
		t.Fatalf("endpoint = %q, want the configured mesh endpoint", endpoint)
	}
	if cidr != alloc.CIDR {
		t.Fatalf("cidr = %q, want %q (the segment's allocated CIDR)", cidr, alloc.CIDR)
	}
	if !strings.Contains(script, "New-NetRoute") {
		t.Fatalf("expected MeshIdentity's lazy interface creation to also install a route, got script: %s", script)
	}

	// Idempotent: a second call for the same segment must reuse the same
	// interface (same public key), not create a second one.
	pub2, _, _, err := d.MeshIdentity(context.Background(), ref)
	if err != nil {
		t.Fatalf("second MeshIdentity: %v", err)
	}
	if pub2 != pub {
		t.Fatalf("second call returned a different public key (%q vs %q) -- must reuse the existing interface", pub2, pub)
	}
	_ = d.closeMeshInterfaces() // test cleanup helper, see Step 7
}

func TestDriver_MeshIdentity_MissingSegmentErrors(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	if _, _, _, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("never-created")); err == nil {
		t.Fatal("expected an error for a segment the ledger has never seen")
	}
}

func TestDriver_AddMeshPeer_ConfiguresThePeer(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	d.meshEndpoint = "203.0.113.5:51820"
	alloc, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	ref := providersdk.SegmentRef(alloc.SwitchName)
	if _, _, _, err := d.MeshIdentity(context.Background(), ref); err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}

	if err := d.AddMeshPeer(context.Background(), ref, "deadbeef", "203.0.113.9:51820", "10.250.0.8/29"); err != nil {
		t.Fatalf("AddMeshPeer: %v", err)
	}
	_ = d.closeMeshInterfaces()
}

func TestDriver_AddMeshPeer_NoInterfaceYetErrors(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	err := d.AddMeshPeer(context.Background(), providersdk.SegmentRef("never-created"), "deadbeef", "203.0.113.9:51820", "10.250.0.8/29")
	if err == nil {
		t.Fatal("expected an error -- AddMeshPeer before any MeshIdentity call for this segment is a caller error")
	}
}

func TestDriver_IsAMeshPeerer(t *testing.T) {
	var d providersdk.Driver = mockDriver(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := d.(providersdk.MeshPeerer); !ok {
		t.Fatal("*hyperv.Driver must satisfy providersdk.MeshPeerer")
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestDriver_(MeshIdentity|AddMeshPeer|IsAMeshPeerer)' -v`
Expected: compile failure — `MeshIdentity`/`AddMeshPeer`/`meshEndpoint`/`closeMeshInterfaces` undefined.

- [ ] **Step 8: Add the dependency and implement**

Run: `go get golang.zx2c4.com/wireguard` (should already be pinned from Plan 2's `go.mod`; this just confirms it resolves for this module too — no new version work needed).

Add the field to `Driver` (`driver.go`, in the same block as `segmentLedgerPath`):

```go
	// meshEndpoint is Config.MeshEndpoint, threaded through the same way
	// segmentLedgerPath is.
	meshEndpoint string

	// meshInterfaces holds this driver's live meshnet.Interface per
	// segment, created lazily on first MeshIdentity call. In-memory only --
	// a process restart loses these (and any peer must reconnect, which is
	// expected WireGuard behavior after any endpoint goes down, not
	// something this driver needs to special-case).
	meshMu         sync.Mutex
	meshInterfaces map[providersdk.SegmentRef]*meshnet.Interface
```

Wire `Config.MeshEndpoint` into `d.meshEndpoint` in `New(cfg *Config)`, alongside the existing `segmentLedgerPath` wiring from Plan 1a.

```go
// pkg/providersdk/providers/hyperv/mesh_peering.go
package hyperv

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/meshnet"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// MeshIdentity satisfies providersdk.MeshPeerer. It lazily creates this
// segment's meshnet.Interface on first call, routes the segment's own
// subnet through it (New-NetRoute -- see this plan's Global Constraints on
// this command being unverified against a live host), and returns what a
// remote peer needs to connect: this interface's public key, this driver's
// configured mesh endpoint, and the segment's own CIDR (so the *other* side
// knows what to route through this peer once it calls AddMeshPeer back).
func (d *Driver) MeshIdentity(ctx context.Context, ref providersdk.SegmentRef) (string, string, string, error) {
	alloc, ok := d.segments().lookupBySwitchName(string(ref))
	if !ok {
		return "", "", "", fmt.Errorf("segment %q not found", ref)
	}
	iface, err := d.meshInterfaceFor(ref)
	if err != nil {
		return "", "", "", err
	}
	ifName, err := iface.Name()
	if err != nil {
		return "", "", "", fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if _, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' -ErrorAction SilentlyContinue)) {
    New-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' | Out-Null
}
`, psq(ifName), psq(alloc.CIDR), psq(ifName), psq(alloc.CIDR))); err != nil {
		return "", "", "", fmt.Errorf("route segment %q's subnet through mesh interface: %w", ref, err)
	}
	return iface.PublicKeyHex(), d.meshEndpoint, alloc.CIDR, nil
}

// AddMeshPeer satisfies providersdk.MeshPeerer. The segment's mesh
// interface must already exist (created by a prior MeshIdentity call) --
// there is no lazy-create path here, since a caller adding a peer without
// first asking for this side's own identity is a genuine ordering bug, not
// something to paper over.
func (d *Driver) AddMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.AddPeer(peerPublicKey, peerEndpoint, []string{peerCIDR})
}

// RemoveMeshPeer satisfies providersdk.MeshPeerer.
func (d *Driver) RemoveMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.RemovePeer(peerPublicKey)
}

func (d *Driver) meshInterfaceFor(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	if d.meshInterfaces == nil {
		d.meshInterfaces = make(map[providersdk.SegmentRef]*meshnet.Interface)
	}
	if iface, ok := d.meshInterfaces[ref]; ok {
		return iface, nil
	}
	iface, err := meshnet.New(string(ref), 0)
	if err != nil {
		return nil, fmt.Errorf("create mesh interface for segment %q: %w", ref, err)
	}
	d.meshInterfaces[ref] = iface
	return iface, nil
}

func (d *Driver) existingMeshInterface(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	iface, ok := d.meshInterfaces[ref]
	if !ok {
		return nil, fmt.Errorf("no mesh interface for segment %q -- MeshIdentity must be called for this segment before AddMeshPeer/RemoveMeshPeer", ref)
	}
	return iface, nil
}

// closeMeshInterfaces closes every live mesh interface this driver owns.
// Test-only for now (referenced from mesh_peering_test.go); DestroySegment
// (Plan 1a) should call this for a specific ref once a segment is torn
// down -- left as a follow-up wiring note for whichever change next
// touches DestroySegment, not built as part of this plan's scope.
func (d *Driver) closeMeshInterfaces() error {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	for ref, iface := range d.meshInterfaces {
		if err := iface.Close(); err != nil {
			return fmt.Errorf("close mesh interface for segment %q: %w", ref, err)
		}
		delete(d.meshInterfaces, ref)
	}
	return nil
}
```

Add `"sync"` and `"github.com/Geogboe/boxy/pkg/meshnet"` to `driver.go`'s import block.

- [ ] **Step 9: Run test to verify it passes**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestDriver_(MeshIdentity|AddMeshPeer|IsAMeshPeerer)' -v`
Expected: PASS (all five).

- [ ] **Step 10: Run the full hyperv package test suite to check for regressions**

Run: `go test ./pkg/providersdk/providers/hyperv/...`
Expected: PASS, no regressions.

- [ ] **Step 11: Commit**

```bash
git add pkg/providersdk/providers/hyperv/network_isolation.go \
        pkg/providersdk/providers/hyperv/network_isolation_test.go \
        pkg/providersdk/providers/hyperv/mesh_peering.go \
        pkg/providersdk/providers/hyperv/mesh_peering_test.go \
        pkg/providersdk/providers/hyperv/config.go \
        pkg/providersdk/providers/hyperv/driver.go
git commit -m "feat(hyperv): implement providersdk.MeshPeerer

New-NetRoute/route-installation commands are written to standard syntax
but not verified against a live Hyper-V host, per this project's standing
constraint. DestroySegment does not yet close mesh interfaces -- flagged
as a follow-up, not built here.

Part of #224."
```

---

### Task 3: Docker `MeshPeerer` implementation

**Files:**
- Modify: `pkg/providersdk/providers/docker/driver.go` (add `NetworkInspect` to `dockerClient` + mock; add mesh interface map/mutex + `hostExec` seam; add `Config.MeshEndpoint`)
- Create: `pkg/providersdk/providers/docker/mesh_peering.go`
- Test: `pkg/providersdk/providers/docker/mesh_peering_test.go`

**Interfaces:**
- Consumes: `meshnet.New`/`AddPeer`/`RemovePeer`/`Name`/`PublicKeyHex` (Plan 2), `dockerClient.NetworkInspect` (new in this task).
- Produces: `(*docker.Driver)` satisfies `providersdk.MeshPeerer`.

The Docker driver has no existing host-shell-execution seam (unlike Hyper-V's `d.ps`) — it only ever talks to the Docker Engine API. Routing a Linux host's traffic through a mesh interface (`ip route add`) is a real host-level operation the Docker Engine API has no equivalent for, so this task adds a minimal, injectable `hostExec` seam to this driver, used for nothing else.

- [ ] **Step 1: Add `NetworkInspect` to `dockerClient` + mock**

In `pkg/providersdk/providers/docker/driver.go`'s `dockerClient` interface (after the network methods Plan 1a Task 3 already added):

```go
	NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
```

In `driver_test.go`'s `mockDockerClient` struct and methods (same pattern as Plan 1a Task 3):

```go
	networkInspect func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
```

```go
func (m *mockDockerClient) NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
	return m.networkInspect(ctx, networkID, options)
}
```

- [ ] **Step 2: Build to verify the interface and mock stay in sync**

Run: `go build ./pkg/providersdk/providers/docker/...`
Expected: succeeds.

- [ ] **Step 3: Add `Config.MeshEndpoint` and the `hostExec` seam**

In `pkg/providersdk/providers/docker/config.go`, add to `Config` (same doc-comment reasoning as Hyper-V's, Task 2 Step 5):

```go
	// MeshEndpoint is the host:port this agent should be dialed at by a
	// peer agent's WireGuard interface for cross-host sandbox traffic. See
	// hyperv.Config.MeshEndpoint's doc comment for why this is explicit,
	// operator-declared config rather than auto-detected.
	MeshEndpoint string `json:"mesh_endpoint,omitempty" yaml:"mesh_endpoint,omitempty"`
```

In `driver.go`, add to `Driver`:

```go
	meshEndpoint string

	// hostExec runs a shell command on the Docker host, used only for
	// installing the mesh route (ip route add) -- everything else this
	// driver does goes through the Docker Engine API, never a host shell.
	// nil -> a real os/exec-backed implementation; inject a fake in tests.
	hostExec func(ctx context.Context, name string, args ...string) (string, error)

	meshMu         sync.Mutex
	meshInterfaces map[providersdk.SegmentRef]*meshnet.Interface
```

Add a small real implementation and accessor, mirroring Hyper-V's `d.ps` seam shape:

```go
func (d *Driver) runHost(ctx context.Context, name string, args ...string) (string, error) {
	if d.hostExec != nil {
		return d.hostExec(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
```

Add `"os/exec"`, `"sync"`, and `"github.com/Geogboe/boxy/pkg/meshnet"` to `driver.go`'s imports. Wire `Config.MeshEndpoint` into `d.meshEndpoint` in `New(cfg *Config)`.

- [ ] **Step 4: Write the failing tests**

```go
// pkg/providersdk/providers/docker/mesh_peering_test.go
package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriver_MeshIdentity_DiscoversSubnetAndInstallsRoute(t *testing.T) {
	var gotArgs []string
	cli := &mockDockerClient{
		networkInspect: func(_ context.Context, networkID string, _ network.InspectOptions) (network.Inspect, error) {
			if networkID != "net-abc123" {
				t.Fatalf("inspected %q, want net-abc123", networkID)
			}
			return network.Inspect{IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}, nil
		},
	}
	d := &Driver{cli: cli, meshEndpoint: "203.0.113.5:51820"}
	d.hostExec = func(_ context.Context, name string, args ...string) (string, error) {
		gotArgs = append(gotArgs, strings.Join(append([]string{name}, args...), " "))
		return "", nil
	}

	pub, endpoint, cidr, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("net-abc123"))
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	if endpoint != "203.0.113.5:51820" {
		t.Fatalf("endpoint = %q, want the configured mesh endpoint", endpoint)
	}
	if cidr != "172.30.0.0/24" {
		t.Fatalf("cidr = %q, want the discovered subnet", cidr)
	}
	found := false
	for _, call := range gotArgs {
		if strings.Contains(call, "ip route add 172.30.0.0/24") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an ip route add call for the discovered subnet, got calls: %v", gotArgs)
	}
	_ = d.closeMeshInterfaces()
}

func TestDriver_AddMeshPeer_NoInterfaceYetErrors(t *testing.T) {
	d := &Driver{cli: &mockDockerClient{}}
	err := d.AddMeshPeer(context.Background(), providersdk.SegmentRef("net-abc123"), "deadbeef", "203.0.113.9:51820", "10.250.0.8/29")
	if err == nil {
		t.Fatal("expected an error -- AddMeshPeer before any MeshIdentity call for this segment is a caller error")
	}
}

func TestDriver_IsAMeshPeerer(t *testing.T) {
	var d providersdk.Driver = &Driver{cli: &mockDockerClient{}}
	if _, ok := d.(providersdk.MeshPeerer); !ok {
		t.Fatal("*docker.Driver must satisfy providersdk.MeshPeerer")
	}
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./pkg/providersdk/providers/docker/... -run 'TestDriver_(MeshIdentity|AddMeshPeer|IsAMeshPeerer)' -v`
Expected: compile failure — `MeshIdentity`/`AddMeshPeer`/`closeMeshInterfaces` undefined on `*Driver`.

- [ ] **Step 6: Implement**

```go
// pkg/providersdk/providers/docker/mesh_peering.go
package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/meshnet"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// MeshIdentity satisfies providersdk.MeshPeerer. Unlike Hyper-V (which
// tracks its own segment CIDR in a ledger, since it chose the subnet
// itself), this driver lets Docker auto-assign the subnet at CreateSegment
// time (Plan 1a) -- so discovering it means asking Docker directly via
// NetworkInspect.
func (d *Driver) MeshIdentity(ctx context.Context, ref providersdk.SegmentRef) (string, string, string, error) {
	inspected, err := d.cli.NetworkInspect(ctx, string(ref), network.InspectOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("inspect network %q: %w", ref, err)
	}
	if len(inspected.IPAM.Config) == 0 || inspected.IPAM.Config[0].Subnet == "" {
		return "", "", "", fmt.Errorf("network %q has no discoverable subnet", ref)
	}
	cidr := inspected.IPAM.Config[0].Subnet

	iface, err := d.meshInterfaceFor(ref)
	if err != nil {
		return "", "", "", err
	}
	ifName, err := iface.Name()
	if err != nil {
		return "", "", "", fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if _, err := d.runHost(ctx, "ip", "route", "add", cidr, "dev", ifName); err != nil {
		return "", "", "", fmt.Errorf("route segment %q's subnet through mesh interface: %w", ref, err)
	}
	return iface.PublicKeyHex(), d.meshEndpoint, cidr, nil
}

func (d *Driver) AddMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.AddPeer(peerPublicKey, peerEndpoint, []string{peerCIDR})
}

func (d *Driver) RemoveMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.RemovePeer(peerPublicKey)
}

func (d *Driver) meshInterfaceFor(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	if d.meshInterfaces == nil {
		d.meshInterfaces = make(map[providersdk.SegmentRef]*meshnet.Interface)
	}
	if iface, ok := d.meshInterfaces[ref]; ok {
		return iface, nil
	}
	iface, err := meshnet.New(string(ref), 0)
	if err != nil {
		return nil, fmt.Errorf("create mesh interface for segment %q: %w", ref, err)
	}
	d.meshInterfaces[ref] = iface
	return iface, nil
}

func (d *Driver) existingMeshInterface(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	iface, ok := d.meshInterfaces[ref]
	if !ok {
		return nil, fmt.Errorf("no mesh interface for segment %q -- MeshIdentity must be called for this segment before AddMeshPeer/RemoveMeshPeer", ref)
	}
	return iface, nil
}

// closeMeshInterfaces closes every live mesh interface this driver owns.
// Test-only for now -- see hyperv's identical method for the same
// DestroySegment-wiring follow-up note.
func (d *Driver) closeMeshInterfaces() error {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	for ref, iface := range d.meshInterfaces {
		if err := iface.Close(); err != nil {
			return fmt.Errorf("close mesh interface for segment %q: %w", ref, err)
		}
		delete(d.meshInterfaces, ref)
	}
	return nil
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./pkg/providersdk/providers/docker/... -run 'TestDriver_(MeshIdentity|AddMeshPeer|IsAMeshPeerer)' -v`
Expected: PASS (all three).

- [ ] **Step 8: Run the full docker package test suite to check for regressions**

Run: `go test ./pkg/providersdk/providers/docker/...`
Expected: PASS, no regressions.

- [ ] **Step 9: Commit**

```bash
git add pkg/providersdk/providers/docker/driver.go \
        pkg/providersdk/providers/docker/driver_test.go \
        pkg/providersdk/providers/docker/mesh_peering.go \
        pkg/providersdk/providers/docker/mesh_peering_test.go \
        pkg/providersdk/providers/docker/config.go
git commit -m "feat(docker): implement providersdk.MeshPeerer

ip route add is written to standard syntax but not verified against a
live Linux Docker host, per this project's standing constraint.

Part of #224."
```

---

### Task 4: agentsdk/proto wiring — `MeshPeeringAgent`

**Files:**
- Modify: `proto/boxyagent/v1/agent.proto` (+ regenerate)
- Modify: `pkg/agentsdk/agent.go` (new `MeshPeeringAgent` interface)
- Modify: `pkg/agentsdk/embedded.go`
- Modify: `pkg/agentsdk/remote.go`
- Modify: `pkg/agentsdk/remoteclient.go`
- Test: `pkg/agentsdk/embedded_test.go`, `pkg/agentsdk/remote_test.go`, `pkg/agentsdk/remoteclient_test.go`

**Interfaces:**
- Consumes: `providersdk.MeshPeerer` (Tasks 1–3).
- Produces: `agentsdk.MeshPeeringAgent` — `MeshIdentity(ctx, provider, ref) (publicKey, endpoint, cidr string, err error)`, `AddMeshPeer(ctx, provider, ref, peerPublicKey, peerEndpoint, peerCIDR string) error`, `RemoveMeshPeer(ctx, provider, ref, peerPublicKey string) error` — implemented identically by `EmbeddedAgent` and `RemoteAgent`.

This mirrors Plan 1b's `NetworkIsolatingAgent` shape exactly, three new methods instead of three. Sub-tasks below.

#### 4.1 — Proto messages

- [ ] **Step 1: Add the `Command` oneof variants**

In `proto/boxyagent/v1/agent.proto`, inside `message Command`'s `oneof op` (after `DestroySegmentCommand destroy_segment = 12;`):

```proto
    MeshIdentityCommand mesh_identity = 13;
    AddMeshPeerCommand add_mesh_peer = 14;
    RemoveMeshPeerCommand remove_mesh_peer = 15;
```

- [ ] **Step 2: Add the command message definitions**

After `message DestroySegmentCommand { ... }`:

```proto
// MeshIdentityCommand asks the agent for the WireGuard identity of one
// segment, lazily creating its mesh interface if this is the first mesh
// call for it. See providersdk.MeshPeerer.
message MeshIdentityCommand {
  string segment_ref = 1;
}

// AddMeshPeerCommand asks the agent to peer segment_ref's mesh interface
// with a remote peer. The segment's mesh interface must already exist
// (i.e. a MeshIdentityCommand must already have been sent for it).
message AddMeshPeerCommand {
  string segment_ref = 1;
  string peer_public_key = 2;
  string peer_endpoint = 3;
  string peer_cidr = 4;
}

message RemoveMeshPeerCommand {
  string segment_ref = 1;
  string peer_public_key = 2;
}
```

- [ ] **Step 3: Add the `CommandResult` oneof variants**

Inside `message CommandResult`'s `oneof outcome` (after `google.protobuf.Empty destroy_segment = 13;`):

```proto
    MeshIdentityResult mesh_identity = 14;
    google.protobuf.Empty add_mesh_peer = 15;
    google.protobuf.Empty remove_mesh_peer = 16;
```

- [ ] **Step 4: Add the result message definition**

After `message CreateSegmentResult { ... }`:

```proto
message MeshIdentityResult {
  string public_key = 1;
  string endpoint = 2;
  string cidr = 3;
}
```

- [ ] **Step 5: Regenerate, lint, build**

Run: `task proto:generate`
Expected: `pkg/agentproto/boxyagent/v1/agent.pb.go`/`agent_grpc.pb.go` rewritten with the new types.

Run: `task proto:lint`
Expected: no lint errors.

Run: `go build ./pkg/agentproto/...`
Expected: succeeds.

- [ ] **Step 6: Commit**

```bash
git add proto/boxyagent/v1/agent.proto pkg/agentproto/boxyagent/v1/agent.pb.go pkg/agentproto/boxyagent/v1/agent_grpc.pb.go
git commit -m "feat(agentproto): add MeshIdentity/AddMeshPeer/RemoveMeshPeer wire messages

Part of #224."
```

#### 4.2 — `EmbeddedAgent`

- [ ] **Step 1: Write the failing tests**

```go
// append to pkg/agentsdk/embedded_test.go

type fakeMeshPeeringDriver struct {
	*fakeDriver
	identityPub, identityEndpoint, identityCIDR string
	identityErr                                 error
	addPeerErr, removePeerErr                    error
	gotAddPeerKey, gotAddPeerEndpoint, gotAddPeerCIDR string
	gotRemovePeerKey                                  string
}

func (f *fakeMeshPeeringDriver) MeshIdentity(_ context.Context, _ providersdk.SegmentRef) (string, string, string, error) {
	return f.identityPub, f.identityEndpoint, f.identityCIDR, f.identityErr
}
func (f *fakeMeshPeeringDriver) AddMeshPeer(_ context.Context, _ providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	f.gotAddPeerKey, f.gotAddPeerEndpoint, f.gotAddPeerCIDR = peerPublicKey, peerEndpoint, peerCIDR
	return f.addPeerErr
}
func (f *fakeMeshPeeringDriver) RemoveMeshPeer(_ context.Context, _ providersdk.SegmentRef, peerPublicKey string) error {
	f.gotRemovePeerKey = peerPublicKey
	return f.removePeerErr
}

func TestEmbeddedAgent_MeshIdentity(t *testing.T) {
	driver := &fakeMeshPeeringDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, identityPub: "pub1", identityEndpoint: "203.0.113.5:51820", identityCIDR: "10.250.0.0/29"}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	pub, endpoint, cidr, err := agent.MeshIdentity(context.Background(), "hyperv", "boxy-sb-sb-1")
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub != "pub1" || endpoint != "203.0.113.5:51820" || cidr != "10.250.0.0/29" {
		t.Fatalf("got (%q, %q, %q)", pub, endpoint, cidr)
	}
}

func TestEmbeddedAgent_AddMeshPeer(t *testing.T) {
	driver := &fakeMeshPeeringDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if err := agent.AddMeshPeer(context.Background(), "hyperv", "boxy-sb-sb-1", "pub2", "203.0.113.9:51820", "10.250.0.8/29"); err != nil {
		t.Fatalf("AddMeshPeer: %v", err)
	}
	if driver.gotAddPeerKey != "pub2" || driver.gotAddPeerEndpoint != "203.0.113.9:51820" || driver.gotAddPeerCIDR != "10.250.0.8/29" {
		t.Fatalf("driver got (%q, %q, %q)", driver.gotAddPeerKey, driver.gotAddPeerEndpoint, driver.gotAddPeerCIDR)
	}
}

func TestEmbeddedAgent_RemoveMeshPeer(t *testing.T) {
	driver := &fakeMeshPeeringDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if err := agent.RemoveMeshPeer(context.Background(), "hyperv", "boxy-sb-sb-1", "pub2"); err != nil {
		t.Fatalf("RemoveMeshPeer: %v", err)
	}
	if driver.gotRemovePeerKey != "pub2" {
		t.Fatalf("driver got %q, want pub2", driver.gotRemovePeerKey)
	}
}

func TestEmbeddedAgent_MeshIdentityUnsupportedDriverErrors(t *testing.T) {
	driver := &fakeDriver{providerType: "docker"}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if _, _, _, err := agent.MeshIdentity(context.Background(), "docker", "net-1"); err == nil {
		t.Fatal("expected an error for a driver that does not implement MeshPeerer")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestEmbeddedAgent_.*Mesh -v`
Expected: compile failure.

- [ ] **Step 3: Define `agentsdk.MeshPeeringAgent` and implement it on `EmbeddedAgent`**

In `pkg/agentsdk/agent.go`, after `NetworkIsolatingAgent`:

```go
// MeshPeeringAgent is an optional agent capability for providers that
// implement providersdk.MeshPeerer. Like NetworkIsolatingAgent, an
// unsupported driver is a caller error (no fallback path), since a caller
// reaching these methods already type-asserted for this capability.
type MeshPeeringAgent interface {
	MeshIdentity(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) (publicKey, endpoint, cidr string, err error)
	AddMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error
	RemoveMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey string) error
}
```

In `pkg/agentsdk/embedded.go`, add to the `var (...)` block: `_ MeshPeeringAgent = (*EmbeddedAgent)(nil)`. Then, after the `DestroySegment` method:

```go
func (a *EmbeddedAgent) MeshIdentity(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) (string, string, string, error) {
	d, err := a.driver(provider)
	if err != nil {
		return "", "", "", err
	}
	peerer, ok := d.(providersdk.MeshPeerer)
	if !ok {
		return "", "", "", fmt.Errorf("agent %q: provider %q does not support mesh peering", a.info.ID, provider)
	}
	return peerer.MeshIdentity(ctx, ref)
}

func (a *EmbeddedAgent) AddMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	d, err := a.driver(provider)
	if err != nil {
		return err
	}
	peerer, ok := d.(providersdk.MeshPeerer)
	if !ok {
		return fmt.Errorf("agent %q: provider %q does not support mesh peering", a.info.ID, provider)
	}
	return peerer.AddMeshPeer(ctx, ref, peerPublicKey, peerEndpoint, peerCIDR)
}

func (a *EmbeddedAgent) RemoveMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey string) error {
	d, err := a.driver(provider)
	if err != nil {
		return err
	}
	peerer, ok := d.(providersdk.MeshPeerer)
	if !ok {
		return fmt.Errorf("agent %q: provider %q does not support mesh peering", a.info.ID, provider)
	}
	return peerer.RemoveMeshPeer(ctx, ref, peerPublicKey)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestEmbeddedAgent_.*Mesh -v`
Expected: PASS (all four).

- [ ] **Step 5: Run the full agentsdk package test suite**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/agentsdk/agent.go pkg/agentsdk/embedded.go pkg/agentsdk/embedded_test.go
git commit -m "feat(agentsdk): implement MeshPeeringAgent on EmbeddedAgent

Part of #224."
```

#### 4.3 — `RemoteAgent`

- [ ] **Step 1: Write the failing tests**

```go
// append to pkg/agentsdk/remote_test.go

func TestRemoteAgent_MeshIdentityRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		pub, endpoint, cidr string
		err                 error
	}
	resultCh := make(chan result, 1)
	go func() {
		pub, endpoint, cidr, err := a.MeshIdentity(context.Background(), "hyperv", "boxy-sb-sb-1")
		resultCh <- result{pub, endpoint, cidr, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	mi := cmd.GetMeshIdentity()
	if mi == nil || mi.GetSegmentRef() != "boxy-sb-sb-1" {
		t.Fatalf("expected a MeshIdentityCommand for boxy-sb-sb-1, got %#v", cmd)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_MeshIdentity{MeshIdentity: &boxyagentv1.MeshIdentityResult{
			PublicKey: "pub1", Endpoint: "203.0.113.5:51820", Cidr: "10.250.0.0/29",
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("MeshIdentity returned error: %v", r.err)
		}
		if r.pub != "pub1" || r.endpoint != "203.0.113.5:51820" || r.cidr != "10.250.0.0/29" {
			t.Fatalf("got (%q, %q, %q)", r.pub, r.endpoint, r.cidr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MeshIdentity to return")
	}
}

func TestRemoteAgent_AddMeshPeerRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.AddMeshPeer(context.Background(), "hyperv", "boxy-sb-sb-1", "pub2", "203.0.113.9:51820", "10.250.0.8/29")
	}()

	cmd := recvCommand(t, stream.sentCh)
	add := cmd.GetAddMeshPeer()
	if add == nil || add.GetSegmentRef() != "boxy-sb-sb-1" || add.GetPeerPublicKey() != "pub2" || add.GetPeerEndpoint() != "203.0.113.9:51820" || add.GetPeerCidr() != "10.250.0.8/29" {
		t.Fatalf("unexpected AddMeshPeerCommand: %#v", add)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_AddMeshPeer{AddMeshPeer: &emptypb.Empty{}},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("AddMeshPeer returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for AddMeshPeer to return")
	}
}

func TestRemoteAgent_RemoveMeshPeerRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.RemoveMeshPeer(context.Background(), "hyperv", "boxy-sb-sb-1", "pub2")
	}()

	cmd := recvCommand(t, stream.sentCh)
	remove := cmd.GetRemoveMeshPeer()
	if remove == nil || remove.GetSegmentRef() != "boxy-sb-sb-1" || remove.GetPeerPublicKey() != "pub2" {
		t.Fatalf("unexpected RemoveMeshPeerCommand: %#v", remove)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_RemoveMeshPeer{RemoveMeshPeer: &emptypb.Empty{}},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RemoveMeshPeer returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RemoveMeshPeer to return")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestRemoteAgent_.*Mesh -v`
Expected: compile failure.

- [ ] **Step 3: Implement**

In `pkg/agentsdk/remote.go`, add `var _ MeshPeeringAgent = (*RemoteAgent)(nil)`, then:

```go
func (a *RemoteAgent) MeshIdentity(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) (string, string, string, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_MeshIdentity{MeshIdentity: &boxyagentv1.MeshIdentityCommand{SegmentRef: string(ref)}},
	})
	if err != nil {
		return "", "", "", err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return "", "", "", reconstructAgentError(a.info.ID, agentErr)
	}
	mi := res.GetMeshIdentity()
	return mi.GetPublicKey(), mi.GetEndpoint(), mi.GetCidr(), nil
}

func (a *RemoteAgent) AddMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op: &boxyagentv1.Command_AddMeshPeer{AddMeshPeer: &boxyagentv1.AddMeshPeerCommand{
			SegmentRef: string(ref), PeerPublicKey: peerPublicKey, PeerEndpoint: peerEndpoint, PeerCidr: peerCIDR,
		}},
	})
	if err != nil {
		return err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return reconstructAgentError(a.info.ID, agentErr)
	}
	return nil
}

func (a *RemoteAgent) RemoveMeshPeer(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef, peerPublicKey string) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_RemoveMeshPeer{RemoveMeshPeer: &boxyagentv1.RemoveMeshPeerCommand{SegmentRef: string(ref), PeerPublicKey: peerPublicKey}},
	})
	if err != nil {
		return err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return reconstructAgentError(a.info.ID, agentErr)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestRemoteAgent_.*Mesh -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full agentsdk package test suite**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/agentsdk/remote.go pkg/agentsdk/remote_test.go
git commit -m "feat(agentsdk): implement MeshPeeringAgent on RemoteAgent

Part of #224."
```

#### 4.4 — agent-side dispatch in `remoteclient.go`

- [ ] **Step 1: Write the failing tests**

```go
// append as new t.Run cases inside TestExecuteCommand in
// pkg/agentsdk/remoteclient_test.go, after the destroy-segment cases

	t.Run("mesh identity success", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakeMeshPeeringDriver{
			fakeDriver:  &fakeDriver{providerType: "hyperv"},
			identityPub: "pub1", identityEndpoint: "203.0.113.5:51820", identityCIDR: "10.250.0.0/29",
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-30",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_MeshIdentity{MeshIdentity: &boxyagentv1.MeshIdentityCommand{SegmentRef: "boxy-sb-sb-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		mi := res.GetMeshIdentity()
		if mi.GetPublicKey() != "pub1" || mi.GetEndpoint() != "203.0.113.5:51820" || mi.GetCidr() != "10.250.0.0/29" {
			t.Fatalf("unexpected MeshIdentityResult: %#v", mi)
		}
	})

	t.Run("mesh identity unsupported by driver errors", func(t *testing.T) {
		drivers := DriverSet{"docker": &fakeDriver{providerType: "docker"}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-31",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_MeshIdentity{MeshIdentity: &boxyagentv1.MeshIdentityCommand{SegmentRef: "net-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil {
			t.Fatal("expected an error for a driver that does not implement MeshPeerer")
		}
	})

	t.Run("add mesh peer success", func(t *testing.T) {
		driver := &fakeMeshPeeringDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
		drivers := DriverSet{"hyperv": driver}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-32",
			ProviderType: "hyperv",
			Op: &boxyagentv1.Command_AddMeshPeer{AddMeshPeer: &boxyagentv1.AddMeshPeerCommand{
				SegmentRef: "boxy-sb-sb-1", PeerPublicKey: "pub2", PeerEndpoint: "203.0.113.9:51820", PeerCidr: "10.250.0.8/29",
			}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if res.GetAddMeshPeer() == nil {
			t.Fatalf("expected an AddMeshPeer (empty) outcome, got %#v", res.GetOutcome())
		}
		if driver.gotAddPeerKey != "pub2" || driver.gotAddPeerEndpoint != "203.0.113.9:51820" || driver.gotAddPeerCIDR != "10.250.0.8/29" {
			t.Fatalf("driver got (%q, %q, %q)", driver.gotAddPeerKey, driver.gotAddPeerEndpoint, driver.gotAddPeerCIDR)
		}
	})

	t.Run("remove mesh peer success", func(t *testing.T) {
		driver := &fakeMeshPeeringDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
		drivers := DriverSet{"hyperv": driver}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-33",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_RemoveMeshPeer{RemoveMeshPeer: &boxyagentv1.RemoveMeshPeerCommand{SegmentRef: "boxy-sb-sb-1", PeerPublicKey: "pub2"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if res.GetRemoveMeshPeer() == nil {
			t.Fatalf("expected a RemoveMeshPeer (empty) outcome, got %#v", res.GetOutcome())
		}
		if driver.gotRemovePeerKey != "pub2" {
			t.Fatalf("driver got %q, want pub2", driver.gotRemovePeerKey)
		}
	})
```

(`fakeMeshPeeringDriver` was defined in `embedded_test.go` by Task 4.2, same package — reused here.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestExecuteCommand -v`
Expected: FAIL on the four new subtests (no dispatch case yet — they fall into `default: unknown command op`).

- [ ] **Step 3: Add the dispatch cases**

In `pkg/agentsdk/remoteclient.go`'s `executeCommand` switch, after the `case *boxyagentv1.Command_DestroySegment:` block:

```go
	case *boxyagentv1.Command_MeshIdentity:
		peerer, ok := d.(providersdk.MeshPeerer)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support mesh peering", cmd.GetProviderType()), nil)
		}
		pub, endpoint, cidr, err := peerer.MeshIdentity(ctx, providersdk.SegmentRef(op.MeshIdentity.GetSegmentRef()))
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_MeshIdentity{MeshIdentity: &boxyagentv1.MeshIdentityResult{PublicKey: pub, Endpoint: endpoint, Cidr: cidr}},
		}

	case *boxyagentv1.Command_AddMeshPeer:
		peerer, ok := d.(providersdk.MeshPeerer)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support mesh peering", cmd.GetProviderType()), nil)
		}
		if err := peerer.AddMeshPeer(ctx, providersdk.SegmentRef(op.AddMeshPeer.GetSegmentRef()), op.AddMeshPeer.GetPeerPublicKey(), op.AddMeshPeer.GetPeerEndpoint(), op.AddMeshPeer.GetPeerCidr()); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_AddMeshPeer{AddMeshPeer: &emptypb.Empty{}},
		}

	case *boxyagentv1.Command_RemoveMeshPeer:
		peerer, ok := d.(providersdk.MeshPeerer)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support mesh peering", cmd.GetProviderType()), nil)
		}
		if err := peerer.RemoveMeshPeer(ctx, providersdk.SegmentRef(op.RemoveMeshPeer.GetSegmentRef()), op.RemoveMeshPeer.GetPeerPublicKey()); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_RemoveMeshPeer{RemoveMeshPeer: &emptypb.Empty{}},
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestExecuteCommand -v`
Expected: PASS (all subtests, including the four new ones).

- [ ] **Step 5: Run the full agentsdk package test suite, then the full repository build/test/lint**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (rerun the known pre-existing Windows `t.TempDir()` `internal/cli` flake in isolation if it appears).

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add pkg/agentsdk/remoteclient.go pkg/agentsdk/remoteclient_test.go
git commit -m "feat(agentsdk): dispatch MeshIdentity/AddMeshPeer/RemoveMeshPeer to the local driver

Closes the agent-wiring half of #224's mesh (Decision 2). Nothing calls
this from the control plane yet -- sandbox.Manager wiring is Task 5."
```

---

### Task 5: `sandbox.Manager` mesh-peering trigger

**Files:**
- Modify: `internal/sandbox/allocator.go` (new `MeshPeeringAllocator` optional capability)
- Modify: `internal/pool/provisioner_agent.go` (`AgentProvisioner` implements it)
- Modify: `internal/sandbox/manager.go` (trigger logic inside `ensureNetworkSegment`, Plan 1c)
- Test: `internal/sandbox/manager_test.go`

**Interfaces:**
- Consumes: `agentsdk.MeshPeeringAgent` (Task 4).
- Produces: whenever `ensureNetworkSegment` (Plan 1c) causes `sb.NetworkSegments` to grow past one entry, every agent already in the list gets peered with the newly-added one (full mesh, pairwise).

- [ ] **Step 1: Write the failing test**

```go
// append to internal/sandbox/manager_test.go

type fakeMeshPeeringAllocator struct {
	*fakeIsolatingAllocator // from Plan 1c Task 4, same file
	identities map[string]struct{ pub, endpoint, cidr string } // keyed by agentID
	peerCalls  []struct{ toAgentID, peerPublicKey, peerEndpoint, peerCIDR string }
}

func (f *fakeMeshPeeringAllocator) MeshIdentity(_ context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef) (string, string, string, error) {
	id := f.identities[agentID]
	return id.pub, id.endpoint, id.cidr, nil
}
func (f *fakeMeshPeeringAllocator) AddMeshPeer(_ context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	f.peerCalls = append(f.peerCalls, struct{ toAgentID, peerPublicKey, peerEndpoint, peerCIDR string }{agentID, peerPublicKey, peerEndpoint, peerCIDR})
	return nil
}

func TestManager_EnsureNetworkSegment_PeersWhenSandboxSpansTwoAgents(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{
		ID:     "sb-1",
		Status: model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"},
		},
	}

	allocator := &fakeMeshPeeringAllocator{
		fakeIsolatingAllocator: &fakeIsolatingAllocator{createRef: "boxy-sb-sb-1", createType: "hyperv"},
		identities: map[string]struct{ pub, endpoint, cidr string }{
			"agent-1": {"pubkey-1", "203.0.113.1:51820", "10.250.0.0/29"},
			"agent-2": {"pubkey-2", "203.0.113.2:51820", "10.250.0.8/29"},
		},
	}
	m := New(st, allocator)

	res := model.Resource{ID: "res-2", Provider: model.ProviderRef{AgentID: "agent-2"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-b"}, res); err != nil {
		t.Fatalf("ensureNetworkSegment: %v", err)
	}

	if len(sb.NetworkSegments) != 2 {
		t.Fatalf("NetworkSegments = %+v, want 2 entries (sandbox now spans agent-1 and agent-2)", sb.NetworkSegments)
	}
	if len(allocator.peerCalls) != 2 {
		t.Fatalf("expected exactly 2 AddMeshPeer calls (agent-1<-agent-2's identity, agent-2<-agent-1's identity), got %d: %+v", len(allocator.peerCalls), allocator.peerCalls)
	}
}

func TestManager_EnsureNetworkSegment_SingleHostSandboxNeverTriggersMesh(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady}

	allocator := &fakeMeshPeeringAllocator{
		fakeIsolatingAllocator: &fakeIsolatingAllocator{createRef: "boxy-sb-sb-1", createType: "hyperv"},
		identities:             map[string]struct{ pub, endpoint, cidr string }{},
	}
	m := New(st, allocator)

	res := model.Resource{ID: "res-1", Provider: model.ProviderRef{AgentID: "agent-1"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-a"}, res); err != nil {
		t.Fatalf("ensureNetworkSegment: %v", err)
	}
	// A second resource from the SAME agent must not trigger mesh either.
	res2 := model.Resource{ID: "res-2", Provider: model.ProviderRef{AgentID: "agent-1"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-a"}, res2); err != nil {
		t.Fatalf("ensureNetworkSegment (2nd resource, same agent): %v", err)
	}

	if len(allocator.peerCalls) != 0 {
		t.Fatalf("expected 0 AddMeshPeer calls for a single-host sandbox, got %d: %+v", len(allocator.peerCalls), allocator.peerCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run TestManager_EnsureNetworkSegment -v`
Expected: compile failure — `MeshPeeringAllocator`/the new fake methods undefined, or (once compiling) the peering test fails with 0 `peerCalls`.

- [ ] **Step 3: Define `MeshPeeringAllocator` and implement it on `AgentProvisioner`**

In `internal/sandbox/allocator.go`:

```go
// MeshPeeringAllocator is an optional capability for allocators whose
// underlying agent/driver supports cross-host mesh peering
// (providersdk.MeshPeerer, via agentsdk.MeshPeeringAgent). Manager uses it
// only when a sandbox's segments span more than one agent -- a single-host
// sandbox never touches this.
type MeshPeeringAllocator interface {
	MeshIdentity(ctx context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef) (publicKey, endpoint, cidr string, err error)
	AddMeshPeer(ctx context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error
}
```

In `internal/pool/provisioner_agent.go`, implement it by resolving the agent directly from `ap.Registry.Get(agentID)` (no `res` needed here, unlike `CreateSegment`/`AttachToSegment` -- `MeshIdentity`/`AddMeshPeer` operate purely in terms of already-known agent IDs and segment refs):

```go
func (ap *AgentProvisioner) MeshIdentity(ctx context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef) (string, string, string, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return "", "", "", fmt.Errorf("unknown pool %q", pool.Name)
	}
	agent, ok := ap.Registry.Get(agentID)
	if !ok {
		return "", "", "", fmt.Errorf("agent %q unavailable", agentID)
	}
	peerer, ok := agent.(agentsdk.MeshPeeringAgent)
	if !ok {
		return "", "", "", fmt.Errorf("agent %q does not support mesh peering", agentID)
	}
	return peerer.MeshIdentity(ctx, ap.driverTypeForPool(spec), ref)
}

func (ap *AgentProvisioner) AddMeshPeer(ctx context.Context, pool model.Pool, agentID string, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return fmt.Errorf("unknown pool %q", pool.Name)
	}
	agent, ok := ap.Registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q unavailable", agentID)
	}
	peerer, ok := agent.(agentsdk.MeshPeeringAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support mesh peering", agentID)
	}
	return peerer.AddMeshPeer(ctx, ap.driverTypeForPool(spec), ref, peerPublicKey, peerEndpoint, peerCIDR)
}
```

- [ ] **Step 4: Wire the trigger into `ensureNetworkSegment`**

In `internal/sandbox/manager.go`, extend `ensureNetworkSegment` (Plan 1c) — after the `sb.NetworkSegments = append(...)` line that records the newly-created segment, before `return nil`:

```go
	if err := m.triggerMeshPeering(ctx, sb, pool, agentID); err != nil {
		return fmt.Errorf("establish mesh peering for sandbox %q: %w", sb.ID, err)
	}
	return nil
}

// triggerMeshPeering peers the sandbox's newly-added agent (identified by
// newAgentID) with every agent already in sb.NetworkSegments -- full mesh,
// pairwise. A no-op if m.allocator doesn't support MeshPeeringAllocator, or
// if this is the sandbox's first (and so far only) segment (nothing to
// peer with yet).
func (m *Manager) triggerMeshPeering(ctx context.Context, sb *model.Sandbox, pool model.Pool, newAgentID string) error {
	peerer, ok := m.allocator.(MeshPeeringAllocator)
	if !ok {
		return nil
	}
	if len(sb.NetworkSegments) < 2 {
		return nil
	}
	var newRef providersdk.SegmentRef
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == newAgentID {
			newRef = providersdk.SegmentRef(seg.Ref)
			break
		}
	}
	newPub, newEndpoint, newCIDR, err := peerer.MeshIdentity(ctx, pool, newAgentID, newRef)
	if err != nil {
		return err
	}
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == newAgentID {
			continue
		}
		existingPub, existingEndpoint, existingCIDR, err := peerer.MeshIdentity(ctx, pool, seg.AgentID, providersdk.SegmentRef(seg.Ref))
		if err != nil {
			return err
		}
		if err := peerer.AddMeshPeer(ctx, pool, seg.AgentID, providersdk.SegmentRef(seg.Ref), newPub, newEndpoint, newCIDR); err != nil {
			return err
		}
		if err := peerer.AddMeshPeer(ctx, pool, newAgentID, newRef, existingPub, existingEndpoint, existingCIDR); err != nil {
			return err
		}
	}
	return nil
}
```

Note the exact insertion point in `ensureNetworkSegment`'s existing body (Plan 1c): the `agentID` variable this new code references is the same one already in scope there (`agentID := res.Provider.AgentID`, computed at the top of `ensureNetworkSegment`).

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestManager_EnsureNetworkSegment -v`
Expected: PASS (both).

- [ ] **Step 6: Run the full sandbox and pool package test suites, then the full repository build/test/lint**

Run: `go test ./internal/sandbox/... ./internal/pool/...`
Expected: PASS, no regressions.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (rerun the known pre-existing Windows `t.TempDir()` `internal/cli` flake in isolation if it appears).

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/allocator.go internal/pool/provisioner_agent.go internal/sandbox/manager.go internal/sandbox/manager_test.go
git commit -m "feat(sandbox): trigger full-mesh peering when a sandbox spans hosts

Closes #224's Decision 2 (cross-host WireGuard mesh) end to end. A
single-host sandbox never touches mesh peering; a sandbox spanning N
hosts gets a full pairwise mesh among exactly those N agents' segments,
nothing global.

Closes #224 (Decisions 1 and 2 complete). JIT access (Decision 3) and the
AccessBroker extension point (Decision 4) remain separate plans."
```

---

## After This Plan

Decisions 1 and 2 of the spec are fully implemented and wired end to end. Two follow-ups explicitly flagged during this plan and not built here:

- **Mesh interfaces are never closed by `DestroySegment`.** Both drivers' `closeMeshInterfaces` exist only for test cleanup right now. `DestroySegment` (Plan 1a) should call it for the specific segment being destroyed — a small, well-scoped follow-up once this plan lands, not folded in here to keep this plan's diff focused on peering itself.
- **No mesh orphan-sweep**, mirroring Plan 1c's identical, already-flagged segment orphan-sweep gap — a crash between establishing a peer and the sandbox record reflecting it leaves a stray peer entry in a live `meshnet.Interface` with nothing pointing at it.

Decision 3 (JIT native-protocol access) and Decision 4 (`AccessBroker` extension point) remain separate, not-yet-written plans.
