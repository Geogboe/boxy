# Hyper-V Memory Preflight + Reservation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before `hyperv.Driver.Create` runs its `New-VHD`/`New-VM`/`Start-VM` script, check the host actually has enough free memory and atomically reserve it across concurrent `Create` calls, replacing the raw `0x8007000E` PowerShell crash with a typed, immediate rejection (#173, Phase A).

**Architecture:** A new optional `providersdk.AvailabilityReporter` interface (two real implementers: `hyperv.Driver` does a live host query, `devfactory.Driver` returns a static config value). `hyperv.Driver` gains an in-process mutex-guarded reservation counter (`reservedMB`) so `Create` can atomically check-and-commit against live host memory before running its create script, and release the hold via `defer` on every exit path.

**Tech Stack:** Go, PowerShell (`Get-CimInstance Win32_OperatingSystem`), existing `psExec` test-mock seam.

## Global Constraints

- `defaultHostReserveMB = 512` (confirmed default headroom, unexported constant — not user-configurable this PR; see spec's "Known gap").
- The error type is named `CapacityError` (package `hyperv`), never `HyperVCapacityError` — this codebase never repeats the package name in exported type names (`pool.MaxTotalReachedError`).
- `Win32_OperatingSystem.FreePhysicalMemory` is reported in **kilobytes** — every query result must be divided by 1024 before comparing against MB values.
- `reserveMemory` holds its mutex across the entire live PowerShell query, not just the accounting (confirmed: simplest zero-TOCTOU-gap option).
- No wire/proto changes, no `pool.Manager` changes, no `boxy status`/`boxy agent list` changes, no `devfactory.Create`-level enforcement — all out of scope for this PR (see spec's "Non-goals" and "Follow-ups").
- Full spec: `docs/superpowers/specs/2026-08-12-hyperv-memory-preflight-design.md`.

---

### Task 1: `providersdk.AvailabilityReporter` interface

**Files:**
- Create: `pkg/providersdk/availability.go`

**Interfaces:**
- Produces: `providersdk.ResourceAvailability{MemoryMB int64}`, `providersdk.AvailabilityReporter` interface with method `Availability(ctx context.Context) (*ResourceAvailability, error)`.

This is a pure type/interface declaration with no behavior of its own — nothing to TDD here. Verification is the compile-time `var _ providersdk.AvailabilityReporter = (*Driver)(nil)` assertions added in Tasks 2 and 5.

- [ ] **Step 1: Write the interface file**

Mirror the style of the existing `pkg/providersdk/guest_personalization.go` (an optional-capability interface in the same package):

```go
// pkg/providersdk/availability.go
package providersdk

import "context"

// ResourceAvailability is a driver's point-in-time view of how much of a
// resource it can currently hand out to a new Create request.
type ResourceAvailability struct {
	// MemoryMB is free memory available for new resources, in megabytes.
	MemoryMB int64
}

// AvailabilityReporter is an optional provider capability for reporting
// current resource headroom before a Create is attempted. Not every driver
// implements it — callers that care must type-assert, the same pattern as
// GuestPersonalizer.
type AvailabilityReporter interface {
	Availability(ctx context.Context) (*ResourceAvailability, error)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./pkg/providersdk/...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add pkg/providersdk/availability.go
git commit -m "feat(#173): add providersdk.AvailabilityReporter interface"
```

---

### Task 2: `hyperv.CapacityError` + `Driver.Availability()` + `Driver.reserveMemory()`

Implements the reservation mechanism as a standalone, directly-testable unit — not yet wired into `Create` (that's Task 3).

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go`
- Test: `pkg/providersdk/providers/hyperv/driver_test.go`

**Interfaces:**
- Consumes: `providersdk.ResourceAvailability`, `providersdk.AvailabilityReporter` (Task 1).
- Produces: `(d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error)`, `(d *Driver) reserveMemory(ctx context.Context, requestedMB int64) (release func(), err error)`, `CapacityError{RequestedMemoryMB, AvailableMemoryMB int64}` — Task 3 and Task 4 call these by these exact names.

- [ ] **Step 1: Write the failing tests**

Add to `pkg/providersdk/providers/hyperv/driver_test.go`, right before the `// --- Read ---` marker (currently line 176):

```go
// --- Availability / reserveMemory ---

// hyperVFreeMemoryScript is the fragment that appears in the live
// free-memory query script; tests key their psExec mock off it.
const hyperVFreeMemoryScript = "FreePhysicalMemory"

func TestDriver_Availability_NetsOutReserveAndReservations(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, hyperVFreeMemoryScript) {
			t.Fatalf("unexpected script: %s", script)
		}
		return "16777216\n", nil // 16 GB in KB
	})
	d.reservedMB = 1000

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 16 GB = 16384 MB, minus defaultHostReserveMB (512), minus reservedMB (1000).
	want := int64(16384 - 512 - 1000)
	if avail.MemoryMB != want {
		t.Errorf("MemoryMB = %d, want %d", avail.MemoryMB, want)
	}
}

func TestDriver_Availability_QueryFailurePropagates(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("Get-CimInstance failed")
	})

	if _, err := d.Availability(context.Background()); err == nil {
		t.Fatal("expected error when the free-memory query fails")
	}
}

func TestDriver_ReserveMemory_SufficientCapacitySucceeds(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "16777216\n", nil // 16 GB in KB
	})

	release, err := d.reserveMemory(context.Background(), 2048)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release == nil {
		t.Fatal("expected a non-nil release function")
	}
	if d.reservedMB != 2048 {
		t.Errorf("reservedMB = %d, want 2048", d.reservedMB)
	}

	release()
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after release = %d, want 0", d.reservedMB)
	}
}

func TestDriver_ReserveMemory_InsufficientCapacityReturnsCapacityError(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "1048576\n", nil // 1 GB in KB = 1024 MB free
	})

	// 1024 MB free, minus 512 reserve = 512 MB available. Requesting 2048 must fail.
	_, err := d.reserveMemory(context.Background(), 2048)
	var capErr *CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected *CapacityError, got %#v", err)
	}
	if capErr.RequestedMemoryMB != 2048 {
		t.Errorf("RequestedMemoryMB = %d, want 2048", capErr.RequestedMemoryMB)
	}
	if capErr.AvailableMemoryMB != 512 {
		t.Errorf("AvailableMemoryMB = %d, want 512", capErr.AvailableMemoryMB)
	}
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after a rejected reservation = %d, want 0 (nothing committed)", d.reservedMB)
	}
}

// --- providersdk.AvailabilityReporter interface compliance ---

var _ providersdk.AvailabilityReporter = (*Driver)(nil)
```

`driver_test.go`'s import block already has `"errors"`, `"fmt"`, `"strings"`, `"testing"`, and `"github.com/Geogboe/boxy/pkg/providersdk"` — no import changes needed for this task.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run TestDriver_Availability -v` and `go test ./pkg/providersdk/providers/hyperv/... -run TestDriver_ReserveMemory -v`
Expected: FAIL — `d.Availability`, `d.reserveMemory`, `d.reservedMB`, and `CapacityError` are undefined.

- [ ] **Step 3: Write the implementation**

In `pkg/providersdk/providers/hyperv/driver.go`, add `"sync"` to the import block (`strconv` is already imported; `sync` is not). Add the reservation fields to the `Driver` struct (currently lines 22-41):

```go
type Driver struct {
	// psExec is the host-side PowerShell execution backend (VM lifecycle ops).
	// nil → real powershell.exe; inject a mock in tests.
	psExec func(ctx context.Context, script string) (string, error)

	// guestExecFactory constructs a vmsdk.GuestExec for a given guest.
	// nil → real implementation. Inject a mock in tests.
	guestExecFactory func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec

	// resolveSecret resolves a persisted secret reference only when guest
	// bootstrap access is needed.
	resolveSecret func(ctx context.Context, ref providersdk.SecretRef) (string, error)

	// deleteWaitTimeout/deleteWaitInterval bound how long Delete waits for a
	// VM stuck mid-transition (e.g. "Turning Off") to reach a terminal state
	// before giving up. Zero values use production defaults; tests override
	// these to avoid real sleeps. See #118.
	deleteWaitTimeout  time.Duration
	deleteWaitInterval time.Duration

	// mu guards reservedMB, the memory (in MB) committed to in-flight Create
	// calls that a live host query doesn't reflect yet. See reserveMemory.
	mu         sync.Mutex
	reservedMB int64
}
```

Add the constant near the top of the file, alongside the existing `const` block (currently lines 50-57):

```go
// defaultHostReserveMB is headroom subtracted from the host's free memory
// before it's offered to a new Create request, protecting the host OS and
// other processes. Not currently user-configurable — see #173's design
// spec, "Known gap surfaced by this work."
const defaultHostReserveMB = 512
```

Add the new methods right after `checkHostHealth` (currently ends at line 413, right before `// waitForTerminalVMState polls...`):

```go
// CapacityError indicates the host does not currently have enough free
// memory to satisfy a Create request. AvailableMemoryMB is already net of
// defaultHostReserveMB and any other in-flight reservations.
type CapacityError struct {
	RequestedMemoryMB int64
	AvailableMemoryMB int64
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf(
		"hyperv: insufficient host memory: requested %d MB, %d MB available",
		e.RequestedMemoryMB, e.AvailableMemoryMB,
	)
}

// queryFreeMemoryMB returns the host's current free physical memory in
// megabytes. Win32_OperatingSystem.FreePhysicalMemory is reported in
// kilobytes, hence the /1024.
func (d *Driver) queryFreeMemoryMB(ctx context.Context) (int64, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
(Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory
`)
	if err != nil {
		return 0, fmt.Errorf("hyperv query free memory: %w", err)
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hyperv parse free memory %q: %w", out, err)
	}
	return kb / 1024, nil
}

// Availability implements providersdk.AvailabilityReporter.
func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	freeMB, err := d.queryFreeMemoryMB(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	reserved := d.reservedMB
	d.mu.Unlock()

	avail := freeMB - defaultHostReserveMB - reserved
	if avail < 0 {
		avail = 0
	}
	return &providersdk.ResourceAvailability{MemoryMB: avail}, nil
}

// reserveMemory atomically checks and commits requestedMB of host memory
// against live availability, returning a release closure that must be
// called exactly once (success or failure) to give the memory back. The
// mutex is held across the live PowerShell query itself, not just the
// accounting — the only construct with zero TOCTOU gap, and Create already
// makes several more sequential PowerShell round-trips regardless, so this
// isn't a new bottleneck relative to today.
func (d *Driver) reserveMemory(ctx context.Context, requestedMB int64) (release func(), err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	freeMB, err := d.queryFreeMemoryMB(ctx)
	if err != nil {
		return nil, err
	}

	available := freeMB - defaultHostReserveMB - d.reservedMB
	if available < requestedMB {
		return nil, &CapacityError{RequestedMemoryMB: requestedMB, AvailableMemoryMB: available}
	}

	d.reservedMB += requestedMB
	return func() {
		d.mu.Lock()
		d.reservedMB -= requestedMB
		d.mu.Unlock()
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestDriver_Availability|TestDriver_ReserveMemory' -v`
Expected: PASS, all cases.

- [ ] **Step 5: Run the full package test suite to confirm nothing else broke**

Run: `go test ./pkg/providersdk/...`
Expected: PASS (Task 3 hasn't wired `reserveMemory` into `Create` yet, so existing `Create` tests are untouched by this task).

- [ ] **Step 6: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#173): add hyperv.Driver.Availability and reserveMemory"
```

---

### Task 3: Wire `reserveMemory` into `Create`; fix existing Create tests

**Files:**
- Modify: `pkg/providersdk/providers/hyperv/driver.go`
- Modify: `pkg/providersdk/providers/hyperv/driver_test.go`

**Interfaces:**
- Consumes: `d.reserveMemory` (Task 2).

Adding a real memory-query call to every `Create` breaks five existing tests whose mock unconditionally returns a fixed string for every `psExec` call — the free-memory query script now needs a *numeric* response, not `fakeGUID` or a state string. This task's failing-test step is "run the existing suite and watch it break," then each broken test gets its mock updated to discriminate by script content, then a new capacity-rejection test is added.

- [ ] **Step 1: Wire `reserveMemory` into `Create`**

In `pkg/providersdk/providers/hyperv/driver.go`, `Create` currently reads (starting at line 93):

```go
	if err := d.checkHostHealth(ctx); err != nil {
		return nil, fmt.Errorf("hyperv host health check failed, refusing to provision: %w", err)
	}

	vhdDir := cc.VHDDir
```

Change it to:

```go
	if err := d.checkHostHealth(ctx); err != nil {
		return nil, fmt.Errorf("hyperv host health check failed, refusing to provision: %w", err)
	}

	release, err := d.reserveMemory(ctx, int64(cc.MemoryMB))
	if err != nil {
		return nil, err
	}
	defer release()

	vhdDir := cc.VHDDir
```

(`cc.MemoryMB`'s default of 2048 is already applied earlier in `Create`'s defaults block, before `checkHostHealth` — no reordering needed. `CapacityError.Error()` is already a complete, clear message, so it's returned unwrapped rather than double-prefixed like the health-check error above.)

- [ ] **Step 2: Run the full test suite to see what breaks**

Run: `go test ./pkg/providersdk/providers/hyperv/... -v 2>&1 | grep -E "^(--- FAIL|FAIL)"`
Expected: `TestDriver_Create_HappyPath`, `TestDriver_Create_Defaults`, `TestDriver_Create_CleanupOnFailure`, and `TestDriver_Create_LinuxDefaults` FAIL. `TestDriver_Create_MissingTemplateVHD`, `TestDriver_Create_HealthCheckFailure`, and `TestDriver_Create_RejectsGuestPassword` still PASS (they fail before `checkHostHealth`/`reserveMemory` is ever reached — decode-time errors and a health-check failure both return before the new call).

- [ ] **Step 3: Fix `TestDriver_Create_HappyPath`**

Replace:

```go
func TestDriver_Create_HappyPath(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callCount++
		return fakeGUID + "\n", nil
	})
```

with:

```go
func TestDriver_Create_HappyPath(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		if strings.Contains(script, hyperVFreeMemoryScript) {
			return "16777216\n", nil // 16 GB in KB, comfortably above any test's request
		}
		return fakeGUID + "\n", nil
	})
```

(the rest of the test body is unchanged).

- [ ] **Step 4: Fix `TestDriver_Create_Defaults`**

Replace:

```go
func TestDriver_Create_Defaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		capturedScript = script
		return fakeGUID + "\n", nil
	})
```

with:

```go
func TestDriver_Create_Defaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, hyperVFreeMemoryScript) {
			return "16777216\n", nil
		}
		if strings.Contains(script, "New-VM") {
			capturedScript = script
		}
		return fakeGUID + "\n", nil
	})
```

(the rest of the test body is unchanged).

- [ ] **Step 5: Fix `TestDriver_Create_LinuxDefaults`**

Replace:

```go
func TestDriver_Create_LinuxDefaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		capturedScript = script
		return fakeGUID + "\n", nil
	})
```

with:

```go
func TestDriver_Create_LinuxDefaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, hyperVFreeMemoryScript) {
			return "16777216\n", nil
		}
		if strings.Contains(script, "New-VM") {
			capturedScript = script
		}
		return fakeGUID + "\n", nil
	})
```

(the rest of the test body is unchanged).

- [ ] **Step 6: Fix `TestDriver_Create_CleanupOnFailure`**

Replace:

```go
func TestDriver_Create_CleanupOnFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch callCount {
		case 1:
			// Host health check succeeds.
			return "OK\n", nil
		case 2:
			// Main create script fails.
			return "", fmt.Errorf("New-VHD failed")
		default:
			// Cleanup script succeeds.
			return "", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	if err == nil {
		t.Fatal("expected error when create script fails")
	}
	if callCount < 3 {
		t.Errorf("expected health check + create + cleanup calls, callCount = %d", callCount)
	}
}
```

with:

```go
func TestDriver_Create_CleanupOnFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVFreeMemoryScript):
			return "16777216\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			// Cleanup script succeeds.
			return "", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	if err == nil {
		t.Fatal("expected error when create script fails")
	}
	if callCount < 4 {
		t.Errorf("expected health check + memory query + create + cleanup calls, callCount = %d", callCount)
	}
}
```

- [ ] **Step 7: Add the capacity-rejection test**

Add near the other `Create` tests (after `TestDriver_Create_HealthCheckFailure`, currently ending at line 137):

```go
func TestDriver_Create_InsufficientMemoryRejectedBeforeNewVM(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVFreeMemoryScript):
			return "1048576\n", nil // 1 GB in KB = 1024 MB free, minus 512 reserve = 512 MB available
		case strings.Contains(script, "New-VM"):
			t.Fatal("New-VM must not run when capacity is insufficient")
			return "", nil
		}
		return "", fmt.Errorf("unexpected script: %s", script)
	})

	// Default MemoryMB is 2048; 512 MB available can't satisfy it.
	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	var capErr *CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected *CapacityError, got %#v", err)
	}
	if capErr.RequestedMemoryMB != 2048 {
		t.Errorf("RequestedMemoryMB = %d, want 2048", capErr.RequestedMemoryMB)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (health check + memory query only)", callCount)
	}
}
```

- [ ] **Step 8: Run the full package test suite**

Run: `go test ./pkg/providersdk/providers/hyperv/... -v`
Expected: PASS, all tests including the four fixed ones and the new rejection test.

- [ ] **Step 9: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver.go pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "feat(#173): preflight host memory before hyperv VM creation"
```

---

### Task 4: Concurrency and reservation-lifecycle tests

Verifies the actual atomicity guarantee under real concurrent goroutines, and that a reservation never leaks — separate from Task 2's basic sequential-call tests because it's a materially different (slower, harder-to-get-right) kind of test worth its own review gate.

**Files:**
- Test: `pkg/providersdk/providers/hyperv/driver_test.go`

- [ ] **Step 1: Write the failing concurrency test**

Add after the Task 2 `reserveMemory`/`Availability` tests:

```go
func TestDriver_ReserveMemory_ConcurrentCallsLimitToCapacity(t *testing.T) {
	// 16 GB free, minus the 512 MB reserve, leaves 16384-512 = 15872 MB
	// available. Each caller requests 4096 MB, so exactly 3 of 8 concurrent
	// callers can fit (3*4096=12288 <= 15872 < 4*4096=16384).
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "16777216\n", nil
	})

	const callers = 8
	const requestMB = 4096
	var wg sync.WaitGroup
	results := make(chan error, callers)
	releases := make(chan func(), callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := d.reserveMemory(context.Background(), requestMB)
			results <- err
			if err == nil {
				releases <- release
			}
		}()
	}
	wg.Wait()
	close(results)
	close(releases)

	succeeded := 0
	for err := range results {
		var capErr *CapacityError
		switch {
		case err == nil:
			succeeded++
		case errors.As(err, &capErr):
			// Expected for callers that lost the race.
		default:
			t.Fatalf("unexpected error type: %v", err)
		}
	}
	if succeeded != 3 {
		t.Errorf("succeeded = %d, want 3", succeeded)
	}
	if d.reservedMB != int64(succeeded)*requestMB {
		t.Errorf("reservedMB = %d, want %d", d.reservedMB, int64(succeeded)*requestMB)
	}

	for release := range releases {
		release()
	}
	if d.reservedMB != 0 {
		t.Errorf("reservedMB after releasing all = %d, want 0", d.reservedMB)
	}
}

func TestDriver_Create_ReservationReleasedAfterFailureAllowsNextCreate(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVFreeMemoryScript):
			return "16777216\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			return "", nil // cleanup
		}
	})

	// First Create fails after reserving memory.
	if _, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`}); err == nil {
		t.Fatal("expected the first Create to fail")
	}
	if d.reservedMB != 0 {
		t.Fatalf("reservedMB after a failed Create = %d, want 0 (must not leak)", d.reservedMB)
	}

	// A second Create must still be able to reserve the same memory.
	release, err := d.reserveMemory(context.Background(), 2048)
	if err != nil {
		t.Fatalf("second reservation unexpectedly failed: %v", err)
	}
	release()
}
```

`driver_test.go`'s import block does not currently have `"sync"` — add it.

- [ ] **Step 2: Run tests to verify they fail (or, for the concurrency test, confirm it's exercising real concurrency)**

Run: `go test ./pkg/providersdk/providers/hyperv/... -run 'TestDriver_ReserveMemory_ConcurrentCallsLimitToCapacity|TestDriver_Create_ReservationReleasedAfterFailureAllowsNextCreate' -race -v`
Expected: both should already PASS if Tasks 2-3 were implemented correctly — this task's value is the `-race` flag catching any lock-scope mistake, not new production code. If either fails, the bug is in Task 2/3's `reserveMemory`/`Create` wiring, not in these tests — go back and fix it there.

- [ ] **Step 3: Run with `-race` explicitly as the permanent verification for this behavior**

Run: `go test ./pkg/providersdk/providers/hyperv/... -race -v`
Expected: PASS, no data race reported, full package.

- [ ] **Step 4: Commit**

```bash
git add pkg/providersdk/providers/hyperv/driver_test.go
git commit -m "test(#173): verify reserveMemory atomicity under concurrency and no leak on failure"
```

---

### Task 5: `devfactory` availability stub

**Files:**
- Modify: `pkg/providersdk/providers/devfactory/config.go`
- Modify: `pkg/providersdk/providers/devfactory/driver.go`
- Test: `pkg/providersdk/providers/devfactory/driver_test.go`

**Interfaces:**
- Consumes: `providersdk.ResourceAvailability`, `providersdk.AvailabilityReporter` (Task 1).
- Produces: `(d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error)` on `devfactory.Driver`.

- [ ] **Step 1: Write the failing test**

Add to `pkg/providersdk/providers/devfactory/driver_test.go`:

```go
func TestDriver_Availability_ReturnsConfiguredValue(t *testing.T) {
	d := New(&Config{AvailableMemoryMB: 4096, DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != 4096 {
		t.Errorf("MemoryMB = %d, want 4096", avail.MemoryMB)
	}
}

func TestDriver_Availability_ZeroMeansUnlimited(t *testing.T) {
	d := New(&Config{DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != math.MaxInt64 {
		t.Errorf("MemoryMB = %d, want math.MaxInt64", avail.MemoryMB)
	}
}

// --- providersdk.AvailabilityReporter interface compliance ---

var _ providersdk.AvailabilityReporter = (*Driver)(nil)
```

`devfactory/driver_test.go`'s import block currently has `"context"`, `"encoding/json"`, `"os"`, `"path/filepath"`, `"testing"`, `"time"` — add `"math"` and `"github.com/Geogboe/boxy/pkg/providersdk"`, both of which it's missing today.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/providersdk/providers/devfactory/... -run TestDriver_Availability -v`
Expected: FAIL — `Config.AvailableMemoryMB` and `Driver.Availability` are undefined.

- [ ] **Step 3: Add the config field**

In `pkg/providersdk/providers/devfactory/config.go`, add to `Config` (currently lines 44-70), after `Labels`:

```go
	// AvailableMemoryMB is the value Availability() reports as free memory.
	// Zero (the default) means unlimited — matches the zero-value-friendly
	// pattern the other fields here already use (e.g. FailCreate's zero
	// value means "don't fail").
	AvailableMemoryMB int64 `yaml:"available_memory_mb" json:"available_memory_mb"`
```

- [ ] **Step 4: Implement `Availability`**

In `pkg/providersdk/providers/devfactory/driver.go`, add `"math"` to the import block, and add the method near `Create` (after the `Type` method, before `Create`):

```go
// Availability implements providersdk.AvailabilityReporter. It returns the
// static value configured via Config.AvailableMemoryMB — no per-call
// memory-request modeling; devfactory's Create doesn't decode one today,
// and adding it purely to enforce here would be scope creep unrelated to
// what this stub is for (letting other code exercise the
// AvailabilityReporter interface without needing a real Hyper-V host).
func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	mb := d.cfg.AvailableMemoryMB
	if mb == 0 {
		mb = math.MaxInt64
	}
	return &providersdk.ResourceAvailability{MemoryMB: mb}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/providersdk/providers/devfactory/... -v`
Expected: PASS, full package.

- [ ] **Step 6: Commit**

```bash
git add pkg/providersdk/providers/devfactory/config.go pkg/providersdk/providers/devfactory/driver.go pkg/providersdk/providers/devfactory/driver_test.go
git commit -m "feat(#173): devfactory implements AvailabilityReporter as a config-driven stub"
```

---

### Task 6: Docs, verification, follow-up issues, PR

**Files:**
- Modify: `docs/adr/0004-hyperv-teardown-guard-and-provisioning-backoff.md`

- [ ] **Step 1: Add a section to ADR-0004**

ADR-0004 already documents `hyperv.Driver`'s provisioning-side decisions (teardown guard, provisioning backoff). Add a new `###` subsection under `## Decision`, after the existing "Provisioning backoff" section (currently ends at line ~59, before `## Consequences`):

```markdown
### Memory preflight and reservation (#173)

`Create` queries the host's live free memory
(`Get-CimInstance Win32_OperatingSystem`) before running its `New-VHD`/
`New-VM`/`Start-VM` script, rejecting a request that can't fit with a typed
`CapacityError` instead of letting `Start-VM` fail with a raw
`0x8007000E`. An in-process, mutex-guarded reservation counter
(`Driver.reservedMB`) makes this atomic across concurrent `Create` calls on
the same agent process — the mutex is held across the live PowerShell query
itself, not just the accounting, trading a small amount of parallelism for
a genuinely zero-TOCTOU-gap guarantee. `defaultHostReserveMB` (512 MB,
unexported) is reserved for the host OS and other processes; it is not
currently user-configurable, because `boxy agent serve` has no
provider-config plumbing at all today (a pre-existing gap affecting every
provider, not just Hyper-V — see the design spec's "Known gap").

Full design: `docs/superpowers/specs/2026-08-12-hyperv-memory-preflight-design.md`.
```

- [ ] **Step 2: Commit the doc**

```bash
git add docs/adr/0004-hyperv-teardown-guard-and-provisioning-backoff.md
git commit -m "docs(#173): record memory preflight decision in ADR-0004"
```

- [ ] **Step 3: Run the full verification suite**

Run, in order: `task test`, `task lint`, `task generate`, `task build`.
Expected: all pass clean, `task generate` produces no diff (`git status --short` shows nothing new after it runs).

- [ ] **Step 4: Reconcile with `main`**

Check whether #167 (PR #175) has merged: `gh pr view 175 --json state`. If `MERGED`, rebase this branch onto `main` so the #173 PR's diff doesn't include #167's already-merged commit:

```bash
git fetch origin
git rebase origin/main
```

Resolve any conflicts (unlikely — #167 touched `internal/agentserver`, `internal/cli/agent_serve.go`/`serve.go`, and `pkg/agentsdk`; this plan touches `pkg/providersdk` and `docs/`, disjoint file sets). Re-run Step 3's verification suite after rebasing.

If #175 hasn't merged yet, skip this step for now — re-run it before actually opening the PR in Step 6.

- [ ] **Step 5: File the four follow-up issues**

Using `gh issue create`, file each of the following (title, then body drawn directly from the spec's "Follow-ups" section and this conversation's design discussion, so the context isn't lost):

1. **Title:** `Wire hyperv AvailabilityReporter data up to the server via Heartbeat`
   **Body:** Summarize: `providersdk.AvailabilityReporter` (added in #173) is agent-local only. Extend `Heartbeat` (proto `boxyagent.v1`) to carry `ResourceAvailability`, store the latest snapshot per agent on `pool.AgentRegistry`. No scheduling logic — just plumbing, queryable by a future consumer. Link to the design spec and to #173.

2. **Title:** `pool.Manager: use reported agent availability when picking an agent`
   **Body:** Summarize: once availability is on `AgentRegistry` (see the Heartbeat-wiring follow-up), `pool.Manager`/`AgentProvisioner`'s routing could prefer/filter agents by reported headroom for multi-agent/multi-host pools. Note this likely folds into #124's job-scheduler design discussion rather than standing alone — link both #124 and #173.

3. **Title:** `boxy agent serve has no provider-config plumbing`
   **Body:** Summarize: `buildAgentDrivers` in `internal/cli/agent_serve.go` always instantiates every driver with a zero-value `Config` — there's no flag or file for provider-level settings on the standalone remote-agent path, unlike `boxy serve`'s embedded-agent `buildDrivers`, which decodes real config from `boxy.yaml`. Affects every provider with config fields (e.g. `docker.Config.Host`, and blocks making `hyperv`'s `defaultHostReserveMB` user-configurable). Link #173.

4. **Title:** `Bring devfactory to full parity as a general-purpose provider stub`
   **Body:** Summarize: #173 gave `devfactory` a minimal `AvailabilityReporter` stub. Auditing every optional interface every real driver (`hyperv`, `docker`) implements and mirroring all of them in `devfactory` would make it a genuinely complete reference/testing double — a separate, unbounded project, not something to fold into a single feature PR. Link #173.

Record the resulting issue numbers (needed for the PR body in Step 6).

- [ ] **Step 6: Open the PR**

```bash
git push -u origin feat/173-hyperv-memory-preflight
gh pr create --title "feat(#173): preflight Hyper-V host memory before VM provisioning" --body "$(cat <<'EOF'
## Summary

`hyperv.Driver.Create` now checks the host's live free memory before running its `New-VHD`/`New-VM`/`Start-VM` script, and atomically reserves it across concurrent `Create` calls on the same agent process — replacing the raw `0x8007000E` PowerShell crash with a typed, immediate `CapacityError`.

## Scope decision

#173's acceptance criteria bundled several separable things: preflight + reservation (this PR), wire-level reporting to the server, pool.Manager scheduling by availability, `boxy status`/`boxy agent list` surfacing, and the "stale inventory" observation from the original issue. This PR covers preflight + reservation only; the rest are filed as follow-ups: #<N1>, #<N2>, #<N3>, #<N4>.

Full design: `docs/superpowers/specs/2026-08-12-hyperv-memory-preflight-design.md`.

## Testing

- New unit tests for `Availability`, `reserveMemory` (sufficient/insufficient capacity, release lifecycle), a concurrency test under `-race` proving atomicity, and a failed-Create-doesn't-leak-a-reservation test.
- Four existing `Create` tests updated to route their fake `psExec` by script content, since a real memory-query call now runs on every `Create`.
- `devfactory` gets a minimal, config-driven `AvailabilityReporter` stub.
- `task test`, `task lint`, `task generate`, `task build` all pass.

Fixes #173.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Replace `#<N1>`-`#<N4>` with the actual issue numbers from Step 5 before running.
