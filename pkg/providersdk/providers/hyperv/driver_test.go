package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

const fakeGUID = "12345678-1234-1234-1234-123456789abc"

func int64Ptr(v int64) *int64 { return &v }

// mockDriver builds a Driver with psExec and optional guestExecFactory injected.
func mockDriver(psExecFn func(ctx context.Context, script string) (string, error)) *Driver {
	return &Driver{psExec: psExecFn}
}

func TestNew_HostReserveDefaultsAndValidation(t *testing.T) {
	d, err := New(&Config{})
	if err != nil {
		t.Fatalf("New(default): %v", err)
	}
	if got := d.hostReserve(); got != DefaultHostReserveMB {
		t.Fatalf("default host reserve = %d, want %d", got, DefaultHostReserveMB)
	}
	zero, err := New(&Config{HostReserveMB: int64Ptr(0)})
	if err != nil {
		t.Fatalf("New(zero): %v", err)
	}
	if got := zero.hostReserve(); got != 0 {
		t.Fatalf("zero host reserve = %d, want 0", got)
	}
	custom, err := New(&Config{HostReserveMB: int64Ptr(1024)})
	if err != nil {
		t.Fatalf("New(custom): %v", err)
	}
	if got := custom.hostReserve(); got != 1024 {
		t.Fatalf("custom host reserve = %d, want 1024", got)
	}
	if _, err := New(&Config{HostReserveMB: int64Ptr(-1)}); err == nil {
		t.Fatal("New(negative) error = nil")
	}
}

// --- Create ---

func TestDriver_Create_HappyPath(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		if strings.Contains(script, hyperVAvailableMemoryScript) {
			return "16384\n", nil // 16 GB, comfortably above any test's request
		}
		return fakeGUID + "\n", nil
	})

	res, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		VHDDir:      `C:\VMs`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID != fakeGUID {
		t.Errorf("ID = %q, want %q", res.ID, fakeGUID)
	}
	if res.ConnectionInfo["guest_os"] != "windows" {
		t.Errorf("guest_os = %q, want windows", res.ConnectionInfo["guest_os"])
	}
	if callCount == 0 {
		t.Error("psExec was never called")
	}
}

func TestDriver_Create_MissingTemplateVHD(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		t.Fatal("psExec should not be called when config is invalid")
		return "", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{})
	if err == nil {
		t.Fatal("expected error for missing TemplateVHD")
	}
	if !strings.Contains(err.Error(), "template_vhd") {
		t.Errorf("error %q should mention template_vhd", err.Error())
	}
}

func TestDriver_Create_Defaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, hyperVAvailableMemoryScript) {
			return "16384\n", nil
		}
		if strings.Contains(script, "New-VM") {
			capturedScript = script
		}
		return fakeGUID + "\n", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify defaults appear in the script.
	if !strings.Contains(capturedScript, "-Generation 2") {
		t.Errorf("expected default generation 2 in script: %s", capturedScript)
	}
	if !strings.Contains(capturedScript, "-ProcessorCount 2") {
		t.Errorf("expected default cpu_count 2 in script: %s", capturedScript)
	}
	if strings.Contains(capturedScript, "boxy_guest_password=") {
		t.Errorf("expected raw guest password to be absent from notes: %s", capturedScript)
	}
}

func TestDriver_Create_CleanupOnFailure(t *testing.T) {
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
			// Cleanup script's existence check: empty output = confirmed gone.
			// deleteBestEffort makes exactly one round-trip in this case (no
			// retry needed since the VM is confirmed gone on the first attempt).
			return "\n", nil
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

func TestDriver_DeleteBestEffort_RetriesAndResolvesGUIDOnPersistentFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		// Every attempt's existence check reports the VM still present.
		return fakeGUID + "\n", nil
	})
	d.deleteBestEffortInterval = time.Millisecond // avoid real sleeps in the test

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

func TestDriver_DeleteBestEffort_SucceedsAfterRetry(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		if callCount == 1 {
			return fakeGUID + "\n", nil // still present on the first attempt
		}
		return "\n", nil // confirmed gone on the second attempt
	})
	d.deleteBestEffortInterval = time.Millisecond

	guid, err := d.deleteBestEffort(context.Background(), "boxy-abc123", `C:\VMs\boxy-abc123.vhdx`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if guid != "" {
		t.Errorf("guid = %q, want empty once confirmed gone, even though an earlier attempt saw it present", guid)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want exactly 2 (one retry before confirmation)", callCount)
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
	d.deleteBestEffortInterval = time.Millisecond

	_, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	var orphanErr *providersdk.OrphanedResourceError
	if !errors.As(err, &orphanErr) {
		t.Fatalf("expected *providersdk.OrphanedResourceError, got %#v", err)
	}
	if orphanErr.ID != fakeGUID {
		t.Errorf("ID = %q, want %q", orphanErr.ID, fakeGUID)
	}
}

func TestDriver_Create_CleanupFailureSurfacedWhenNoGUIDResolved(t *testing.T) {
	// Every deleteBestEffort attempt's PowerShell call itself fails (e.g.
	// host unreachable), so guid never resolves — deleteBestEffort returns
	// ("", lastErr). createFailure must not drop lastErr silently in this
	// case: there's no GUID to quarantine under, so the cleanup failure is
	// the only signal an operator has that a VM might be orphaned.
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "New-VM"):
			return "", fmt.Errorf("New-VHD failed")
		default:
			// deleteBestEffort: every attempt's PS call fails outright.
			return "", fmt.Errorf("host unreachable")
		}
	})
	d.deleteBestEffortInterval = time.Millisecond

	_, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`})
	if err == nil {
		t.Fatal("expected an error")
	}
	var orphanErr *providersdk.OrphanedResourceError
	if errors.As(err, &orphanErr) {
		t.Fatalf("expected a plain error, not *OrphanedResourceError, when no GUID could be resolved: %#v", orphanErr)
	}
	if !strings.Contains(err.Error(), "New-VHD failed") {
		t.Errorf("error = %q, want it to contain the original cause", err.Error())
	}
	if !strings.Contains(err.Error(), "host unreachable") {
		t.Errorf("error = %q, want it to also contain the cleanup failure instead of dropping it", err.Error())
	}
}

func TestDriver_CreateFailure_CleanupDetachedFromCancelledCallerContext(t *testing.T) {
	// psExec deliberately checks ctx itself (unlike most mocks here) to
	// simulate a real d.ps call failing on an already-cancelled context —
	// reproduces the exact scenario where deleteBestEffort's first
	// PowerShell call fails immediately with the caller's ctx error, before
	// any existence check ever runs.
	d := mockDriver(func(ctx context.Context, _ string) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fakeGUID + "\n", nil // existence check: still present
	})
	d.deleteBestEffortInterval = time.Millisecond

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before createFailure is even called

	err := d.createFailure(cancelledCtx, "boxy-abc123", `C:\VMs\boxy-abc123.vhdx`, fmt.Errorf("original cause"))
	var orphanErr *providersdk.OrphanedResourceError
	if !errors.As(err, &orphanErr) {
		t.Fatalf("expected *providersdk.OrphanedResourceError despite a cancelled caller ctx (cleanup must run detached), got %#v", err)
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

func TestDriver_Create_HealthCheckFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callCount++
		return "", fmt.Errorf("Get-VMHost failed: VMMS unavailable")
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	if err == nil {
		t.Fatal("expected error when host health check fails")
	}
	if !strings.Contains(err.Error(), "health check failed") {
		t.Errorf("error %q should mention health check", err.Error())
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (should not attempt create after failed health check)", callCount)
	}
}

func TestDriver_Create_InsufficientMemoryRejectedBeforeNewVM(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		switch {
		case strings.Contains(script, "Get-VMHost"):
			return "OK\n", nil
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "1024\n", nil // 1 GB free, minus 512 reserve = 512 MB available
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

func TestDriver_Create_NegativeMemoryRejected(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		t.Fatal("psExec should not be called when memory_mb is negative")
		return "", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
		MemoryMB:    -1024,
	})
	if err == nil {
		t.Fatal("expected error for negative memory_mb")
	}
	if !strings.Contains(err.Error(), "memory_mb") {
		t.Fatalf("expected error to mention memory_mb, got %v", err)
	}
}

func TestDriver_Create_LinuxDefaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, hyperVAvailableMemoryScript) {
			return "16384\n", nil
		}
		if strings.Contains(script, "New-VM") {
			capturedScript = script
		}
		return fakeGUID + "\n", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
		GuestOS:     "linux",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedScript, "boxy_guest_user=admin") {
		t.Errorf("expected default linux guest user 'admin' in notes: %s", capturedScript)
	}
}

func TestDriver_Create_RejectsGuestPassword(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		t.Fatal("psExec should not be called when guest_password is configured")
		return "", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD:   `C:\t.vhdx`,
		GuestPassword: "${BOXY_TEST_PASSWORD}",
	})
	if err == nil {
		t.Fatal("expected error for deprecated guest_password")
	}
	if !strings.Contains(err.Error(), "guest_password_ref") {
		t.Fatalf("expected error to mention guest_password_ref, got %v", err)
	}
}

// --- Availability / reserveMemory ---

// hyperVAvailableMemoryScript is the fragment that appears in the live
// available-memory query script; tests key their psExec mock off it. Mock
// return values below are plain MB strings — the real query
// (queryAvailableMemoryMB) now reads Win32_PerfFormattedData_PerfOS_Memory's
// AvailableMBytes, which unlike the old FreePhysicalMemory is already in MB.
const hyperVAvailableMemoryScript = "AvailableMBytes"

func TestDriver_Availability_NetsOutReserveAndReservations(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, hyperVAvailableMemoryScript) {
			t.Fatalf("unexpected script: %s", script)
		}
		return "16384\n", nil // 16 GB
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

func TestDriver_Availability_UsesConfiguredHostReserve(t *testing.T) {
	d, err := New(&Config{HostReserveMB: int64Ptr(1024)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.psExec = func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, hyperVAvailableMemoryScript) {
			t.Fatalf("unexpected script: %s", script)
		}
		return "4096\n", nil
	}
	d.reservedMB = 512
	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if got, want := avail.MemoryMB, int64(4096-1024-512); got != want {
		t.Fatalf("MemoryMB = %d, want %d", got, want)
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
		return "16384\n", nil // 16 GB
	})
	d.reservationGraceInterval = 50 * time.Millisecond

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
	// Reads after release() race the grace-period goroutine's mutex-guarded
	// write (driver.go's reserveMemory), so they must take the same lock —
	// see TestDriver_ReserveMemory_ReleaseHasGracePeriod for the pattern.
	d.mu.Lock()
	immediatelyAfter := d.reservedMB
	d.mu.Unlock()
	if immediatelyAfter != 2048 {
		t.Errorf("reservedMB immediately after release() = %d, want 2048 (grace period still holding it)", immediatelyAfter)
	}
	time.Sleep(150 * time.Millisecond)
	d.mu.Lock()
	afterGracePeriod := d.reservedMB
	d.mu.Unlock()
	if afterGracePeriod != 0 {
		t.Errorf("reservedMB after the grace period = %d, want 0", afterGracePeriod)
	}
}

func TestDriver_ReserveMemory_ReleaseHasGracePeriod(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		return "1024\n", nil // 1 GB available
	})
	d.reservationGraceInterval = 50 * time.Millisecond

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

	time.Sleep(150 * time.Millisecond)
	d.mu.Lock()
	afterGracePeriod := d.reservedMB
	d.mu.Unlock()
	if afterGracePeriod != 0 {
		t.Fatalf("reservedMB after the grace period = %d, want 0", afterGracePeriod)
	}
}

func TestDriver_ReserveMemory_InsufficientCapacityReturnsCapacityError(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "1024\n", nil // 1 GB free
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

func TestDriver_ReserveMemory_QueryTimeoutBoundsMutexHold(t *testing.T) {
	unblock := make(chan struct{})
	d := mockDriver(func(ctx context.Context, _ string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-unblock:
			return "16384\n", nil
		}
	})
	d.memoryQueryTimeout = 10 * time.Millisecond

	start := time.Now()
	_, err := d.reserveMemory(context.Background(), 2048)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected reserveMemory to fail when the query times out")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("reserveMemory took %v, want it bounded by memoryQueryTimeout", elapsed)
	}

	// mu must be released despite the timeout: unblock the mock and confirm
	// a second call succeeds promptly instead of deadlocking on mu.
	close(unblock)
	done := make(chan struct{})
	go func() {
		_, _ = d.reserveMemory(context.Background(), 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reserveMemory deadlocked after a timed-out query")
	}
}

func TestDriver_ReserveMemory_ConcurrentCallsLimitToCapacity(t *testing.T) {
	// 16 GB free, minus the 512 MB reserve, leaves 16384-512 = 15872 MB
	// available. Each caller requests 4096 MB, so exactly 3 of 8 concurrent
	// callers can fit (3*4096=12288 <= 15872 < 4*4096=16384).
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "16384\n", nil
	})
	d.reservationGraceInterval = 50 * time.Millisecond

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
	time.Sleep(150 * time.Millisecond)
	// Races the grace-period goroutines' mutex-guarded writes; see the note
	// in TestDriver_ReserveMemory_SufficientCapacitySucceeds.
	d.mu.Lock()
	afterGracePeriod := d.reservedMB
	d.mu.Unlock()
	if afterGracePeriod != 0 {
		t.Errorf("reservedMB after releasing all and the grace period = %d, want %d", afterGracePeriod, 0)
	}
}

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
	d.reservationGraceInterval = 50 * time.Millisecond

	if _, err := d.Create(context.Background(), &CreateConfig{TemplateVHD: `C:\t.vhdx`}); err == nil {
		t.Fatal("expected the first Create to fail")
	}
	// Races the grace-period goroutine's mutex-guarded write; see the note
	// in TestDriver_ReserveMemory_SufficientCapacitySucceeds.
	d.mu.Lock()
	immediatelyAfter := d.reservedMB
	d.mu.Unlock()
	if immediatelyAfter == 0 {
		t.Fatal("expected reservedMB to still be held immediately after a failed Create (grace period)")
	}

	time.Sleep(150 * time.Millisecond)
	d.mu.Lock()
	afterGracePeriod := d.reservedMB
	d.mu.Unlock()
	if afterGracePeriod != 0 {
		t.Fatalf("reservedMB after the grace period = %d, want 0 (must not leak permanently)", afterGracePeriod)
	}

	// A second reservation must succeed once the grace period has elapsed.
	release, err := d.reserveMemory(context.Background(), 2048)
	if err != nil {
		t.Fatalf("second reservation unexpectedly failed: %v", err)
	}
	release()
}

// --- providersdk.AvailabilityReporter interface compliance ---

var _ providersdk.AvailabilityReporter = (*Driver)(nil)

// --- Read ---

func TestDriver_Read_StateMapping(t *testing.T) {
	cases := []struct {
		psOut string
		want  string
	}{
		{"Running", "running"},
		{"Off", "stopped"},
		{"Saved", "saved"},
		{"Paused", "paused"},
		{"Starting", "starting"},
	}

	for _, tc := range cases {
		d := mockDriver(func(_ context.Context, _ string) (string, error) {
			return tc.psOut + "\n", nil
		})
		status, err := d.Read(context.Background(), fakeGUID)
		if err != nil {
			t.Errorf("Read(%q): unexpected error: %v", tc.psOut, err)
			continue
		}
		if status.State != tc.want {
			t.Errorf("Read(%q): state = %q, want %q", tc.psOut, status.State, tc.want)
		}
	}
}

func TestDriver_Read_Error(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("vm not found")
	})
	_, err := d.Read(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

// --- Update ---

func TestDriver_Update_UnsupportedOp(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "", nil
	})
	_, err := d.Update(context.Background(), fakeGUID, struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported operation")
	}
}

func TestDriver_Update_ExecOp_EmptyCommand(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "boxy_guest_os=windows;boxy_guest_user=admin;boxy_guest_password_ref=env:BOX_PASSWORD\n", nil
	})
	_, err := d.Update(context.Background(), fakeGUID, &ExecOp{Command: []string{}})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestDriver_Update_ExecOp_Windows(t *testing.T) {
	var guestExecCalled bool
	d := &Driver{
		psExec: func(_ context.Context, _ string) (string, error) {
			return "boxy_guest_os=windows;boxy_guest_user=Administrator;boxy_guest_password_ref=env:BOX_PASSWORD\n", nil
		},
		resolveSecret: func(_ context.Context, ref providersdk.SecretRef) (string, error) {
			if ref != "env:BOX_PASSWORD" {
				t.Fatalf("unexpected secret ref %q", ref)
			}
			return "${BOXY_TEST_PASSWORD}", nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			guestExecCalled = true
			if guestOS != "windows" {
				t.Errorf("guestOS = %q, want windows", guestOS)
			}
			if guestPassword != "${BOXY_TEST_PASSWORD}" {
				t.Errorf("guestPassword = %q, want ${BOXY_TEST_PASSWORD}", guestPassword)
			}
			return &fakeGuestExec{stdout: "output", exitCode: 0}
		},
	}

	result, err := d.Update(context.Background(), fakeGUID, &ExecOp{
		Command:         []string{"echo", "hello"},
		GuestCredential: passwordCredential("Administrator", "${BOXY_TEST_PASSWORD}"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !guestExecCalled {
		t.Error("guestExecFactory was not called")
	}
	if result.Outputs["stdout"] != "output" {
		t.Errorf("stdout = %q, want %q", result.Outputs["stdout"], "output")
	}
}

func TestDriver_Update_ExecOp_Linux(t *testing.T) {
	callNum := 0
	d := &Driver{
		psExec: func(_ context.Context, script string) (string, error) {
			callNum++
			switch callNum {
			case 1:
				// readNotes
				return "boxy_guest_os=linux;boxy_guest_user=admin;boxy_guest_password_ref=env:BOX_PASSWORD\n", nil
			case 2:
				// vmNameFromID
				return "boxy-abc123\n", nil
			case 3:
				// vmIP
				return "192.0.2.5\n", nil
			}
			return "", fmt.Errorf("unexpected call %d", callNum)
		},
		resolveSecret: func(_ context.Context, ref providersdk.SecretRef) (string, error) {
			if ref != "env:BOX_PASSWORD" {
				t.Fatalf("unexpected secret ref %q", ref)
			}
			return "${BOXY_TEST_LINUX_PASSWORD}", nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			if guestOS != "linux" {
				t.Errorf("guestOS = %q, want linux", guestOS)
			}
			if guestPassword != "${BOXY_TEST_LINUX_PASSWORD}" {
				t.Errorf("guestPassword = %q, want ${BOXY_TEST_LINUX_PASSWORD}", guestPassword)
			}
			if sshHost != "192.0.2.5" {
				t.Errorf("sshHost = %q, want 192.0.2.5", sshHost)
			}
			return &fakeGuestExec{stdout: "linux output", exitCode: 0}
		},
	}

	result, err := d.Update(context.Background(), fakeGUID, &ExecOp{
		Command:         []string{"uname", "-a"},
		GuestCredential: passwordCredential("admin", "${BOXY_TEST_LINUX_PASSWORD}"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outputs["stdout"] != "linux output" {
		t.Errorf("stdout = %q, want linux output", result.Outputs["stdout"])
	}
}

// --- Delete ---

func TestDriver_Delete_EmptyID(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		t.Fatal("psExec should not be called for empty ID")
		return "", nil
	})
	err := d.Delete(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestDriver_Delete_HappyPath(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			// Info query: name|vhd|state (already off, no wait needed)
			return "boxy-abc123|C:\\VMs\\boxy-abc123.vhdx|Off\n", nil
		case 2:
			// Stop+Remove
			return "", nil
		case 3:
			// Delete VHD
			return "", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	err := d.Delete(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callNum != 3 {
		t.Fatalf("callNum = %d, want 3 (no wait loop for an already-terminal VM)", callNum)
	}
}

func TestDriver_Delete_MissingVMIsSuccess(t *testing.T) {
	calls := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		calls++
		return "__BOXY_NOT_FOUND__\n", nil
	})

	if err := d.Delete(context.Background(), fakeGUID); err != nil {
		t.Fatalf("Delete missing VM: %v", err)
	}
	if calls != 1 {
		t.Fatalf("powershell calls = %d, want 1", calls)
	}
}

func TestDriver_Delete_WaitsForStuckVMThenSucceeds(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			// Info query: VM is mid-teardown.
			return "boxy-abc123|C:\\VMs\\boxy-abc123.vhdx|Stopping\n", nil
		case 2:
			// First state poll: still stopping.
			return "Stopping\n", nil
		case 3:
			// Second state poll: now settled.
			return "Off\n", nil
		case 4:
			// Stop+Remove
			return "", nil
		case 5:
			// Delete VHD
			return "", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})
	d.deleteWaitInterval = time.Millisecond

	if err := d.Delete(context.Background(), fakeGUID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callNum != 5 {
		t.Fatalf("callNum = %d, want 5", callNum)
	}
}

func TestDriver_Delete_TimesOutOnStuckVMWithoutForcingRemoval(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callNum++
		if callNum == 1 {
			return "boxy-abc123|C:\\VMs\\boxy-abc123.vhdx|Stopping\n", nil
		}
		// Always still stopping — never settles.
		return "Stopping\n", nil
	})
	d.deleteWaitTimeout = 5 * time.Millisecond
	d.deleteWaitInterval = time.Millisecond

	err := d.Delete(context.Background(), fakeGUID)
	if err == nil {
		t.Fatal("expected error when VM never reaches a terminal state")
	}
	if !errors.Is(err, ErrVMBusy) {
		t.Fatalf("error = %v, want wrapped ErrVMBusy", err)
	}
}

// --- Allocate ---

func TestDriver_Allocate_Linux(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			return "boxy_guest_os=linux;boxy_guest_user=ubuntu\n", nil // readNotes
		case 2:
			return "boxy-abc123\n", nil // vmNameFromID
		case 3:
			return "198.51.100.100\n", nil // vmIP
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	d.resolveBootstrap = func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
		return providersdk.GuestBootstrapCredential{Username: "ubuntu", Password: "bootstrap"}, nil
	}
	d.guestExecFactory = func(_, _, _, _, _ string) vmsdk.GuestExec {
		return &fakeGuestExec{exitCode: 0}
	}

	info, err := d.Allocate(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info["access"] != "ssh" {
		t.Errorf("access = %q, want ssh", info["access"])
	}
	if info["ssh_host"] != "198.51.100.100" {
		t.Errorf("ssh_host = %q, want 198.51.100.100", info["ssh_host"])
	}
	if info["ssh_user"] != "ubuntu" {
		t.Errorf("ssh_user = %q, want ubuntu", info["ssh_user"])
	}
}

func TestDriver_PersonalizeGuest_Linux(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			return "boxy_guest_os=linux;boxy_guest_user=ubuntu\n", nil
		case 2:
			return "boxy-abc123\n", nil
		case 3:
			return "198.51.100.100\n", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	d.resolveBootstrap = func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
		return providersdk.GuestBootstrapCredential{Username: "ubuntu", Password: "bootstrap"}, nil
	}
	d.guestExecFactory = func(_, _, _, _, _ string) vmsdk.GuestExec {
		return &fakeGuestExec{exitCode: 0}
	}

	result, err := d.PersonalizeGuest(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.AccessDetails.Properties["ssh_user"]; got != "ubuntu" {
		t.Errorf("ssh_user = %q, want ubuntu", got)
	}
}

func TestDriver_PersonalizeGuest_RotatesAndReturnsCredential(t *testing.T) {
	var guestExecs []*recordingGuestExec
	d := &Driver{
		psExec: func(_ context.Context, script string) (string, error) {
			switch {
			case strings.Contains(script, "Get-VMNetworkAdapter"):
				return "10.0.0.5\n", nil
			case strings.Contains(script, "(Get-VM -Id") && strings.Contains(script, ").Name"):
				return "boxy-abc123\n", nil
			default:
				return "boxy_guest_os=windows;boxy_guest_user=Administrator\n", nil
			}
		},
		resolveBootstrap: func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
			return providersdk.GuestBootstrapCredential{Username: "Administrator", Password: "bootstrap"}, nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			exec := &recordingGuestExec{password: guestPassword}
			guestExecs = append(guestExecs, exec)
			return exec
		},
	}

	result, err := d.PersonalizeGuest(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("PersonalizeGuest: %v", err)
	}
	if len(guestExecs) != 2 {
		t.Fatalf("guest exec sessions = %d, want bootstrap and verification sessions", len(guestExecs))
	}
	if guestExecs[0].password != "bootstrap" {
		t.Fatalf("bootstrap password = %q, want bootstrap", guestExecs[0].password)
	}
	if guestExecs[1].password == "" || guestExecs[1].password == guestExecs[0].password {
		t.Fatalf("rotated password = %q, want a fresh password", guestExecs[1].password)
	}
	if len(guestExecs[0].calls) != 1 || !strings.Contains(strings.Join(guestExecs[0].calls[0], " "), "Set-LocalUser") {
		t.Fatalf("rotation calls = %+v, want Set-LocalUser", guestExecs[0].calls)
	}
	if len(guestExecs[1].calls) != 1 || guestExecs[1].calls[0][0] != "whoami" {
		t.Fatalf("verification calls = %+v, want whoami", guestExecs[1].calls)
	}

	if result.EphemeralCredential == nil || result.EphemeralCredential.Kind != "password" {
		t.Fatalf("ephemeral credential = %+v, want password credential", result.EphemeralCredential)
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(result.EphemeralCredential.Data, &payload); err != nil {
		t.Fatalf("decode returned credential: %v", err)
	}
	if payload.Username != "Administrator" || payload.Password != guestExecs[1].password {
		t.Fatalf("returned payload = %+v, want Administrator/%q", payload, guestExecs[1].password)
	}
}

func TestDriver_Allocate_Windows(t *testing.T) {
	callNum := 0
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callNum++
		switch callNum {
		case 1:
			return "boxy_guest_os=windows;boxy_guest_user=Administrator\n", nil
		case 2:
			return "boxy-abc123\n", nil
		case 3:
			return "192.0.2.1\n", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	d.resolveBootstrap = func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
		return providersdk.GuestBootstrapCredential{Username: "Administrator", Password: "bootstrap"}, nil
	}
	d.guestExecFactory = func(_, _, _, _, _ string) vmsdk.GuestExec {
		return &fakeGuestExec{exitCode: 0}
	}

	info, err := d.Allocate(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info["access"] != "winrm" {
		t.Errorf("access = %q, want winrm", info["access"])
	}
}

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

// --- Helpers ---

// fakeGuestExec is a test double for vmsdk.GuestExec.
type fakeGuestExec struct {
	stdout   string
	exitCode int
	err      error
}

type recordingGuestExec struct {
	password string
	calls    [][]string
}

func (f *recordingGuestExec) Exec(_ context.Context, cmd string, args ...string) (*vmsdk.ExecResult, error) {
	f.calls = append(f.calls, append([]string{cmd}, args...))
	return &vmsdk.ExecResult{ExitCode: 0}, nil
}

func (f *fakeGuestExec) Exec(_ context.Context, _ string, _ ...string) (*vmsdk.ExecResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &vmsdk.ExecResult{Stdout: f.stdout, ExitCode: f.exitCode}, nil
}

func passwordCredential(username, password string) *providersdk.GuestCredential {
	b, _ := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: username, Password: password})
	return &providersdk.GuestCredential{Kind: "password", Data: b}
}

// --- providersdk.Driver interface compliance ---

var _ providersdk.Driver = (*Driver)(nil)
var _ providersdk.ResourceLister = (*Driver)(nil)
var _ providersdk.GuestPersonalizer = (*Driver)(nil)
