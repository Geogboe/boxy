# Overlay Network Fabric — AccessBroker Extension Point (Plan 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reserve the seam an external access broker (Teleport, Boundary, or similar) would plug into later, replacing Plan 3's own JIT relay for operators who already run one — interface only, per the spec's Decision 4, which explicitly scopes this to "defined now, no implementation."

**Architecture:** One new interface in `internal/sandbox` (not `pkg/`, since it's wired into sandbox lifecycle orchestration, not a general-purpose primitive an external project would consume on its own — unlike `providersdk`/`agentsdk`). `sandbox.Manager` calls it, if configured, at the same points it already touches sandbox creation and deletion. Nothing implements it yet.

**Tech Stack:** Go 1.25 — no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan implements Decision 4 in full (which is deliberately just the interface + wiring, no concrete broker).

## Global Constraints

- No concrete Teleport/Boundary implementation in this plan — the spec is explicit that this is out of scope here. Resist the urge to sketch one "for completeness."
- Unconfigured (the default, and the only state every existing test exercises) must be indistinguishable from today's behavior — `AccessBroker` being nil is not a new error path anywhere it's checked.

---

### Task 1: `AccessBroker` interface + `sandbox.Manager` wiring

**Files:**
- Create: `internal/sandbox/access_broker.go`
- Modify: `internal/sandbox/manager.go` (add an optional `accessBroker AccessBroker` field + a setter, and call sites at sandbox creation/deletion)
- Test: `internal/sandbox/access_broker_test.go`

**Interfaces:**
- Produces: `AccessBroker` interface — `RegisterSandbox(ctx, sandbox model.Sandbox) error`, `DeregisterSandbox(ctx, sandboxID model.SandboxID) error`; `Manager.SetAccessBroker(AccessBroker)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/access_broker_test.go
package sandbox

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeAccessBroker struct {
	registered   []model.Sandbox
	deregistered []model.SandboxID
	registerErr  error
}

func (f *fakeAccessBroker) RegisterSandbox(_ context.Context, sb model.Sandbox) error {
	f.registered = append(f.registered, sb)
	return f.registerErr
}
func (f *fakeAccessBroker) DeregisterSandbox(_ context.Context, id model.SandboxID) error {
	f.deregistered = append(f.deregistered, id)
	return nil
}

func TestManager_CreateFromPool_RegistersWithAccessBrokerWhenConfigured(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-1", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-1", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	m := New(st, nil)
	broker := &fakeAccessBroker{}
	m.SetAccessBroker(broker)

	sb, err := m.CreateFromPool(ctx, "test-sandbox", "pool-a", 1, model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if len(broker.registered) != 1 || broker.registered[0].ID != sb.ID {
		t.Fatalf("registered = %+v, want exactly sb.ID once", broker.registered)
	}
}

func TestManager_CreateFromPool_NoAccessBrokerConfiguredBehavesAsToday(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-1", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-1", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	m := New(st, nil) // no SetAccessBroker call at all

	if _, err := m.CreateFromPool(ctx, "test-sandbox", "pool-a", 1, model.SandboxPolicies{}); err != nil {
		t.Fatalf("CreateFromPool: %v (must succeed identically to today with no broker configured)", err)
	}
}

func TestJITSessionReconciler_UnrelatedToAccessBroker(t *testing.T) {
	// Sanity check that AccessBroker and JITSession are independent seams
	// -- an AccessBroker deployment replaces Plan 3's relay, it does not
	// need to know about JITSession records at all. This test documents
	// that boundary rather than exercising new behavior.
	var _ AccessBroker = (*fakeAccessBroker)(nil)
}

func TestManager_RequestDelete_DeregistersFromAccessBrokerWhenConfigured(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	m := New(st, nil)
	broker := &fakeAccessBroker{}
	m.SetAccessBroker(broker)

	if _, err := m.RequestDelete(ctx, "sb-1"); err != nil {
		t.Fatalf("RequestDelete: %v", err)
	}
	if len(broker.deregistered) != 1 || broker.deregistered[0] != "sb-1" {
		t.Fatalf("deregistered = %+v, want exactly sb-1 once", broker.deregistered)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run 'TestManager_(CreateFromPool_.*AccessBroker|RequestDelete_Deregisters)|TestJITSessionReconciler_UnrelatedToAccessBroker' -v`
Expected: compile failure — `AccessBroker`, `SetAccessBroker` undefined.

- [ ] **Step 3: Implement**

```go
// internal/sandbox/access_broker.go
package sandbox

import (
	"context"

	"github.com/Geogboe/boxy/pkg/model"
)

// AccessBroker is an optional extension point (spec Decision 4): if
// configured, it's asked to register/deregister a sandbox's resources as
// human-access targets in an external system (Teleport, Boundary, or
// similar) instead of relying on this project's own JIT relay (Plan 3).
// Unconfigured (nil) is the default and is not a degraded state -- it's
// simply "use this project's own JITSession relay," today's only option
// and the one every existing test exercises.
//
// No concrete implementation exists yet -- this interface exists so a
// future integration has a defined seam to build against instead of
// retrofitting one later. See the design spec's Decision 4.
type AccessBroker interface {
	RegisterSandbox(ctx context.Context, sandbox model.Sandbox) error
	DeregisterSandbox(ctx context.Context, sandboxID model.SandboxID) error
}
```

In `internal/sandbox/manager.go`, add a field to `Manager`:

```go
	// accessBroker, if set, is notified as sandboxes are created/deleted
	// (see AccessBroker's doc comment). Nil is the default and does not
	// change any existing behavior.
	accessBroker AccessBroker
```

Add a setter (mirroring `SetClock`'s existing shape):

```go
// SetAccessBroker configures an optional AccessBroker. Used by boxy serve's
// wiring when one is configured; tests that don't care leave this unset.
func (m *Manager) SetAccessBroker(broker AccessBroker) {
	m.accessBroker = broker
}
```

In `CreateFromPool`, right before its final `return sb, nil`:

```go
	if m.accessBroker != nil {
		if err := m.accessBroker.RegisterSandbox(ctx, sb); err != nil {
			return model.Sandbox{}, fmt.Errorf("register sandbox %q with access broker: %w", sb.ID, err)
		}
	}
```

In `AddFromPoolWithPackages`, at the same relative point (its own final `return sb, nil`) — a sandbox growing via `AddFromPool` should re-register (the broker's own `RegisterSandbox` is expected to be idempotent/upsert-shaped, the same assumption this codebase already makes about `NetworkIsolator.CreateSegment`'s idempotency):

```go
	if m.accessBroker != nil {
		if err := m.accessBroker.RegisterSandbox(ctx, sb); err != nil {
			return model.Sandbox{}, fmt.Errorf("re-register sandbox %q with access broker: %w", sb.ID, err)
		}
	}
```

In `RequestDelete`, after the sandbox is successfully marked for deletion (find the exact point it currently returns its result, `grep -n "func (m \*Manager) RequestDelete" -A 30 internal/sandbox/manager.go` to see its exact final shape before adding this):

```go
	if m.accessBroker != nil {
		if err := m.accessBroker.DeregisterSandbox(ctx, sbID); err != nil {
			return model.Sandbox{}, fmt.Errorf("deregister sandbox %q from access broker: %w", sbID, err)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run 'TestManager_(CreateFromPool_.*AccessBroker|RequestDelete_Deregisters)|TestJITSessionReconciler_UnrelatedToAccessBroker' -v`
Expected: PASS (all four).

- [ ] **Step 5: Run the full sandbox package test suite, then the full repository build/test/lint**

Run: `go test ./internal/sandbox/...`
Expected: PASS, no regressions (every existing test leaves `accessBroker` nil, so this must not change any of their behavior).

Run: `go build ./... && go test ./...`
Expected: PASS everywhere.

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/access_broker.go internal/sandbox/access_broker_test.go internal/sandbox/manager.go
git commit -m "feat(sandbox): add AccessBroker extension point (interface only)

Closes #224 (Decision 4 -- the extension point Decision 3's JIT relay
sits behind for operators who already run Teleport/Boundary or similar).
No concrete broker implementation -- deliberately out of scope per the
design spec.

Closes #224 (all four decisions now have implementation plans)."
```

---

## After This Plan

All four decisions in `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` have complete, self-reviewed implementation plans:

- Plan 1a/1b/1c — driver-native, automatic per-sandbox isolation (Decision 1)
- Plan 2/2b — cross-host WireGuard mesh (Decision 2)
- Plan 3 — JIT native-protocol access (Decision 3)
- Plan 4 (this one) — the `AccessBroker` seam (Decision 4)

Nothing beyond writing these plans has been executed — no code from any of them has been implemented yet. Concrete Teleport/Boundary integration(s), a segment/mesh orphan-sweep, and closing mesh interfaces from `DestroySegment` all remain explicitly flagged, real follow-ups noted in their respective plans' "After This Plan" sections — not silently missing.
