# Hyper-V Create-Failure Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Hyper-V create failures leave no untracked VM behind, and let typed driver errors (capacity, orphaned-resource) survive the RemoteAgent/gRPC boundary — closing #174, #185, and (partially, by design) #183.

**Architecture:** Three layers, built bottom-up: (1) a provider-neutral typed-error wire contract (`providersdk.ErrorTyper` + two proto fields on `AgentError`) that lets `CapacityError`/`OrphanedResourceError` survive serialization; (2) a Create-failure path that resolves a failed VM's real GUID via a bounded cleanup-retry loop and surfaces it through that wire contract so the pool layer can quarantine (`ResourceStateError`) and auto-retry-destroy it, plus a periodic `ResourceLister`-based sweep as a defense-in-depth safety net for the cases the inline path can't see; (3) two narrow, explicitly-not-total mitigations to the in-process memory-reservation window from #183.

**Tech Stack:** Go, protobuf/buf (existing `boxyagent.v1` proto), PowerShell (Hyper-V driver scripts), the existing `policycontroller`-based reconcile pattern.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-13-hyperv-create-failure-hardening-design.md` — every task below implements one numbered fix/mitigation from it; re-read the relevant section before starting a task if anything here is ambiguous.
- Sequencing: #185 (Tasks 1-2) → #174 (Tasks 3-7) → #183 (Tasks 8-9) → docs (Task 10). Do not reorder — later tasks depend on earlier ones' exact names/signatures.
- Use `task` (Taskfile) for all build/test/lint/proto commands, never raw `go`/`buf` — see `Taskfile.yml`. In particular, proto changes are generated with `task proto:generate`, never `buf generate` directly.
- `Driver.Create`'s signature (`func(ctx context.Context, cfg any) (*providersdk.Resource, error)`) does not change anywhere in this plan — only what the returned `error` wraps changes. `docker`/`devfactory` drivers are untouched.
- No task introduces a new PowerShell round-trip beyond what correctness already requires — see Task 3's existence-check reuse and Task 8's script split, both justified in the spec's "Alternatives considered."
- Every new/changed constant, type, and function name below is exact — later tasks reference earlier ones' names verbatim, matching the spec.

---

### Task 1: Promote `CapacityError`, add `OrphanedResourceError` and `ErrorTyper` to `providersdk`

**Files:**
- Create: `pkg/providersdk/errors.go`
- Create: `pkg/providersdk/errors_test.go`
- Modify: `pkg/providersdk/driver.go` (add `ErrorTyper` interface next to the existing `ResourceLister`/`StreamingDriver` optional-capability interfaces, around line 94-108)
- Modify: `pkg/providersdk/providers/hyperv/driver.go:461-474` (delete the local `CapacityError` type, replace with an alias)
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go` (no behavior change expected — existing `var capErr *CapacityError; errors.As(err, &capErr)` assertions at lines 168, 308, 391 must keep passing unmodified, since `hyperv.CapacityError` stays a valid local name via the alias)

**Interfaces:**
- Produces: `providersdk.CapacityError{RequestedMemoryMB, AvailableMemoryMB int64}`, `providersdk.OrphanedResourceError{ID, CauseMessage string}`, `providersdk.ErrorTyper` (interface: `ErrorType() string`), `hyperv.CapacityError` (type alias to `providersdk.CapacityError`, unchanged from the caller's perspective).

- [x] **Step 1: Write the failing test for the new types**

```go
// pkg/providersdk/errors_test.go
package providersdk

import (
	"errors"
	"testing"
)

func TestCapacityError_ImplementsErrorTyper(t *testing.T) {
	var err error = &CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512}
	var et ErrorTyper
	if !errors.As(err, &et) {
		t.Fatal("expected *CapacityError to satisfy ErrorTyper")
	}
	if et.ErrorType() != "capacity" {
		t.Errorf("ErrorType() = %q, want %q", et.ErrorType(), "capacity")
	}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}

func TestOrphanedResourceError_ImplementsErrorTyper(t *testing.T) {
	var err error = &OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}
	var et ErrorTyper
	if !errors.As(err, &et) {
		t.Fatal("expected *OrphanedResourceError to satisfy ErrorTyper")
	}
	if et.ErrorType() != "orphaned_resource" {
		t.Errorf("ErrorType() = %q, want %q", et.ErrorType(), "orphaned_resource")
	}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `task test`
Expected: FAIL — `pkg/providersdk` doesn't compile (`CapacityError`, `OrphanedResourceError`, `ErrorTyper` undefined).

- [x] **Step 3: Add `ErrorTyper` to `pkg/providersdk/driver.go`**

Insert immediately after the existing `ResourceLister` interface (currently ends at line 101):

```go
// ErrorTyper lets a driver error self-report a stable category (and, via
// json.Marshal on the error value itself, a JSON detail payload) for
// propagation across the RemoteAgent/gRPC boundary — without coupling the
// provider-neutral agentsdk layer to any specific driver package. Optional:
// errors that don't implement it just lose their type on that path, same as
// before this existed. See docs/superpowers/specs/2026-08-13-hyperv-create-failure-hardening-design.md.
type ErrorTyper interface {
	ErrorType() string // e.g. "capacity"
}
```

- [x] **Step 4: Write `pkg/providersdk/errors.go`**

```go
package providersdk

import "fmt"

// CapacityError indicates the host does not currently have enough available
// capacity (e.g. memory) to satisfy a Create request. Provider-neutral: any
// driver can return one, not just Hyper-V, which aliases this type — see
// pkg/providersdk/providers/hyperv/driver.go.
type CapacityError struct {
	RequestedMemoryMB int64
	AvailableMemoryMB int64
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf(
		"insufficient host capacity: requested %d MB, %d MB available",
		e.RequestedMemoryMB, e.AvailableMemoryMB,
	)
}

// ErrorType implements ErrorTyper.
func (e *CapacityError) ErrorType() string { return "capacity" }

// OrphanedResourceError indicates Create failed and best-effort cleanup of
// the partially-created resource also failed, leaving it on the underlying
// host outside Boxy's inventory. ID is the provider-native identifier — the
// same convention every successfully created Resource uses — so a caller
// can record a quarantined resource and retry destroying it later.
// CauseMessage is a plain string, not a wrapped error, so this type
// round-trips through json.Marshal/json.Unmarshal across the
// RemoteAgent/gRPC boundary (see #185) — an error interface's concrete type
// usually can't survive that.
type OrphanedResourceError struct {
	ID           string
	CauseMessage string
}

func (e *OrphanedResourceError) Error() string {
	return fmt.Sprintf("resource %q orphaned after create failure and cleanup failure: %s", e.ID, e.CauseMessage)
}

// ErrorType implements ErrorTyper.
func (e *OrphanedResourceError) ErrorType() string { return "orphaned_resource" }
```

- [x] **Step 5: Alias `hyperv.CapacityError`, delete the local type**

In `pkg/providersdk/providers/hyperv/driver.go`, delete lines 461-474 (the local `CapacityError` struct and its `Error()` method) and replace with:

```go
// CapacityError is providersdk.CapacityError under this package's existing
// name — see #185's design spec for why the type moved.
type CapacityError = providersdk.CapacityError
```

Place it where the old type was (just above `queryAvailableMemoryMB`, which references it in a doc comment).

- [x] **Step 6: Run tests to verify everything passes**

Run: `task test`
Expected: PASS — new tests pass, and every existing hyperv test using `*CapacityError`/`errors.As` (driver_test.go:168,308,391) still compiles and passes unmodified, since `hyperv.CapacityError` is still a valid local name.

- [x] **Step 7: Commit**

```bash
git add pkg/providersdk/errors.go pkg/providersdk/errors_test.go pkg/providersdk/driver.go pkg/providersdk/providers/hyperv/driver.go
git commit -m "feat(#185): promote CapacityError to providersdk, add OrphanedResourceError and ErrorTyper

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 2: Wire typed errors across the RemoteAgent/gRPC boundary

**Files:**
- Modify: `proto/boxyagent/v1/agent.proto:59-61` (`AgentError` message)
- Modify: `pkg/agentsdk/remoteclient.go:519-524` (`errorResult`) and every call site (lines 251, 258, 265, 379, 387, 392, 406, 417, 422, 431, 441, 445, 459, 466, 484, 500 — all currently `errorResult(commandID, msg)`)
- Modify: `pkg/agentsdk/remote.go` (add `reconstructAgentError`, use it at all 8 `agentErr.GetMessage()` sites: lines 243-244, 265-266, 287-288, 343-344, 386-387, 406-407, 432-433, 461-462)
- Modify: `pkg/agentsdk/remoteclient_test.go`, `pkg/agentsdk/remote_test.go`

**Interfaces:**
- Consumes: `providersdk.ErrorTyper`, `providersdk.CapacityError`, `providersdk.OrphanedResourceError` (Task 1).
- Produces: `errorResult(commandID, msg string, err error) *boxyagentv1.AgentError` (signature changes — every existing call site updates), `reconstructAgentError(agentID string, ae *boxyagentv1.AgentError) error`.

- [x] **Step 1: Extend the proto message**

In `proto/boxyagent/v1/agent.proto`, replace:

```protobuf
message AgentError {
  string message = 1;
}
```

with:

```protobuf
message AgentError {
  string message = 1;
  // error_type/error_detail_json let a typed driver error (see
  // providersdk.ErrorTyper) survive this boundary instead of flattening to
  // a plain string — mirrors CreateCommand.config_json's opaque-JSON
  // rationale above: a new typed error never needs another proto change.
  string error_type = 2;        // e.g. "capacity"; empty = no typed error
  bytes error_detail_json = 3;  // opaque JSON payload for error_type
}
```

- [x] **Step 2: Regenerate protobuf stubs**

Run: `task proto:generate`
Expected: `pkg/agentproto/boxyagent/v1/agent.pb.go` regenerates with `GetErrorType() string` and `GetErrorDetailJson() []byte` accessors on `AgentError`.

- [x] **Step 3: Write the failing round-trip test**

```go
// pkg/agentsdk/remoteclient_test.go — add
func TestErrorResult_ClassifiesTypedErrors(t *testing.T) {
	t.Run("capacity error", func(t *testing.T) {
		err := &providersdk.CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512}
		result := errorResult("cmd-1", err.Error(), err)
		ae := result.GetError()
		if ae.GetErrorType() != "capacity" {
			t.Errorf("error_type = %q, want %q", ae.GetErrorType(), "capacity")
		}
		var got providersdk.CapacityError
		if jerr := json.Unmarshal(ae.GetErrorDetailJson(), &got); jerr != nil {
			t.Fatalf("unmarshal detail: %v", jerr)
		}
		if got.RequestedMemoryMB != 2048 || got.AvailableMemoryMB != 512 {
			t.Errorf("detail = %+v, want original fields", got)
		}
	})

	t.Run("orphaned resource error", func(t *testing.T) {
		err := &providersdk.OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}
		result := errorResult("cmd-2", err.Error(), err)
		ae := result.GetError()
		if ae.GetErrorType() != "orphaned_resource" {
			t.Errorf("error_type = %q, want %q", ae.GetErrorType(), "orphaned_resource")
		}
		var got providersdk.OrphanedResourceError
		if jerr := json.Unmarshal(ae.GetErrorDetailJson(), &got); jerr != nil {
			t.Fatalf("unmarshal detail: %v", jerr)
		}
		if got.ID != "guid-1" || got.CauseMessage != "remove-vm failed" {
			t.Errorf("detail = %+v, want original fields", got)
		}
	})

	t.Run("untyped error carries no error_type", func(t *testing.T) {
		result := errorResult("cmd-3", "boom", errors.New("boom"))
		if result.GetError().GetErrorType() != "" {
			t.Errorf("error_type = %q, want empty for an untyped error", result.GetError().GetErrorType())
		}
	})
}
```

```go
// pkg/agentsdk/remote_test.go — add
func TestRemoteAgent_Create_ReconstructsCapacityError(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.Resource
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.Create(context.Background(), "hyperv", map[string]any{})
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)

	detail, err := json.Marshal(&providersdk.CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_Error{Error: &boxyagentv1.AgentError{
			Message:         "insufficient host capacity: requested 2048 MB, 512 MB available",
			ErrorType:       "capacity",
			ErrorDetailJson: detail,
		}},
	})

	select {
	case r := <-resultCh:
		var capErr *providersdk.CapacityError
		if !errors.As(r.err, &capErr) {
			t.Fatalf("expected *providersdk.CapacityError, got %#v", r.err)
		}
		if capErr.RequestedMemoryMB != 2048 || capErr.AvailableMemoryMB != 512 {
			t.Fatalf("capErr = %+v, want original fields intact", capErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Create to return")
	}
}

func TestRemoteAgent_Create_ReconstructsOrphanedResourceError(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.Resource
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.Create(context.Background(), "hyperv", map[string]any{})
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)

	detail, err := json.Marshal(&providersdk.OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_Error{Error: &boxyagentv1.AgentError{
			Message:         "resource \"guid-1\" orphaned after create failure and cleanup failure: remove-vm failed",
			ErrorType:       "orphaned_resource",
			ErrorDetailJson: detail,
		}},
	})

	select {
	case r := <-resultCh:
		var orphanErr *providersdk.OrphanedResourceError
		if !errors.As(r.err, &orphanErr) {
			t.Fatalf("expected *providersdk.OrphanedResourceError, got %#v", r.err)
		}
		if orphanErr.ID != "guid-1" || orphanErr.CauseMessage != "remove-vm failed" {
			t.Fatalf("orphanErr = %+v, want original fields intact", orphanErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Create to return")
	}
}
```

`remote_test.go` needs `"encoding/json"` added to its imports for these tests.

- [x] **Step 4: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — `errorResult` doesn't accept a third argument yet, `reconstructAgentError` is undefined.

- [x] **Step 5: Update `errorResult` and every call site in `remoteclient.go`**

```go
func errorResult(commandID, msg string, err error) *boxyagentv1.CommandResult {
	ae := &boxyagentv1.AgentError{Message: msg}
	var et providersdk.ErrorTyper
	if err != nil && errors.As(err, &et) {
		ae.ErrorType = et.ErrorType()
		if detail, jerr := json.Marshal(err); jerr == nil {
			ae.ErrorDetailJson = detail
		}
	}
	return &boxyagentv1.CommandResult{CommandId: commandID, Outcome: &boxyagentv1.CommandResult_Error{Error: ae}}
}
```

Add `"errors"` to the file's imports. Update every call site: sites that already have a live `err` in scope (lines 258, 379, 387, 392, 406, 417, 422, 431, 445, 459, 466, 484) change `errorResult(cmd.GetCommandId(), fmt.Sprintf(...))` / `errorResult(cmd.GetCommandId(), err.Error())` to pass that same `err` as the third argument, e.g. line 392 becomes `errorResult(cmd.GetCommandId(), err.Error(), err)`. Sites with no underlying driver error (251, 265, 500 — "provider not available", "unknown command op") pass `nil`.

Also change the two direct-`AgentError` construction sites (`executeStreamingCommand`, line 251, and `remoteclient.go`'s streaming completion path) the same way — they already call `errorResult`, so they get the fix automatically once the signature change above lands; only their call sites' argument lists change as described.

- [x] **Step 6: Add `reconstructAgentError` and use it in `remote.go`**

```go
// reconstructAgentError rebuilds a typed error from an AgentError's
// error_type/error_detail_json when recognized, falling back to today's
// plain message-only error otherwise. See providersdk.ErrorTyper.
func reconstructAgentError(agentID string, ae *boxyagentv1.AgentError) error {
	base := fmt.Errorf("agent %q: %s", agentID, ae.GetMessage())
	switch ae.GetErrorType() {
	case "capacity":
		var ce providersdk.CapacityError
		if json.Unmarshal(ae.GetErrorDetailJson(), &ce) == nil {
			return &ce
		}
	case "orphaned_resource":
		var oe providersdk.OrphanedResourceError
		if json.Unmarshal(ae.GetErrorDetailJson(), &oe) == nil {
			return &oe
		}
	}
	return base
}
```

Replace all 8 occurrences of:

```go
if agentErr := res.GetError(); agentErr != nil {
    return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
}
```

(and the `Delete`/streaming variants that return just `error`) with:

```go
if agentErr := res.GetError(); agentErr != nil {
    return nil, reconstructAgentError(a.info.ID, agentErr)
}
```

adjusting the return arity to match each method (`Delete` returns just `reconstructAgentError(...)`, no `nil,`).

- [x] **Step 7: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 8: Commit**

```bash
git add proto/boxyagent/v1/agent.proto pkg/agentproto/boxyagent/v1/agent.pb.go pkg/agentsdk/remoteclient.go pkg/agentsdk/remote.go pkg/agentsdk/remoteclient_test.go pkg/agentsdk/remote_test.go
git commit -m "feat(#185): propagate typed driver errors across the RemoteAgent/gRPC boundary

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 3: Bounded, reliable cleanup with a typed failure carrying the real GUID

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go` (`deleteBestEffort`, lines 836-848; `Create`'s two failure branches, lines 215-225)
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go` (`TestDriver_Create_CleanupOnFailure`, lines 99-125, needs its mock updated for the new cleanup script shape; add new tests)

**Interfaces:**
- Consumes: `providersdk.OrphanedResourceError` (Task 1).
- Produces: `(d *Driver) deleteBestEffort(ctx context.Context, vmName, vhdPath string) (guid string, err error)` (signature changes from `error` to `(string, error)`), `(d *Driver) createFailure(ctx context.Context, vmName, diffPath string, cause error) error`.

- [x] **Step 1: Write the failing tests**

```go
// pkg/providersdk/providers/hyperv/driver_test.go — add
func TestDriver_DeleteBestEffort_RetriesAndResolvesGUIDOnPersistentFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		// Every attempt's existence check reports the VM still present.
		return fakeGUID + "\n", nil
	})
	d.deleteWaitInterval = time.Millisecond // avoid real sleeps in the test

	guid, err := d.deleteBestEffort(context.Background(), "boxy-abc123", `C:\VMs\boxy-abc123.vhdx`)
	if err == nil {
		t.Fatal("expected error when the VM is still present after all attempts")
	}
	if guid != fakeGUID {
		t.Errorf("guid = %q, want %q", guid, fakeGUID)
	}
	if callCount != deleteBestEffortAttempts {
		t.Errorf("callCount = %d, want %d attempts", callCount, deleteBestEffortAttempts)
	}
}

func TestDriver_DeleteBestEffort_SucceedsWhenVMConfirmedGone(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		return "\n", nil // empty output = Get-VM found nothing
	})

	guid, err := d.deleteBestEffort(context.Background(), "boxy-abc123", `C:\VMs\boxy-abc123.vhdx`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if guid != "" {
		t.Errorf("guid = %q, want empty on confirmed cleanup", guid)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want exactly 1 (no retry needed once confirmed gone)", callCount)
	}
}

func TestDriver_Create_ReturnsOrphanedResourceErrorWhenCleanupFails(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			// deleteBestEffort's script: existence check reports still present.
			return fakeGUID + "\n", nil
		}
	})
	d.deleteWaitInterval = time.Millisecond

	_, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	var orphanErr *providersdk.OrphanedResourceError
	if !errors.As(err, &orphanErr) {
		t.Fatalf("expected *providersdk.OrphanedResourceError, got %#v", err)
	}
	if orphanErr.ID != fakeGUID {
		t.Errorf("ID = %q, want %q", orphanErr.ID, fakeGUID)
	}
}

func TestDriver_Create_PlainErrorWhenCleanupSucceeds(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			return "\n", nil // deleteBestEffort's existence check: confirmed gone
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	if err == nil {
		t.Fatal("expected an error")
	}
	var orphanErr *providersdk.OrphanedResourceError
	if errors.As(err, &orphanErr) {
		t.Fatalf("expected a plain error, not *OrphanedResourceError, when cleanup succeeded: %#v", orphanErr)
	}
}
```

Note: `deleteWaitInterval` is reused as the retry-interval override field name is new (`deleteBestEffortInterval` is a package const, not overridable per-instance like `deleteWaitTimeout`/`deleteWaitInterval` are) — Step 3 below adds a `d.deleteBestEffortInterval time.Duration` field mirroring the existing override pattern so tests don't sleep for real; update the tests above to set `d.deleteBestEffortInterval = time.Millisecond` instead of `d.deleteWaitInterval`.

- [x] **Step 2: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — `deleteBestEffort` still returns a single `error`, `deleteBestEffortAttempts`/`deleteBestEffortInterval`/`d.deleteBestEffortInterval` undefined, `createFailure` undefined.

- [x] **Step 3: Add the retry-interval override field and constants**

In the `Driver` struct (after `memoryQueryTimeout`, around line 49), add:

```go
	// deleteBestEffortInterval bounds the pause between deleteBestEffort's
	// cleanup-retry attempts. Zero uses the production default; tests
	// override it to avoid real sleeps. See #174.
	deleteBestEffortInterval time.Duration
```

In the `const` block (around line 64-81), add:

```go
	// deleteBestEffortAttempts/defaultDeleteBestEffortInterval bound
	// deleteBestEffort's cleanup retry: Remove-VM's -ErrorAction
	// SilentlyContinue masks whether it actually worked, so a single
	// attempt can silently leave a VM behind (see #174). Same order of
	// magnitude as defaultDeleteWaitInterval.
	deleteBestEffortAttempts       = 3
	defaultDeleteBestEffortInterval = 2 * time.Second
```

Add the accessor next to `waitInterval()`/`memQueryTimeout()`:

```go
func (d *Driver) bestEffortInterval() time.Duration {
	if d.deleteBestEffortInterval > 0 {
		return d.deleteBestEffortInterval
	}
	return defaultDeleteBestEffortInterval
}
```

- [x] **Step 4: Rewrite `deleteBestEffort`**

Replace the existing function (lines 836-848) with:

```go
// deleteBestEffort attempts to remove a partially-created VM after Create
// fails, retrying up to deleteBestEffortAttempts times since a transient
// VMMS hiccup can make a single Stop-VM/Remove-VM attempt fail. Each attempt
// is followed by a Get-VM existence check — -ErrorAction SilentlyContinue on
// Remove-VM masks whether it actually worked, so this check is the only way
// to know for certain, and its own output doubles as the VM's GUID if
// cleanup still didn't work (needed to build OrphanedResourceError) — no
// separate lookup call added. Returns ("", nil) once the VM is confirmed
// gone. Returns (guid, err) if it's still present after all attempts; guid
// is empty only if every attempt's PowerShell call itself failed (host
// unreachable), meaning nothing could even be confirmed, let alone
// quarantined.
func (d *Driver) deleteBestEffort(ctx context.Context, vmName, vhdPath string) (guid string, err error) {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
Stop-VM -Name '%s' -Force -TurnOff -ErrorAction SilentlyContinue
Remove-VM -Name '%s' -Force -ErrorAction SilentlyContinue
if ('%s' -ne '' -and (Test-Path '%s')) { Remove-Item '%s' -Force -ErrorAction SilentlyContinue }
$vm = Get-VM -Name '%s' -ErrorAction SilentlyContinue
if ($null -eq $vm) { '' } else { $vm.Id.ToString() }
`,
		psq(vmName), psq(vmName),
		psq(vhdPath), psq(vhdPath), psq(vhdPath),
		psq(vmName),
	)

	var lastErr error
	for attempt := 1; attempt <= deleteBestEffortAttempts; attempt++ {
		out, psErr := d.ps(ctx, script)
		if psErr != nil {
			lastErr = psErr
		} else if remaining := strings.TrimSpace(out); remaining == "" {
			return "", nil // confirmed gone
		} else {
			guid = remaining
			lastErr = fmt.Errorf("hyperv cleanup: VM %q still present after Remove-VM", vmName)
		}
		if attempt < deleteBestEffortAttempts {
			select {
			case <-ctx.Done():
				return guid, ctx.Err()
			case <-time.After(d.bestEffortInterval()):
			}
		}
	}
	return guid, lastErr
}
```

- [x] **Step 5: Add `createFailure` and wire it into `Create`'s two failure branches**

Add near `deleteBestEffort`:

```go
// createFailure builds Create's return error after a failed create attempt,
// running best-effort cleanup and escalating to *providersdk.OrphanedResourceError
// (carrying the real GUID, resolved by deleteBestEffort's own existence
// check) when cleanup couldn't confirm the VM is gone. cause is the
// original failure that triggered cleanup.
func (d *Driver) createFailure(ctx context.Context, vmName, diffPath string, cause error) error {
	guid, cleanupErr := d.deleteBestEffort(ctx, vmName, diffPath)
	if cleanupErr != nil && guid != "" {
		return &providersdk.OrphanedResourceError{
			ID:           guid,
			CauseMessage: fmt.Sprintf("%v (cleanup also failed: %v)", cause, cleanupErr),
		}
	}
	return cause
}
```

In `Create`, replace:

```go
	out, err := d.ps(ctx, createScript)
	if err != nil {
		_ = d.deleteBestEffort(ctx, vmName, diffPath)
		return nil, fmt.Errorf("hyperv create VM %q: %w", vmName, err)
	}

	vmGUID := strings.TrimSpace(out)
	if vmGUID == "" {
		_ = d.deleteBestEffort(ctx, vmName, diffPath)
		return nil, fmt.Errorf("hyperv create: empty VM GUID returned")
	}
```

with:

```go
	out, err := d.ps(ctx, createScript)
	if err != nil {
		return nil, d.createFailure(ctx, vmName, diffPath, fmt.Errorf("hyperv create VM %q: %w", vmName, err))
	}

	vmGUID := strings.TrimSpace(out)
	if vmGUID == "" {
		return nil, d.createFailure(ctx, vmName, diffPath, fmt.Errorf("hyperv create: empty VM GUID returned"))
	}
```

- [x] **Step 6: Update `TestDriver_Create_CleanupOnFailure`'s mock**

The existing test at driver_test.go:99-125 has a `default:` branch returning `"", nil` for the cleanup script — with the new existence-check appended, that branch is now also deleteBestEffort's own retry loop (up to 3 calls). Update the mock's `default` case to return `"\n", nil` (empty existence check = confirmed gone, so the test's original "cleanup succeeds" intent still holds) and update the `callCount < 4` assertion's comment to note deleteBestEffort now makes exactly one round-trip when the VM is confirmed gone on the first attempt (no retry needed) — the assertion's numeric threshold does not need to change.

- [x] **Step 7: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 8: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#174): bounded cleanup retry, surface OrphanedResourceError with real GUID on create failure

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 4: `Provisioner.Provision` records a quarantined resource on orphan

**Files:**
- Modify: `internal/pool/provisioner_driver.go:29-39`
- Modify: `internal/pool/provisioner_agent.go:38-53`
- Modify: `internal/pool/provisioner_driver_test.go`, `internal/pool/provisioner_agent_test.go`

**Interfaces:**
- Consumes: `providersdk.OrphanedResourceError` (Task 1), `model.ResourceStateError` (existing).
- Produces: no new exported names — `Provision`'s existing signature (`(model.Resource, error)`) is unchanged; only its error-path return value changes shape (non-zero `Resource` alongside a non-nil `error` specifically for this case).

- [x] **Step 1: Write the failing tests**

```go
// internal/pool/provisioner_driver_test.go — add
// fakeProviderDriver gains a createErr field: if set, Create returns
// (nil, d.createErr) instead of succeeding.
```

Add to `fakeProviderDriver`:

```go
type fakeProviderDriver struct {
	createCfg      any
	createErr      error
	deleted        []string
	allocated      []string
	personalized   []string
	deleteErr      error
	personalizeErr error
	personalize    bool
}

func (d *fakeProviderDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	_ = ctx
	d.createCfg = cfg
	if d.createErr != nil {
		return nil, d.createErr
	}
	image, _ := cfg.(map[string]any)["image"].(string)
	return &providersdk.Resource{
		ID:             "provider-res-1",
		ConnectionInfo: map[string]string{"host": "127.0.0.1"},
		Metadata:       map[string]string{"image": image},
	}, nil
}
```

```go
func TestDriverProvisioner_ProvisionQuarantinesOrphanedResource(t *testing.T) {
	driver := &fakeProviderDriver{createErr: &providersdk.OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}}
	dp := newDriverProvisioner(t, driver)

	res, err := dp.Provision(context.Background(), model.Pool{
		Name:      "web",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: "alpine"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.ID != "guid-1" {
		t.Errorf("ID = %q, want %q", res.ID, "guid-1")
	}
	if res.OriginPool != "web" {
		t.Errorf("OriginPool = %q, want %q", res.OriginPool, "web")
	}
	if res.State != model.ResourceStateError {
		t.Errorf("State = %q, want %q", res.State, model.ResourceStateError)
	}
	if res.Properties["quarantine_reason"] != "remove-vm failed" {
		t.Errorf("quarantine_reason = %v, want %q", res.Properties["quarantine_reason"], "remove-vm failed")
	}
}

func TestDriverProvisioner_ProvisionPlainErrorWithoutOrphan(t *testing.T) {
	driver := &fakeProviderDriver{createErr: errors.New("boom")}
	dp := newDriverProvisioner(t, driver)

	res, err := dp.Provision(context.Background(), model.Pool{Name: "web"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.ID != "" {
		t.Errorf("expected zero-value Resource for a non-orphan failure, got %+v", res)
	}
}
```

Mirror both tests in `internal/pool/provisioner_agent_test.go` against `AgentProvisioner` — check that file's existing fake-agent helper for the right construction pattern, and assert `res.Provider.AgentID` is also populated on the quarantine path (unlike `DriverProvisioner`, which has no `AgentID` concept).

- [x] **Step 2: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — both `Provision` methods currently return `model.Resource{}, err` unconditionally on `Create`/`agent.Create` failure.

- [x] **Step 3: Update `DriverProvisioner.Provision`**

```go
func (dp *DriverProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	driver, providerName, err := dp.driverForPool(pool.Name)
	if err != nil {
		return model.Resource{}, fmt.Errorf("provision pool %q: %w", pool.Name, err)
	}

	spec := dp.Specs[pool.Name]
	res, err := driver.Create(ctx, spec.Config)
	if err != nil {
		wrapped := fmt.Errorf("driver create for pool %q: %w", pool.Name, err)
		var orphanErr *providersdk.OrphanedResourceError
		if errors.As(err, &orphanErr) {
			return dp.quarantinedResource(pool.Name, providerName, "", orphanErr), wrapped
		}
		return model.Resource{}, wrapped
	}
	// ... unchanged from here
}

// quarantinedResource builds the ResourceStateError record written for a
// Create failure whose cleanup also failed (see #174). agentID is empty for
// DriverProvisioner (no agent concept); AgentProvisioner passes its own.
func (dp *DriverProvisioner) quarantinedResource(poolName model.PoolName, providerName, agentID string, orphanErr *providersdk.OrphanedResourceError) model.Resource {
	now := time.Now().UTC()
	if dp.Now != nil {
		now = dp.Now().UTC()
	}
	return model.Resource{
		ID:         model.ResourceID(orphanErr.ID),
		OriginPool: poolName,
		Provider:   model.ProviderRef{Name: providerName, AgentID: agentID},
		State:      model.ResourceStateError,
		Properties: map[string]any{"quarantine_reason": orphanErr.CauseMessage},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
```

Add `"errors"` to the file's imports.

- [x] **Step 4: Update `AgentProvisioner.Provision`** the same way

```go
func (ap *AgentProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return model.Resource{}, fmt.Errorf("unknown pool %q", pool.Name)
	}

	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.Registry.Resolve(driverType, spec.Agent)
	if err != nil {
		return model.Resource{}, fmt.Errorf("resolve agent for pool %q: %w", pool.Name, err)
	}

	res, err := agent.Create(ctx, driverType, spec.Config)
	if err != nil {
		wrapped := fmt.Errorf("agent create for pool %q: %w", pool.Name, err)
		var orphanErr *providersdk.OrphanedResourceError
		if errors.As(err, &orphanErr) {
			return ap.quarantinedResource(pool.Name, string(driverType), agent.Info().ID, orphanErr), wrapped
		}
		return model.Resource{}, wrapped
	}
	// ... unchanged from here
}

func (ap *AgentProvisioner) quarantinedResource(poolName model.PoolName, providerName, agentID string, orphanErr *providersdk.OrphanedResourceError) model.Resource {
	now := time.Now().UTC()
	if ap.Now != nil {
		now = ap.Now().UTC()
	}
	return model.Resource{
		ID:         model.ResourceID(orphanErr.ID),
		OriginPool: poolName,
		Provider:   model.ProviderRef{Name: providerName, AgentID: agentID},
		State:      model.ResourceStateError,
		Properties: map[string]any{"quarantine_reason": orphanErr.CauseMessage},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
```

Add `"errors"` to the file's imports.

- [x] **Step 5: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/pool/provisioner_driver.go internal/pool/provisioner_agent.go internal/pool/provisioner_driver_test.go internal/pool/provisioner_agent_test.go
git commit -m "feat(#174): Provisioner.Provision quarantines orphaned resources instead of dropping their ID

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 5: Pool manager writes and sweeps quarantined orphans

**Files:**
- Modify: `internal/pool/manager.go` (actuator's provision loop, lines 583-599; evaluator, lines 463-524 region; new `quarantinedOrphans` function near `orphanedTransientResources`, lines 684-732)
- Modify: `internal/pool/manager_test.go`

**Interfaces:**
- Consumes: `model.ResourceStateError` (existing), the quarantine-record shape from Task 4.
- Produces: `quarantinedOrphans(poolName model.PoolName, resources []model.Resource) []model.Resource`.

- [x] **Step 1: Write the failing tests**

```go
// internal/pool/manager_test.go — add

func TestQuarantinedOrphans_MatchesErrorStateWithMatchingOriginPool(t *testing.T) {
	resources := []model.Resource{
		{ID: "q1", OriginPool: "p1", State: model.ResourceStateError},
		{ID: "ready1", OriginPool: "p1", State: model.ResourceStateReady},
		{ID: "q2", OriginPool: "p2", State: model.ResourceStateError}, // different pool
	}
	got := quarantinedOrphans("p1", resources)
	if len(got) != 1 || got[0].ID != "q1" {
		t.Fatalf("quarantinedOrphans = %+v, want only q1", got)
	}
}

func TestManager_Reconcile_ProvisionFailureWritesQuarantinedResource(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{
		provisionErr:         errors.New("driver create for pool \"p1\": boom"),
		provisionResultOnErr: model.Resource{ID: "quarantine-1", OriginPool: "p1", Provider: model.ProviderRef{Name: "prov_1"}, State: model.ResourceStateError, Properties: map[string]any{"quarantine_reason": "boom"}},
	}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected Reconcile to surface the provision failure")
	}

	res, err := st.GetResource(ctx, "quarantine-1")
	if err != nil {
		t.Fatalf("expected the quarantined resource to be written to the store: %v", err)
	}
	if res.State != model.ResourceStateError || res.OriginPool != "p1" {
		t.Fatalf("quarantined resource = %+v, want State=Error OriginPool=p1", res)
	}
}

func TestManager_Reconcile_SweepsQuarantinedOrphan(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	quarantined := model.Resource{
		ID:         "quarantine-1",
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateError,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, quarantined); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != quarantined.ID {
		t.Fatalf("destroyed = %v, want quarantined resource %q swept", prov.destroyed, quarantined.ID)
	}
	final, err := st.GetResource(ctx, quarantined.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if final.State != model.ResourceStateDestroyed {
		t.Fatalf("final state = %q, want %q", final.State, model.ResourceStateDestroyed)
	}
}
```

Add the `provisionResultOnErr model.Resource` field to `fakeProvisioner` and update its `Provision` method: when `p.provisionErr != nil`, return `(p.provisionResultOnErr, p.provisionErr)` instead of `(model.Resource{}, p.provisionErr)`.

- [x] **Step 2: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — `quarantinedOrphans` undefined; the actuator doesn't write a resource on provision failure yet.

- [x] **Step 3: Add `quarantinedOrphans`**

Add near `orphanedTransientResources` (after it, around line 732):

```go
// quarantinedOrphans finds resources this pool tried to provision that ended
// up recorded as ResourceStateError — a Create failed and its cleanup also
// failed (see #174, Task 4's Provisioner.Provision handling of
// providersdk.OrphanedResourceError). Unlike orphanedTransientResources,
// these haven't started teardown yet — isTransientDestroyState only covers
// Recycling/Destroying — so this is a separate filter rather than
// broadening that one; see the design spec's "Alternatives considered" for
// why isTransientDestroyState itself isn't touched.
func quarantinedOrphans(poolName model.PoolName, resources []model.Resource) []model.Resource {
	var quarantined []model.Resource
	for _, res := range resources {
		if res.State == model.ResourceStateError && res.OriginPool == poolName {
			quarantined = append(quarantined, res)
		}
	}
	return quarantined
}
```

- [x] **Step 4: Merge `quarantinedOrphans` into the evaluator's stale/drain lists**

In `reconcileLocked`'s evaluator, immediately after the existing:

```go
			inInventory := resourceIDSet(p.Inventory.Resources)
			orphans := orphanedTransientResources(p.Name, obs.resources, inInventory, fallbackInventoryIDs)
```

add:

```go
			quarantined := quarantinedOrphans(p.Name, obs.resources)
```

In the `EffectivelyDrained()` branch, change:

```go
				toDrain := append([]model.Resource(nil), p.Inventory.Resources...)
				toDrain = append(toDrain, orphans...)
```

to:

```go
				toDrain := append([]model.Resource(nil), p.Inventory.Resources...)
				toDrain = append(toDrain, orphans...)
				toDrain = append(toDrain, quarantined...)
```

and update that branch's `reason` format string to include `quarantined=%d` alongside the existing `orphans=%d` for observability parity.

In the non-drained path, change:

```go
			stale, kept, err := computeStale(p, obs.now)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			stale = append(stale, orphans...)
```

to:

```go
			stale, kept, err := computeStale(p, obs.now)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			stale = append(stale, orphans...)
			stale = append(stale, quarantined...)
```

No change is needed to the actuator's stale-destroy loop (`m.destroyAndMark(ctx, p, res, model.ResourceStateRecycling, pl.now)`) — `quarantinedOrphans`' results flow through it unmodified, exactly like `orphanedTransientResources`' results already do.

- [x] **Step 5: Write quarantined resources on provision failure in the actuator**

Change:

```go
			for i := 0; i < pl.toProvision; i++ {
				res, err := m.provisioner.Provision(ctx, p)
				if err != nil {
					m.recordProvisionFailure(p.Name, pl.now)
					return fmt.Errorf("provision resource for pool %q: %w", p.Name, err)
				}
```

to:

```go
			for i := 0; i < pl.toProvision; i++ {
				res, err := m.provisioner.Provision(ctx, p)
				if err != nil {
					if res.ID != "" {
						if putErr := m.store.PutResource(ctx, res); putErr != nil {
							return fmt.Errorf("put quarantined resource %q: %w", res.ID, putErr)
						}
					}
					m.recordProvisionFailure(p.Name, pl.now)
					return fmt.Errorf("provision resource for pool %q: %w", p.Name, err)
				}
```

- [x] **Step 6: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add internal/pool/manager.go internal/pool/manager_test.go
git commit -m "feat(#174): pool manager writes and auto-sweeps quarantined orphan resources

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 6: `hyperv.Driver` implements `providersdk.ResourceLister`

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go` (add `List`, near `Read`)
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go`

**Interfaces:**
- Consumes: `providersdk.ResourceLister`, `providersdk.ResourceStatus` (existing), `normalizeVMState` (existing, `driver.go:253-278`).
- Produces: `(d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error)`.

- [x] **Step 1: Write the failing test**

```go
// pkg/providersdk/providers/hyperv/driver_test.go — add
func TestDriver_List_FiltersToBoxyPrefixedVMs(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, "boxy-*") {
			t.Errorf("expected script to filter by boxy-* prefix, got: %s", script)
		}
		return "guid-1|Running\nguid-2|Off\n", nil
	})

	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v, want 2", statuses)
	}
	if statuses[0].ID != "guid-1" || statuses[0].State != "running" {
		t.Errorf("statuses[0] = %+v, want {guid-1 running}", statuses[0])
	}
	if statuses[1].ID != "guid-2" || statuses[1].State != "stopped" {
		t.Errorf("statuses[1] = %+v, want {guid-2 stopped}", statuses[1])
	}
}

func TestDriver_List_EmptyHost(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) { return "", nil })
	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("statuses = %+v, want empty", statuses)
	}
}

var _ providersdk.ResourceLister = (*Driver)(nil)
```

- [x] **Step 2: Run test to verify it fails**

Run: `task test`
Expected: FAIL — `d.List` undefined; the `var _ providersdk.ResourceLister = (*Driver)(nil)` assertion fails to compile.

- [x] **Step 3: Implement `List`**

Add near `Read` (after line 251, before `normalizeVMState`):

```go
// List satisfies providersdk.ResourceLister, enumerating every boxy-*-named
// VM this driver's host currently has — including ones the store has no
// record of, e.g. left behind by a crash between New-VM succeeding and
// Create's failure branch running (see #174). Prefix-filtered inside the
// PowerShell query itself, not client-side, so a host running unrelated VMs
// alongside Boxy's never returns them to a caller that doesn't expect it.
func (d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
Get-VM | Where-Object { $_.Name -like 'boxy-*' } | ForEach-Object { "$($_.Id)|$($_.State)" }
`)
	if err != nil {
		return nil, fmt.Errorf("hyperv list: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	statuses := make([]providersdk.ResourceStatus, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		statuses = append(statuses, providersdk.ResourceStatus{
			ID:    strings.TrimSpace(parts[0]),
			State: normalizeVMState(strings.TrimSpace(parts[1])),
		})
	}
	return statuses, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#174): implement providersdk.ResourceLister for the Hyper-V driver

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 7: Periodic agent reconciliation (defense-in-depth sweep)

**Files:**
- Modify: `internal/pool/reconcile.go` (add `RunAgentReconciliation`)
- Modify: `internal/pool/reconcile_test.go`
- Modify: `internal/agentserver/server.go` (lines 227-239: swap the one-shot goroutine for the periodic one)

**Interfaces:**
- Consumes: `ReconcileAgent` (existing, `reconcile.go:36`), `store.Store`, `*AgentRegistry`.
- Produces: `RunAgentReconciliation(ctx context.Context, st store.Store, registry *AgentRegistry, agentID string, interval, passTimeout time.Duration, logger *slog.Logger)`.

- [x] **Step 1: Write the failing test**

```go
// internal/pool/reconcile_test.go — add
func TestRunAgentReconciliation_RunsImmediatelyThenOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.NewMemoryStore()

	var callCount atomic.Int32
	agent := &fakeListingAgent{
		info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"docker"}},
		listResults: map[providersdk.Type][]providersdk.ResourceStatus{
			"docker": {{ID: "orphan-1", State: "running"}},
		},
	}
	registry := registryWith(t, agent)

	done := make(chan struct{})
	go func() {
		RunAgentReconciliation(ctx, st, registry, "agent-1", 10*time.Millisecond, time.Second, slog.Default())
		close(done)
	}()

	// Wait for at least two passes (immediate + one tick), then cancel.
	deadline := time.After(time.Second)
	for {
		if res, err := st.GetResource(context.Background(), "orphan-1"); err == nil && res.ID == "orphan-1" {
			callCount.Add(1)
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for the first reconciliation pass to adopt orphan-1")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunAgentReconciliation did not return after ctx cancellation")
	}
}

func TestRunAgentReconciliation_ContinuesPastAPassError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.NewMemoryStore()
	registry := NewAgentRegistry() // "agent-1" never registered -> every ReconcileAgent call errors

	done := make(chan struct{})
	go func() {
		RunAgentReconciliation(ctx, st, registry, "agent-1", 5*time.Millisecond, time.Second, slog.Default())
		close(done)
	}()

	time.Sleep(30 * time.Millisecond) // let several failing passes tick without the loop exiting
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunAgentReconciliation should return promptly on ctx cancellation even after repeated pass errors")
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — `RunAgentReconciliation` undefined.

- [x] **Step 3: Implement `RunAgentReconciliation`**

Add to `internal/pool/reconcile.go`, after `ReconcileAgent`:

```go
// RunAgentReconciliation runs ReconcileAgent's Observe/Decide/Act cycle
// immediately, then repeatedly on interval, until ctx is cancelled —
// defense-in-depth for orphans #174's inline Create-failure handling can't
// see (e.g. an agent crash between New-VM succeeding and the failure branch
// running). Each pass is bounded by passTimeout, applied on top of (never
// beyond) ctx. A failed pass is logged and skipped rather than ending the
// loop, matching the previous one-shot call's guarantee that reconciliation
// trouble must never take down agent connectivity. interval is expected to
// be the connection's own heartbeat interval (see
// internal/agentserver/server.go) rather than a new standalone constant.
func RunAgentReconciliation(ctx context.Context, st store.Store, registry *AgentRegistry, agentID string, interval, passTimeout time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = defaultReconciliationInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		pctx, cancel := context.WithTimeout(ctx, passTimeout)
		if err := ReconcileAgent(pctx, st, registry, agentID, logger); err != nil {
			logger.Warn("periodic reconciliation failed", "agent_id", agentID, "error", err)
		}
		cancel()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// defaultReconciliationInterval is RunAgentReconciliation's fallback when no
// interval is supplied (interval <= 0) — production callers pass the
// connection's own heartbeat interval instead.
const defaultReconciliationInterval = 15 * time.Second
```

- [x] **Step 4: Wire it into `internal/agentserver/server.go`**

Replace lines 227-239:

```go
	// The #133 reconciliation sweep needs Serve() already pumping the
	// stream (List is itself a command sent down it), so it can only start
	// here, not before. Runs on every successful registration, not just
	// reconnects — see pool.ReconcileAgent's doc comment. Bounded and
	// logged-only: reconciliation trouble must never take down agent
	// connectivity.
	go func() {
		rctx, cancel := context.WithTimeout(ctx, reconciliationTimeout)
		defer cancel()
		if err := pool.ReconcileAgent(rctx, s.store, s.registry, agentID, s.log()); err != nil {
			s.log().Warn("post-registration reconciliation failed", "agent_id", agentID, "error", err)
		}
	}()
```

with:

```go
	// The #133 reconciliation sweep needs Serve() already pumping the
	// stream (List is itself a command sent down it), so it can only start
	// here, not before. Runs immediately on every successful registration,
	// not just reconnects, then repeatedly on the connection's heartbeat
	// cadence for as long as the connection lasts — see #174's periodic
	// defense-in-depth sweep. ctx is the connection-scoped context already
	// used by this handler's own select below, so the loop stops naturally
	// on disconnect; each pass stays bounded by reconciliationTimeout,
	// logged-only on failure: reconciliation trouble must never take down
	// agent connectivity.
	go pool.RunAgentReconciliation(ctx, s.store, s.registry, agentID, s.heartbeatInterval, reconciliationTimeout, s.log())
```

- [x] **Step 5: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/pool/reconcile.go internal/pool/reconcile_test.go internal/agentserver/server.go
git commit -m "feat(#174): periodic agent reconciliation sweep, replacing the registration-only one-shot

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 8: #183 Mitigation 1 — release right after `Start-VM`, not at `Create`'s return

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go` (`Create`, script construction and execution)
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go`

**Interfaces:**
- Consumes: `createFailure` (Task 3), `release func()` from `reserveMemory` (existing).
- Produces: `(d *Driver) resolveCreatedVMID(ctx context.Context, vmName string) (string, error)`.

- [x] **Step 1: Write the failing tests**

```go
// pkg/providersdk/providers/hyperv/driver_test.go — add
func TestDriver_Create_SplitsSetupAndIDLookupCalls(t *testing.T) {
	var sawStartVMCall, sawIDLookupCall bool
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Start-VM"):
			sawStartVMCall = true
			if strings.Contains(script, ".Id.ToString()") {
				t.Error("expected the ID lookup NOT to be in the same call as Start-VM")
			}
			return "", nil
		case strings.Contains(script, ".Id.ToString()"):
			sawIDLookupCall = true
			if strings.Contains(script, "Start-VM") {
				t.Error("expected Start-VM NOT to be in the same call as the ID lookup")
			}
			return fakeGUID + "\n", nil
		default:
			return "", nil
		}
	})

	res, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID != fakeGUID {
		t.Errorf("ID = %q, want %q", res.ID, fakeGUID)
	}
	if !sawStartVMCall || !sawIDLookupCall {
		t.Fatalf("expected both a setup+Start-VM call and a separate ID lookup call, got startVM=%v idLookup=%v", sawStartVMCall, sawIDLookupCall)
	}
}

func TestDriver_Create_IDLookupFailureDoesNotTriggerCleanup(t *testing.T) {
	cleanupScriptSeen := false
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Start-VM"):
			return "", nil // setup + Start-VM succeeds
		case strings.Contains(script, ".Id.ToString()"):
			return "", fmt.Errorf("transient Get-VM failure")
		case strings.Contains(script, "Remove-VM"):
			cleanupScriptSeen = true
			return "", nil
		default:
			return "", nil
		}
	})
	d.deleteBestEffortInterval = time.Millisecond

	_, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	if err == nil {
		t.Fatal("expected an error when the ID lookup fails")
	}
	var orphanErr *providersdk.OrphanedResourceError
	if errors.As(err, &orphanErr) {
		t.Fatalf("expected a plain error, not *OrphanedResourceError, when only the ID lookup fails: %#v", orphanErr)
	}
	if cleanupScriptSeen {
		t.Error("expected NO cleanup (Stop-VM/Remove-VM) when only the ID lookup failed — the VM is healthy and running")
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `task test`
Expected: FAIL — `Create` still issues one combined script.

- [x] **Step 3: Split the create script and add `resolveCreatedVMID`**

In `Create`, replace the single `createScript`/`d.ps(ctx, createScript)` block with two calls. The setup script drops its trailing `(Get-VM -Name '%s').Id.ToString()` line and its final `%s` verb/argument:

```go
	createScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
New-VHD -ParentPath '%s' -Path '%s' -Differencing | Out-Null
New-VM -Name '%s' -Generation %d -MemoryStartupBytes %d -VHDPath '%s' | Out-Null%s
Set-VM -Name '%s' -ProcessorCount %d | Out-Null
Set-VM -Name '%s' -Notes '%s' | Out-Null
Start-VM -Name '%s' | Out-Null
`,
		psq(cc.TemplateVHD),
		psq(diffPath),
		psq(vmName), cc.Generation, memBytes, psq(diffPath),
		switchBlock,
		psq(vmName), cc.CPUCount,
		psq(vmName), psq(notes),
		psq(vmName),
	)

	if _, err := d.ps(ctx, createScript); err != nil {
		return nil, d.createFailure(ctx, vmName, diffPath, fmt.Errorf("hyperv create VM %q: %w", vmName, err))
	}
	// Memory is committed once Start-VM above returns — stop holding the
	// reservation before the trailing (unguarded) ID lookup, narrowing the
	// over-reservation window described in #183 down to this one fast call
	// instead of the whole Create duration.
	releaseOnce()

	vmGUID, err := d.resolveCreatedVMID(ctx, vmName)
	if err != nil {
		// The VM is healthy and running — Start-VM already succeeded above.
		// Do NOT clean it up: that would destroy a good VM over a metadata
		// lookup hiccup. Leave it for the periodic ResourceLister sweep
		// (#174, Task 7) to pick up later.
		return nil, fmt.Errorf("hyperv create VM %q: resolve id: %w", vmName, err)
	}
```

Remove the old `out, err := d.ps(ctx, createScript)` and `vmGUID := strings.TrimSpace(out); if vmGUID == "" { ... }` block entirely — replaced by the above.

Change the reservation setup near the top of `Create` from:

```go
	release, err := d.reserveMemory(ctx, int64(cc.MemoryMB))
	if err != nil {
		return nil, err
	}
	defer release()
```

to:

```go
	release, err := d.reserveMemory(ctx, int64(cc.MemoryMB))
	if err != nil {
		return nil, err
	}
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()
```

(`sync.OnceFunc` ensures the still-present `defer releaseOnce()` is a no-op on the success path, where `releaseOnce()` above already fired — and still fires exactly once on any early-return failure path before that point, e.g. `checkHostHealth`'s error return, or the new create-script failure branch above.)

Add `resolveCreatedVMID` and its constants near `deleteBestEffort`:

```go
const (
	resolveVMIDAttempts       = 3
	defaultResolveVMIDInterval = 2 * time.Second
)

// resolveCreatedVMID looks up a just-started VM's GUID in a call separate
// from the create script (see #183's script split) — read-only, retried a
// few times for a transient Get-VM hiccup, never mutating anything, so a
// failure here never implies the VM itself is unhealthy.
func (d *Driver) resolveCreatedVMID(ctx context.Context, vmName string) (string, error) {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VM -Name '%s').Id.ToString()
`, psq(vmName))

	var lastErr error
	for attempt := 1; attempt <= resolveVMIDAttempts; attempt++ {
		out, err := d.ps(ctx, script)
		if err == nil {
			if guid := strings.TrimSpace(out); guid != "" {
				return guid, nil
			}
			lastErr = fmt.Errorf("empty VM GUID returned")
		} else {
			lastErr = err
		}
		if attempt < resolveVMIDAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(d.bestEffortInterval()):
			}
		}
	}
	return "", lastErr
}
```

(Reuses `d.bestEffortInterval()` from Task 3 rather than adding a third overridable interval field — both are "cheap bounded retry, 2s apart" by default and tests already override the one field.)

Add `"sync"` to the file's imports if not already present (it is — `Driver.mu sync.Mutex` already imports it).

- [x] **Step 4: Run tests to verify they pass**

Run: `task test`
Expected: PASS — including all of Task 3's existing tests, which must still pass since `createFailure`'s behavior on a call-1 failure is unchanged.

- [x] **Step 5: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#183): release memory reservation right after Start-VM, not at Create's return

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 9: #183 Mitigation 2 — grace-period release

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go` (`reserveMemory`'s returned closure, lines 537-567)
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go`

**Interfaces:**
- Consumes: none new.
- Produces: `reservationGraceInterval` constant.

- [x] **Step 1: Write the failing test**

```go
// pkg/providersdk/providers/hyperv/driver_test.go — add
func TestDriver_ReserveMemory_ReleaseHasGracePeriod(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		return "1024\n", nil // 1 GB available
	})

	release, err := d.reserveMemory(context.Background(), 512)
	if err != nil {
		t.Fatalf("reserveMemory: %v", err)
	}
	release()

	// Immediately after release() returns, the reservation must still be
	// counted — the decrement is scheduled, not synchronous.
	d.mu.Lock()
	immediatelyAfter := d.reservedMB
	d.mu.Unlock()
	if immediatelyAfter != 512 {
		t.Fatalf("reservedMB immediately after release() = %d, want 512 (still held during the grace period)", immediatelyAfter)
	}

	time.Sleep(reservationGraceInterval + 200*time.Millisecond)
	d.mu.Lock()
	afterGracePeriod := d.reservedMB
	d.mu.Unlock()
	if afterGracePeriod != 0 {
		t.Fatalf("reservedMB after the grace period = %d, want 0", afterGracePeriod)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `task test`
Expected: FAIL — today's `release()` decrements `reservedMB` synchronously, so `immediatelyAfter` would already be 0.

- [x] **Step 3: Add the grace period**

In the `const` block, add:

```go
	// reservationGraceInterval delays reserveMemory's release() decrement
	// past Create's return, biasing #183's under-/over-reservation tradeoff
	// toward the safe direction: a stale-high reservedMB can only cause a
	// spurious CapacityError on an immediately-following sequential Create
	// (annoying, safe), never let one overcommit the host (dangerous). Same
	// order of magnitude as defaultDeleteWaitInterval.
	reservationGraceInterval = 5 * time.Second
```

In `reserveMemory`, replace:

```go
	d.reservedMB += requestedMB
	return func() {
		d.mu.Lock()
		d.reservedMB -= requestedMB
		d.mu.Unlock()
	}, nil
```

with:

```go
	d.reservedMB += requestedMB
	return func() {
		// Independent of the caller's ctx (which may already be cancelled
		// by the time Create returns) — this is pure in-process bookkeeping,
		// not I/O, so it doesn't need one.
		time.AfterFunc(reservationGraceInterval, func() {
			d.mu.Lock()
			d.reservedMB -= requestedMB
			d.mu.Unlock()
		})
	}, nil
```

- [x] **Step 4: Update three existing tests whose premise the grace period changes**

Three existing tests assert `reservedMB == 0` immediately after calling
`release()` — all three now need to reflect that the decrement is delayed,
not synchronous:

`TestDriver_ReserveMemory_SufficientCapacitySucceeds` (driver_test.go:279-299)
— change the final block from:

```go
	release()
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after release = %d, want 0", d.reservedMB)
	}
```

to:

```go
	release()
	if d.reservedMB != 2048 {
		t.Errorf("reservedMB immediately after release() = %d, want 2048 (grace period still holding it)", d.reservedMB)
	}
	time.Sleep(reservationGraceInterval + 200*time.Millisecond)
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after the grace period = %d, want 0", d.reservedMB)
	}
```

`TestDriver_ReserveMemory_ConcurrentCallsLimitToCapacity` (driver_test.go:360-414)
— change the final block from:

```go
	for release := range releases {
		release()
	}
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after releasing all = %d, want 0", d.reservedMB)
	}
```

to:

```go
	for release := range releases {
		release()
	}
	time.Sleep(reservationGraceInterval + 200*time.Millisecond)
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after releasing all and the grace period = %d, want 0", d.reservedMB)
	}
```

`TestDriver_Create_ReservationReleasedAfterFailureAllowsNextCreate` (driver_test.go:416-446)
— this test's original premise ("reservedMB after a failed Create = 0,
must not leak, so a second Create can immediately reserve the same memory")
is now deliberately false: Mitigation 2 holds the reservation for
`reservationGraceInterval` after any release, including a failed `Create`'s.
Rename and rewrite it to assert the new intentional behavior instead:

```go
func TestDriver_Create_ReservationHeldThroughGracePeriodAfterFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			return "\n", nil // deleteBestEffort: confirmed gone on first attempt
		}
	})

	if _, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`}); err == nil {
		t.Fatal("expected the first Create to fail")
	}
	if d.reservedMB == 0 {
		t.Fatal("expected reservedMB to still be held immediately after a failed Create (grace period)")
	}

	time.Sleep(reservationGraceInterval + 200*time.Millisecond)
	if d.reservedMB != 0 {
		t.Fatalf("reservedMB after the grace period = %d, want 0 (must not leak permanently)", d.reservedMB)
	}

	// A second reservation must succeed once the grace period has elapsed.
	release, err := d.reserveMemory(context.Background(), 2048)
	if err != nil {
		t.Fatalf("second reservation unexpectedly failed: %v", err)
	}
	release()
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `task test`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#183): grace-period release for the memory reservation, biased toward the safe failure direction

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```

---

### Task 10: Document the work in ADR-0004, close out the design spec

**Files:**
- Modify: `docs/adr/0004-hyperv-teardown-guard-and-provisioning-backoff.md`

**Interfaces:** None (docs only).

- [x] **Step 1: Append a new section to ADR-0004**

After the existing "Memory preflight and reservation (#173)" section (ends around line 87, before "## Consequences"), add:

```markdown
### Create-failure cleanup, quarantine, and typed error propagation (#174, #185, #183)

`Create`'s cleanup-on-failure path (`deleteBestEffort`) now retries up to 3
times, 2s apart, and its own existence re-check (needed anyway, since
`-ErrorAction SilentlyContinue` masked whether `Remove-VM` actually worked)
resolves the VM's real GUID when cleanup still can't confirm it's gone.
`Create` returns a typed `*providersdk.OrphanedResourceError{ID, CauseMessage}`
in that case instead of a plain error. `Provisioner.Provision` (both the
embedded-driver and remote-agent variants) recognizes this type and writes a
`ResourceStateError` resource record — carrying the real GUID and the
originating pool — instead of the failure vanishing with no ID anywhere in
Boxy's store, which was the root cause of #174's "orphaned VMs the host
accumulates but Boxy never learns about." `pool.Manager`'s reconcile loop
picks up these quarantined records automatically via a new
`quarantinedOrphans` filter (parallel to, not a change to,
`orphanedTransientResources`/`isTransientDestroyState` — a quarantined
resource hasn't started teardown yet, which is that helper's documented
meaning) and retries destroying them the same way any other stale resource
is retried.

As defense-in-depth for orphans this inline path can't see (e.g. an agent
crash between `New-VM` succeeding and the failure branch running), the
Hyper-V driver now implements `providersdk.ResourceLister` (same convention
docker already used for #133), and the post-registration reconciliation
sweep (`pool.ReconcileAgent`) now runs periodically for the life of an
agent's connection (`pool.RunAgentReconciliation`, on the connection's
heartbeat cadence) instead of once at registration only — closing the gap
where a long-connected agent (the realistic Hyper-V deployment topology; see
ADR-0005) never got re-audited.

`CapacityError` moved from `hyperv` to `providersdk` (aliased back for
compatibility) since it's not intrinsically Hyper-V-specific, alongside a new
`OrphanedResourceError` and a small `providersdk.ErrorTyper` interface. Both
typed errors now survive the `RemoteAgent`/gRPC boundary (#185): `AgentError`
gained `error_type`/`error_detail_json` fields (opaque JSON, mirroring
`CreateCommand.config_json`'s existing rationale, so a future typed error
never needs another proto change), and the quarantine mechanism above works
identically over that boundary — without it, quarantine would only have
worked for the in-process embedded-agent path, silently doing nothing on the
realistic remote-agent deployment.

Finally, #183's in-process memory-reservation window (the tension between
`Driver.reservedMB` and the live host-memory query around a `Create` call's
boundary) is narrowed, not closed — the issue's own analysis found no clean
full fix. The create script now releases its reservation immediately after
`Start-VM` succeeds rather than holding it through a trailing, now-separate
ID-lookup call, shrinking the over-reservation window from the whole `Create`
duration to one fast `Get-VM` call; and `release()` now delays its decrement
by a 5s grace period, deliberately biasing toward the safer failure direction
(a stale-high reservation can only cause a spurious, harmless
`CapacityError` on an immediately-following sequential `Create`, never let
one overcommit the host). A sequential `Create` arriving after that grace
period but before the OS counter catches up can still overcommit — #183
stays open, documenting this residual gap rather than being closed as fully
fixed.

Full design: `docs/superpowers/specs/2026-08-13-hyperv-create-failure-hardening-design.md`.
```

- [x] **Step 2: Commit**

```bash
git add docs/adr/0004-hyperv-teardown-guard-and-provisioning-backoff.md
git commit -m "docs: record create-failure hardening (#174, #185, #183) in ADR-0004

Co-Authored-By: Claude Sonnet 5 <boxy-bot@example.invalid>
Claude-Session: https://claude.ai/code/session_01WrFcL5kHN8FTgqZqBqWRQm"
```
