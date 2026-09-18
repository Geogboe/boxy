# Overlay Network Fabric — Sandbox Allocation/Deletion Wiring (Plan 1c) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Plans 1a/1b actually run: every time `internal/sandbox.Manager` allocates a resource into a sandbox, it creates (or reuses) that sandbox's segment on the resource's host and attaches the resource to it; every time a sandbox is deleted, its segments are torn down.

**Architecture:** A new optional `internal/sandbox.NetworkIsolatingAllocator` capability (mirroring the existing `PackageSandboxAllocator` pattern in the same file), implemented by `AgentProvisioner` by resolving the resource's agent and calling the `agentsdk.NetworkIsolatingAgent` methods from Plan 1b. `Manager` calls it from both places resources get allocated into a sandbox, tracking which segment exists on which agent in a new `model.Sandbox.NetworkSegments` field so deletion (`internal/sandbox/deleter.go`) knows what to tear down. `DriverProvisioner` (already marked deprecated in its own doc comment) is deliberately **not** given this capability.

**Tech Stack:** Go 1.25 — no new external dependencies.

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan completes Decision 1 (driver-native isolation) end to end. Decisions 2–4 (mesh, JIT access, AccessBroker) are separate plans.

## Global Constraints

- Isolation is automatic — there is no sandbox-level opt-in flag anywhere in this plan. A resource is attached to a segment whenever the underlying allocator supports `NetworkIsolatingAllocator`; nothing checks a sandbox spec field to decide whether to do it.
- `DriverProvisioner` does not get this capability. It's already marked "Deprecated: use AgentProvisioner" in its own doc comment — extending a deprecated path is wasted work.
- A resource whose allocator does **not** implement `NetworkIsolatingAllocator` (e.g. a devfactory-backed sandbox, or any test using a plain `Provisioner`/`SandboxAllocator` fake) allocates exactly as it does today — no segment, no error, no behavior change. This is the expected, permanent state for providers that don't implement Plan 1a's `providersdk.NetworkIsolator` (see that plan's devfactory non-goal), not a gap to fix later.
- Segment orphan-sweep (a segment surviving a crash between `CreateSegment` succeeding and `Sandbox.NetworkSegments` being persisted) is **not** built in this plan — flagged explicitly in "After This Plan" as a known, deliberately deferred follow-up, not a silent gap.
- `NetworkSegments` is keyed by agent ID only, not `(agent ID, provider type)`. An agent that hosts more than one provider type could theoretically collide; every example agent config in this codebase today hosts exactly one provider type, so this is a documented, accepted limitation, not something to build extra plumbing for now.

---

### Task 1: `model.Sandbox.NetworkSegments` field

**Files:**
- Modify: `pkg/model/sandbox.go`
- Test: `pkg/model/sandbox_test.go` (create if it doesn't exist — check first with `ls pkg/model/*_test.go`)

**Interfaces:**
- Produces: `model.NetworkSegment{AgentID, ProviderType, Ref string}`, `model.Sandbox.NetworkSegments []NetworkSegment`.

This is a pure data-shape addition — JSON/YAML round-trip is the only thing worth testing here (everything else is exercised by later tasks).

- [ ] **Step 1: Write the failing test**

```go
// pkg/model/sandbox_test.go (add to existing file, or create it if none exists)
package model_test

import (
	"encoding/json"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestSandbox_NetworkSegmentsRoundTripsThroughJSON(t *testing.T) {
	sb := model.Sandbox{
		ID: "sb-1",
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"},
		},
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.Sandbox
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.NetworkSegments) != 1 || got.NetworkSegments[0].Ref != "boxy-sb-sb-1" {
		t.Fatalf("NetworkSegments did not round-trip: %+v", got.NetworkSegments)
	}
}

func TestSandbox_NoNetworkSegmentsOmittedFromJSON(t *testing.T) {
	sb := model.Sandbox{ID: "sb-1"}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); containsField(got, "network_segments") {
		t.Fatalf("expected network_segments to be omitted when empty, got %s", got)
	}
}

func containsField(jsonStr, field string) bool {
	return len(jsonStr) > 0 && jsonContains(jsonStr, `"`+field+`"`)
}

func jsonContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/model/... -run TestSandbox_NetworkSegments -v`
Expected: compile failure — `model.NetworkSegment`, `Sandbox.NetworkSegments` undefined.

- [ ] **Step 3: Add the field and type**

In `pkg/model/sandbox.go`, after the existing `ExpiresAt` field on `Sandbox`:

```go
	// NetworkSegments records the private network segment(s) this sandbox
	// was given, one per agent/host its resources landed on (see
	// providersdk.NetworkIsolator / internal/sandbox.NetworkIsolatingAllocator).
	// Empty for any sandbox whose allocator doesn't support network
	// isolation (e.g. devfactory-backed) -- this is the expected steady
	// state for such sandboxes, not a partial/incomplete one.
	NetworkSegments []NetworkSegment `json:"network_segments,omitempty" yaml:"network_segments,omitempty"`
```

At the bottom of the same file:

```go
// NetworkSegment is one per-host private network segment a sandbox was
// given. Ref is an opaque provider-defined identifier (mirrors
// providersdk.SegmentRef as a plain string, so this package doesn't import
// providersdk) -- callers pass it back to the same agent/provider type
// unchanged, never interpreting it themselves.
type NetworkSegment struct {
	AgentID      string `json:"agent_id" yaml:"agent_id"`
	ProviderType string `json:"provider_type" yaml:"provider_type"`
	Ref          string `json:"ref" yaml:"ref"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/model/... -run TestSandbox_NetworkSegments -v`
Expected: PASS (both).

- [ ] **Step 5: Run the full model package test suite to check for regressions**

Run: `go test ./pkg/model/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/model/sandbox.go pkg/model/sandbox_test.go
git commit -m "feat(model): add Sandbox.NetworkSegments

Part of #224."
```

---

### Task 2: `internal/sandbox.NetworkIsolatingAllocator` capability

**Files:**
- Modify: `internal/sandbox/allocator.go`
- Test: `internal/sandbox/allocator_test.go` (create — check first with `ls internal/sandbox/allocator_test.go`)

**Interfaces:**
- Consumes: `providersdk.SegmentRef`, `providersdk.Type` (Plan 1a).
- Produces: `NetworkIsolatingAllocator` interface — `CreateSegment(ctx, pool, res, sandboxID) (providersdk.SegmentRef, providersdk.Type, error)`, `AttachToSegment(ctx, pool, res, ref) error`.

Same shape as Task 1 in the earlier plans — this task only defines the contract; Task 3 implements it.

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/allocator_test.go
package sandbox

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeIsolatingAllocator struct {
	*fakeAllocator // reuse this package's existing base SandboxAllocator fake if one exists; otherwise define a minimal one alongside this test (see Step 1 note below)
	createRef  providersdk.SegmentRef
	createType providersdk.Type
	createErr  error
	attachErr  error
	gotPool    model.Pool
	gotRes     model.Resource
	gotSbID    model.SandboxID
	gotAttachRef providersdk.SegmentRef
}

func (f *fakeIsolatingAllocator) CreateSegment(_ context.Context, pool model.Pool, res model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error) {
	f.gotPool, f.gotRes, f.gotSbID = pool, res, sandboxID
	return f.createRef, f.createType, f.createErr
}

func (f *fakeIsolatingAllocator) AttachToSegment(_ context.Context, _ model.Pool, _ model.Resource, ref providersdk.SegmentRef) error {
	f.gotAttachRef = ref
	return f.attachErr
}

func TestNetworkIsolatingAllocator_SatisfiedByTypeAssertion(t *testing.T) {
	var a SandboxAllocator = &fakeIsolatingAllocator{createRef: "seg-1", createType: "hyperv"}
	isolator, ok := a.(NetworkIsolatingAllocator)
	if !ok {
		t.Fatal("allocator implementing NetworkIsolatingAllocator's methods was not detected via type assertion")
	}
	ref, providerType, err := isolator.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, model.Resource{ID: "res-1"}, "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "seg-1" || providerType != "hyperv" {
		t.Fatalf("got (%q, %q), want (seg-1, hyperv)", ref, providerType)
	}
}
```

Check whether this package already has a base `SandboxAllocator`-only fake (`grep -n "type fake.*Allocator" internal/sandbox/*_test.go`). If one exists, embed it as shown; if not, define a minimal one in this same test file:

```go
type fakeAllocator struct{}

func (fakeAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{}, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run TestNetworkIsolatingAllocator -v`
Expected: compile failure — `NetworkIsolatingAllocator` undefined.

- [ ] **Step 3: Write the interface**

In `internal/sandbox/allocator.go`, after `PackageSandboxAllocator`:

```go
// NetworkIsolatingAllocator is an optional capability for allocators whose
// underlying agent/driver supports per-sandbox network isolation
// (providersdk.NetworkIsolator, via agentsdk.NetworkIsolatingAgent). Not
// every provider implements it (devfactory deliberately does not -- see
// the design spec's Decision 1), so Manager must type-assert.
//
// CreateSegment also returns the resolved provider type (needed by Manager
// to record model.NetworkSegment.ProviderType for later destroy calls,
// which have no model.Pool/model.Resource in scope to re-resolve it from).
type NetworkIsolatingAllocator interface {
	SandboxAllocator
	CreateSegment(ctx context.Context, pool model.Pool, res model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error)
	AttachToSegment(ctx context.Context, pool model.Pool, res model.Resource, ref providersdk.SegmentRef) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestNetworkIsolatingAllocator -v`
Expected: PASS.

- [ ] **Step 5: Run the full sandbox package test suite to check for regressions**

Run: `go test ./internal/sandbox/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/allocator.go internal/sandbox/allocator_test.go
git commit -m "feat(sandbox): add NetworkIsolatingAllocator capability

Part of #224."
```

---

### Task 3: `AgentProvisioner` implements `NetworkIsolatingAllocator`

**Files:**
- Modify: `internal/pool/provisioner_agent.go`
- Test: `internal/pool/provisioner_agent_test.go` (or wherever `AgentProvisioner`'s existing tests live — check with `grep -rl "func TestAgentProvisioner" internal/pool/*_test.go`)

**Interfaces:**
- Consumes: `agentsdk.NetworkIsolatingAgent` (Plan 1b), `ap.agentForResource(res)` and `ap.driverTypeForPool(spec)` (existing, `provisioner_agent.go`).
- Produces: `(*AgentProvisioner)` satisfies `sandbox.NetworkIsolatingAllocator`.

- [ ] **Step 1: Write the failing tests**

First, find the existing agent fake used by this file's tests:

Run: `grep -rn "type fake.*Agent struct" internal/pool/*_test.go`

Use whatever that turns up (it should already embed or implement `agentsdk.Agent`) as the base for a new capability-adding fake, the same "embed the base, add the new methods" shape Plan 1b's `fakeIsolatingDriver` used:

```go
// append to internal/pool's AgentProvisioner test file

type fakeIsolatingAgent struct {
	agentsdk.Agent // embed whatever this package's existing fake agent type is, if it satisfies agentsdk.Agent; otherwise embed that fake directly by name
	createRef     providersdk.SegmentRef
	createErr     error
	attachErr     error
	gotSandboxID  string
	gotResourceID string
	gotAttachRef  providersdk.SegmentRef
}

func (f *fakeIsolatingAgent) CreateSegment(_ context.Context, _ providersdk.Type, sandboxID string) (providersdk.SegmentRef, error) {
	f.gotSandboxID = sandboxID
	return f.createRef, f.createErr
}

func (f *fakeIsolatingAgent) AttachToSegment(_ context.Context, _ providersdk.Type, providerResourceID string, ref providersdk.SegmentRef) error {
	f.gotResourceID, f.gotAttachRef = providerResourceID, ref
	return f.attachErr
}

func TestAgentProvisioner_CreateSegment(t *testing.T) {
	agent := &fakeIsolatingAgent{createRef: "boxy-sb-sb-1"}
	registry := NewAgentRegistry() // or however this package's tests already construct one
	registry.Register(agent, []providersdk.Type{"hyperv"}) // match the real registration call this package's other AgentProvisioner tests use
	ap := &AgentProvisioner{
		Registry: registry,
		Specs:    map[model.PoolName]boxyconfig.PoolSpec{"pool-a": {Type: "hyperv"}},
	}
	res := model.Resource{ID: "res-1", Provider: model.ProviderRef{AgentID: agent.Info().ID}}

	ref, providerType, err := ap.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, res, "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "boxy-sb-sb-1" || providerType != "hyperv" {
		t.Fatalf("got (%q, %q), want (boxy-sb-sb-1, hyperv)", ref, providerType)
	}
	if agent.gotSandboxID != "sb-1" {
		t.Fatalf("agent got sandboxID = %q, want sb-1", agent.gotSandboxID)
	}
}

func TestAgentProvisioner_AttachToSegment(t *testing.T) {
	agent := &fakeIsolatingAgent{}
	registry := NewAgentRegistry()
	registry.Register(agent, []providersdk.Type{"hyperv"})
	ap := &AgentProvisioner{
		Registry: registry,
		Specs:    map[model.PoolName]boxyconfig.PoolSpec{"pool-a": {Type: "hyperv"}},
	}
	res := model.Resource{ID: "res-1", Provider: model.ProviderRef{AgentID: agent.Info().ID}}

	if err := ap.AttachToSegment(context.Background(), model.Pool{Name: "pool-a"}, res, "boxy-sb-sb-1"); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if agent.gotResourceID != "res-1" || agent.gotAttachRef != "boxy-sb-sb-1" {
		t.Fatalf("agent got (%q, %q)", agent.gotResourceID, agent.gotAttachRef)
	}
}

func TestAgentProvisioner_IsANetworkIsolatingAllocator(t *testing.T) {
	var a sandbox.SandboxAllocator = &AgentProvisioner{}
	if _, ok := a.(sandbox.NetworkIsolatingAllocator); !ok {
		t.Fatal("*AgentProvisioner must satisfy sandbox.NetworkIsolatingAllocator")
	}
}
```

Adjust the `NewAgentRegistry()`/`registry.Register(...)` calls to match whatever this package's *existing* `AgentProvisioner` tests actually use to register a fake agent — inspect one of those existing tests first (e.g. via the same `grep -rn "type fake.*Agent struct"` search above) and copy its exact registration call shape rather than guessing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pool/... -run 'TestAgentProvisioner_(CreateSegment|AttachToSegment|IsANetworkIsolatingAllocator)' -v`
Expected: compile failure — `CreateSegment`/`AttachToSegment` undefined on `*AgentProvisioner`.

- [ ] **Step 3: Implement the methods**

In `internal/pool/provisioner_agent.go`, after the existing `Allocate` method:

```go
// CreateSegment satisfies sandbox.NetworkIsolatingAllocator. It resolves
// the exact agent that owns res (never re-resolving by provider type --
// see AgentProvisioner's own doc comment on why Allocate/Destroy must route
// back to res.Provider.AgentID) and asks it to create (or, per
// providersdk.NetworkIsolator's contract, idempotently return an existing)
// segment for sandboxID.
func (ap *AgentProvisioner) CreateSegment(ctx context.Context, pool model.Pool, res model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return "", "", fmt.Errorf("unknown pool %q", pool.Name)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return "", "", err
	}
	isolator, ok := agent.(agentsdk.NetworkIsolatingAgent)
	if !ok {
		return "", "", fmt.Errorf("agent %q does not support network isolation", res.Provider.AgentID)
	}
	ref, err := isolator.CreateSegment(ctx, driverType, string(sandboxID))
	if err != nil {
		return "", "", err
	}
	return ref, driverType, nil
}

// AttachToSegment satisfies sandbox.NetworkIsolatingAllocator.
func (ap *AgentProvisioner) AttachToSegment(ctx context.Context, pool model.Pool, res model.Resource, ref providersdk.SegmentRef) error {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return fmt.Errorf("unknown pool %q", pool.Name)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return err
	}
	isolator, ok := agent.(agentsdk.NetworkIsolatingAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support network isolation", res.Provider.AgentID)
	}
	return isolator.AttachToSegment(ctx, driverType, res.ID, ref)
}
```

Note `res.ID` here: check its declared type in `model.Resource` (`ResourceID`, a `string` alias) — `isolator.AttachToSegment` (an `agentsdk.NetworkIsolatingAgent` method from Plan 1b) takes a plain `string` for `providerResourceID`. If `res.ID` doesn't implicitly convert, wrap it as `string(res.ID)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pool/... -run 'TestAgentProvisioner_(CreateSegment|AttachToSegment|IsANetworkIsolatingAllocator)' -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full pool package test suite to check for regressions**

Run: `go test ./internal/pool/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/pool/provisioner_agent.go internal/pool/*_test.go
git commit -m "feat(pool): AgentProvisioner implements NetworkIsolatingAllocator

DriverProvisioner deliberately does not get this capability -- it's
already marked deprecated in its own doc comment.

Part of #224."
```

---

### Task 4: `sandbox.Manager` allocation wiring

**Files:**
- Modify: `internal/sandbox/manager.go`
- Test: `internal/sandbox/manager_test.go`

**Interfaces:**
- Consumes: `NetworkIsolatingAllocator` (Task 2), `model.Sandbox.NetworkSegments`/`model.NetworkSegment` (Task 1).
- Produces: a new private helper `(m *Manager) ensureNetworkSegment(ctx, sb *model.Sandbox, pool model.Pool, res model.Resource) error`, called from both `AddFromPoolWithPackages` (`manager.go:157`) and `CreateFromPool` (`manager.go:267`).

- [ ] **Step 1: Write the failing tests**

```go
// append to internal/sandbox/manager_test.go

// fakeIsolatingAllocator adds NetworkIsolatingAllocator on top of a plain
// SandboxAllocator, mirroring the "capability on top of a base" shape used
// throughout this codebase's other optional-capability tests.
type fakeIsolatingAllocator struct {
	createRef    providersdk.SegmentRef
	createType   providersdk.Type
	createErr    error
	attachErr    error
	createCalls  int
	attachCalls  int
	gotSandboxID model.SandboxID
}

func (f *fakeIsolatingAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{}, nil
}
func (f *fakeIsolatingAllocator) CreateSegment(_ context.Context, _ model.Pool, _ model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error) {
	f.createCalls++
	f.gotSandboxID = sandboxID
	return f.createRef, f.createType, f.createErr
}
func (f *fakeIsolatingAllocator) AttachToSegment(context.Context, model.Pool, model.Resource, providersdk.SegmentRef) error {
	f.attachCalls++
	return f.attachErr
}

func TestManager_CreateFromPool_CreatesAndRecordsSegmentOnce(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	for _, res := range []model.Resource{
		{ID: "res-1", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
		{ID: "res-2", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
	} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("PutResource: %v", err)
		}
	}
	pool, _ := st.GetPool(ctx, "pool-a")
	pool.Inventory.Resources = []model.Resource{
		{ID: "res-1", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady},
		{ID: "res-2", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("PutPool: %v", err)
	}

	allocator := &fakeIsolatingAllocator{createRef: "boxy-sb-sb-1", createType: "hyperv"}
	m := New(st, allocator)

	sb, err := m.CreateFromPool(ctx, "test-sandbox", "pool-a", 2, model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if allocator.createCalls != 1 {
		t.Fatalf("CreateSegment called %d times, want exactly 1 (same sandbox, same agent, must not create twice)", allocator.createCalls)
	}
	if allocator.attachCalls != 2 {
		t.Fatalf("AttachToSegment called %d times, want 2 (once per resource)", allocator.attachCalls)
	}
	if len(sb.NetworkSegments) != 1 || sb.NetworkSegments[0].Ref != "boxy-sb-sb-1" || sb.NetworkSegments[0].ProviderType != "hyperv" || sb.NetworkSegments[0].AgentID != "agent-1" {
		t.Fatalf("NetworkSegments = %+v, want exactly one entry for agent-1", sb.NetworkSegments)
	}

	// Persisted, not just returned.
	persisted, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if len(persisted.NetworkSegments) != 1 {
		t.Fatalf("persisted sandbox NetworkSegments = %+v, want one entry", persisted.NetworkSegments)
	}
}

func TestManager_AddFromPool_ReusesExistingSegmentForSameAgent(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:              "sb-1",
		Status:          model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"}},
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-3", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-3", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	allocator := &fakeIsolatingAllocator{createRef: "should-not-be-used", createType: "hyperv"}
	m := New(st, allocator)

	sb, err := m.AddFromPool(ctx, "sb-1", "pool-a", 1)
	if err != nil {
		t.Fatalf("AddFromPool: %v", err)
	}
	if allocator.createCalls != 0 {
		t.Fatalf("CreateSegment called %d times, want 0 (must reuse the sandbox's existing segment for agent-1)", allocator.createCalls)
	}
	if allocator.attachCalls != 1 {
		t.Fatalf("AttachToSegment called %d times, want 1", allocator.attachCalls)
	}
	if len(sb.NetworkSegments) != 1 {
		t.Fatalf("NetworkSegments = %+v, want the original single entry unchanged", sb.NetworkSegments)
	}
}

func TestManager_CreateFromPool_PlainAllocatorSkipsSegmentsEntirely(t *testing.T) {
	// A SandboxAllocator that does NOT implement NetworkIsolatingAllocator
	// (e.g. devfactory-backed) must allocate exactly as it does today --
	// no error, no segment, matching this plan's Global Constraints.
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-4", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-4", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	m := New(st, plainAllocatorStub{}) // whatever this test file's existing plain SandboxAllocator-only fake is called -- reuse it, don't redefine

	sb, err := m.CreateFromPool(ctx, "plain-sandbox", "pool-a", 1, model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if len(sb.NetworkSegments) != 0 {
		t.Fatalf("NetworkSegments = %+v, want none for a plain allocator", sb.NetworkSegments)
	}
}
```

Replace `plainAllocatorStub{}` with whatever name this test file's *existing* minimal `SandboxAllocator` fake already uses (search: `grep -n "type.*Allocate(ctx context.Context, pool model.Pool, res model.Resource)" internal/sandbox/manager_test.go`) — reuse it rather than defining a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run 'TestManager_(CreateFromPool_CreatesAndRecordsSegmentOnce|AddFromPool_ReusesExistingSegmentForSameAgent|CreateFromPool_PlainAllocatorSkipsSegmentsEntirely)' -v`
Expected: the first two FAIL (`createCalls`/`attachCalls`/`NetworkSegments` all zero — nothing wired yet); the third already PASSes trivially (nothing to skip yet). That's expected — it becomes a real regression guard once Step 3 lands.

- [ ] **Step 3: Implement the helper and wire both call sites**

In `internal/sandbox/manager.go`, add this method (near the other private helpers, e.g. below `rememberGuestCredential`):

```go
// ensureNetworkSegment attaches res to sb's segment on res's agent,
// creating that segment first if this is the first resource from that
// agent this sandbox has seen. Mutates sb.NetworkSegments in place --
// callers persist sb themselves afterward, same as every other mutation
// already made to sb in these allocation loops. A no-op (returns nil
// immediately) when m.allocator doesn't implement NetworkIsolatingAllocator
// -- see this plan's Global Constraints.
func (m *Manager) ensureNetworkSegment(ctx context.Context, sb *model.Sandbox, pool model.Pool, res model.Resource) error {
	isolator, ok := m.allocator.(NetworkIsolatingAllocator)
	if !ok {
		return nil
	}
	agentID := res.Provider.AgentID
	for _, seg := range sb.NetworkSegments {
		if seg.AgentID == agentID {
			return isolator.AttachToSegment(ctx, pool, res, providersdk.SegmentRef(seg.Ref))
		}
	}
	ref, providerType, err := isolator.CreateSegment(ctx, pool, res, sb.ID)
	if err != nil {
		return fmt.Errorf("create network segment for sandbox %q: %w", sb.ID, err)
	}
	if err := isolator.AttachToSegment(ctx, pool, res, ref); err != nil {
		return fmt.Errorf("attach resource %q to network segment: %w", res.ID, err)
	}
	sb.NetworkSegments = append(sb.NetworkSegments, model.NetworkSegment{
		AgentID:      agentID,
		ProviderType: string(providerType),
		Ref:          string(ref),
	})
	return nil
}
```

In `AddFromPoolWithPackages` (`manager.go:157`), inside the `for _, res := range selected` loop, immediately after the existing `if m.allocator != nil { ... }` block that calls `Allocate`/`AllocateWithPackages` and before `res.State = model.ResourceStateAllocated`:

```go
		if m.allocator != nil {
			if err := m.ensureNetworkSegment(ctx, &sb, pool, res); err != nil {
				return model.Sandbox{}, fmt.Errorf("ensure network segment for resource %q: %w", res.ID, err)
			}
		}
```

In `CreateFromPool` (`manager.go:267`), inside its `for _, res := range selected` loop, in the same relative position (after the existing `Allocate` call, before `res.State = model.ResourceStateAllocated`):

```go
		if m.allocator != nil {
			if err := m.ensureNetworkSegment(ctx, &sb, pool, res); err != nil {
				return model.Sandbox{}, fmt.Errorf("ensure network segment for resource %q: %w", res.ID, err)
			}
		}
```

(Both call sites already have `sb` as a local `model.Sandbox` value in scope, and both already `PutSandbox`/`CreateSandbox`+`PutSandbox` it after the loop — `sb.NetworkSegments` mutated via `&sb` inside the loop is captured by that existing final persist call with no further changes needed there.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run 'TestManager_(CreateFromPool_CreatesAndRecordsSegmentOnce|AddFromPool_ReusesExistingSegmentForSameAgent|CreateFromPool_PlainAllocatorSkipsSegmentsEntirely)' -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full sandbox package test suite to check for regressions**

Run: `go test ./internal/sandbox/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/manager.go internal/sandbox/manager_test.go
git commit -m "feat(sandbox): wire NetworkIsolatingAllocator into resource allocation

Every resource claimed into a sandbox is now attached to that sandbox's
per-agent network segment, created on first use and reused for every
later resource from the same agent. A no-op for any allocator that
doesn't support it (e.g. devfactory-backed sandboxes).

Part of #224."
```

---

### Task 5: `SegmentDestroyer` capability + `pool.Manager` delegation

**Files:**
- Create: `internal/pool/segment_destroyer.go`
- Modify: `internal/pool/provisioner_agent.go` (add `DestroySegment`)
- Modify: `internal/pool/manager.go` (add `DestroySegment`, delegating to the provisioner)
- Test: `internal/pool/segment_destroyer_test.go`

**Interfaces:**
- Consumes: `agentsdk.NetworkIsolatingAgent` (Plan 1b), `ap.Registry.Get(agentID)` (existing, `AgentRegistry`).
- Produces: `SegmentDestroyingProvisioner` interface (`DestroySegment(ctx, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error`), implemented by `AgentProvisioner`; `(*pool.Manager).DestroySegment(ctx, agentID, providerType, ref) error`, delegating to it.

- [ ] **Step 1: Write the failing tests**

```go
// internal/pool/segment_destroyer_test.go
package pool

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestAgentProvisioner_DestroySegment(t *testing.T) {
	agent := &fakeIsolatingAgent{} // from Task 3's test file, same package
	registry := NewAgentRegistry()
	registry.Register(agent, []providersdk.Type{"hyperv"})
	ap := &AgentProvisioner{Registry: registry}

	if err := ap.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
}

func TestAgentProvisioner_DestroySegmentUnknownAgentErrors(t *testing.T) {
	ap := &AgentProvisioner{Registry: NewAgentRegistry()}
	if err := ap.DestroySegment(context.Background(), "missing-agent", "hyperv", "boxy-sb-sb-1"); err == nil {
		t.Fatal("expected an error for an unresolvable agent")
	}
}

func TestManager_DestroySegment_DelegatesToProvisionerCapability(t *testing.T) {
	agent := &fakeIsolatingAgent{}
	registry := NewAgentRegistry()
	registry.Register(agent, []providersdk.Type{"hyperv"})
	ap := &AgentProvisioner{Registry: registry}
	m := New(nil, ap) // pool.Manager's constructor -- match this package's existing New(...) signature/usage

	if err := m.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
}
```

(`New(nil, ap)` above is a placeholder call shape — check this package's actual `Manager` constructor signature first with `grep -n "^func New(" internal/pool/manager.go` and match it exactly; `Manager` may need a store even for this test if `New` requires one non-nil, in which case pass `store.NewMemoryStore()` instead of `nil`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pool/... -run 'TestAgentProvisioner_DestroySegment|TestManager_DestroySegment' -v`
Expected: compile failure — `DestroySegment` undefined on both types.

- [ ] **Step 3: Define the capability and implement it**

```go
// internal/pool/segment_destroyer.go
package pool

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// SegmentDestroyingProvisioner is an optional Provisioner capability for
// tearing down a network segment by agent ID directly -- unlike
// sandbox.NetworkIsolatingAllocator's CreateSegment/AttachToSegment (which
// need a model.Pool/model.Resource to resolve the owning agent), sandbox
// deletion only has model.Sandbox.NetworkSegments (agent ID + provider type
// + ref) to work from, with no pool/resource in scope any more.
type SegmentDestroyingProvisioner interface {
	DestroySegment(ctx context.Context, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error
}

// DestroySegment satisfies SegmentDestroyingProvisioner.
func (ap *AgentProvisioner) DestroySegment(ctx context.Context, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error {
	agent, ok := ap.Registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q unavailable to destroy segment %q", agentID, ref)
	}
	isolator, ok := agent.(agentsdk.NetworkIsolatingAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support network isolation", agentID)
	}
	return isolator.DestroySegment(ctx, providerType, ref)
}
```

In `internal/pool/manager.go`, add (near `DestroyResource`):

```go
// DestroySegment tears down one network segment by agent ID, delegating to
// the configured provisioner if it supports SegmentDestroyingProvisioner.
// Called by sandbox.DeletionReconciler (via the sandbox.SegmentDestroyer
// capability -- see Task 6) once a sandbox's resources are all gone.
//
// Signature deliberately uses plain strings, not providersdk.Type/
// providersdk.SegmentRef: internal/sandbox.SegmentDestroyer (Task 6) is
// declared with plain strings, matching model.NetworkSegment's own
// plain-string fields (that package doesn't import providersdk -- see
// Task 1's doc comment on model.NetworkSegment). Go interface satisfaction
// requires an exact method signature match, so a providersdk.Type/
// SegmentRef parameter here would silently fail to satisfy
// sandbox.SegmentDestroyer despite both underlying types being strings --
// the conversion has to happen on this side of the boundary.
func (m *Manager) DestroySegment(ctx context.Context, agentID string, providerType string, ref string) error {
	destroyer, ok := m.provisioner.(SegmentDestroyingProvisioner)
	if !ok {
		return nil
	}
	return destroyer.DestroySegment(ctx, agentID, providersdk.Type(providerType), providersdk.SegmentRef(ref))
}
```

(Returning `nil` rather than an error when the provisioner doesn't support it mirrors this plan's Global Constraint: a sandbox with no segments to begin with — because its allocator never supported isolation — must not fail deletion.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pool/... -run 'TestAgentProvisioner_DestroySegment|TestManager_DestroySegment' -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full pool package test suite to check for regressions**

Run: `go test ./internal/pool/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/pool/segment_destroyer.go internal/pool/segment_destroyer_test.go internal/pool/manager.go
git commit -m "feat(pool): add SegmentDestroyingProvisioner + Manager.DestroySegment

Part of #224."
```

---

### Task 6: `internal/sandbox/deleter.go` wiring

**Files:**
- Modify: `internal/sandbox/deleter.go`
- Test: `internal/sandbox/deleter_test.go`

**Interfaces:**
- Consumes: `model.Sandbox.NetworkSegments` (Task 1), `(*pool.Manager).DestroySegment` (Task 5, via a new `SegmentDestroyer` interface this task defines).
- Produces: `cleanupSandbox` destroys every recorded segment before deleting the sandbox record.

- [ ] **Step 1: Write the failing tests**

```go
// append to internal/sandbox/deleter_test.go

// fakeSegmentDestroyer adds SegmentDestroyer on top of the existing
// fakeDestroyer (ResourceDestroyer), mirroring this codebase's established
// "capability on top of a base fake" shape.
type fakeSegmentDestroyer struct {
	*fakeDestroyer
	destroyedSegments []model.NetworkSegment
	destroySegmentErr error
}

func (f *fakeSegmentDestroyer) DestroySegment(_ context.Context, agentID string, providerType string, ref string) error {
	f.destroyedSegments = append(f.destroyedSegments, model.NetworkSegment{AgentID: agentID, ProviderType: providerType, Ref: ref})
	return f.destroySegmentErr
}

func TestDeletionReconciler_DestroysNetworkSegmentsBeforeDeletingSandbox(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{
		ID:     "sb-1",
		Status: model.SandboxStatusDeleting,
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"},
		},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	destroyer := &fakeSegmentDestroyer{fakeDestroyer: &fakeDestroyer{}}
	r := NewDeletionReconciler(st, destroyer)

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(destroyer.destroyedSegments) != 1 || destroyer.destroyedSegments[0].Ref != "boxy-sb-sb-1" {
		t.Fatalf("destroyed segments = %+v, want one entry for boxy-sb-sb-1", destroyer.destroyedSegments)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err == nil {
		t.Fatal("expected sandbox to be deleted after its segments were torn down")
	}
}

func TestDeletionReconciler_PlainDestroyerSkipsSegmentsEntirely(t *testing.T) {
	// A ResourceDestroyer that does NOT implement SegmentDestroyer (matches
	// this plan's Global Constraints -- a devfactory-only deployment, or
	// any existing caller/test with no segment concept at all).
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{ID: "sb-1", Status: model.SandboxStatusDeleting}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	r := NewDeletionReconciler(st, &fakeDestroyer{})
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err == nil {
		t.Fatal("expected sandbox to be deleted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run TestDeletionReconciler_.*Segment -v`
Expected: `TestDeletionReconciler_DestroysNetworkSegmentsBeforeDeletingSandbox` FAILs (`destroyedSegments` is empty — nothing calls it yet); `TestDeletionReconciler_PlainDestroyerSkipsSegmentsEntirely` already PASSes trivially (becomes a real regression guard once Step 3 lands).

- [ ] **Step 3: Define `SegmentDestroyer` and wire `cleanupSandbox`**

In `internal/sandbox/deleter.go`, after the existing `ResourceDestroyer` interface:

```go
// SegmentDestroyer is an optional capability for a ResourceDestroyer that
// also knows how to tear down network segments (see model.NetworkSegment).
// Not every destroyer supports this -- a deployment with no
// network-isolation-capable providers at all uses a plain
// ResourceDestroyer with no segments to ever destroy.
type SegmentDestroyer interface {
	DestroySegment(ctx context.Context, agentID string, providerType string, ref string) error
}
```

In `cleanupSandbox`, right before `if err := r.store.DeleteSandbox(ctx, sb.ID); ...`:

```go
	if segmentDestroyer, ok := r.destroyer.(SegmentDestroyer); ok {
		for _, seg := range sb.NetworkSegments {
			if err := segmentDestroyer.DestroySegment(ctx, seg.AgentID, seg.ProviderType, seg.Ref); err != nil {
				return fmt.Errorf("destroy network segment %q for sandbox %q: %w", seg.Ref, sb.ID, err)
			}
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestDeletionReconciler_.*Segment -v`
Expected: PASS (both).

- [ ] **Step 5: Run the full sandbox package test suite, then the full repository test suite and lint**

Run: `go test ./internal/sandbox/...`
Expected: PASS, no regressions.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (rerun the known pre-existing Windows `t.TempDir()` `internal/cli` agent-serve flake in isolation if it appears — see AGENTS.md's "Lessons Learned").

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/deleter.go internal/sandbox/deleter_test.go
git commit -m "feat(sandbox): destroy network segments on sandbox deletion

Closes the allocation/deletion wiring for #224's driver-native isolation
(spec Decision 1) end to end -- every sandbox that lands on a
NetworkIsolator-capable provider now gets isolated automatically, from
creation through deletion.

Closes #224 (Decision 1 only -- mesh/JIT access/AccessBroker, Decisions
2-4, remain separate follow-up plans)."
```

---

## After This Plan

Decision 1 (driver-native, automatic per-sandbox isolation) is fully wired end to end: Hyper-V and Docker, in-process or remote agents, allocation through deletion. Two things are explicitly **not** covered and remain open:

- **Segment orphan-sweep.** A crash between `CreateSegment` succeeding and `Sandbox.NetworkSegments` being persisted (Task 4's `ensureNetworkSegment`) leaves a real switch/NAT (Hyper-V) or network (Docker) with no sandbox record pointing at it. This mirrors the exact class of gap ADR-0006 already solved for resources (`internal/pool/manager.go`'s orphan sweep) — a segment-equivalent sweep is a reasonable, bounded follow-up but is not built here.
- **Decisions 2–4 of the spec** (cross-host WireGuard mesh, JIT native-protocol access, the `AccessBroker` extension point) are unrelated subsystems, each its own future plan.
