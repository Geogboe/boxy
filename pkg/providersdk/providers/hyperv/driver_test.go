package hyperv

import (
	"context"
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

// mockDriver builds a Driver with psExec and optional guestExecFactory injected.
func mockDriver(psExecFn func(ctx context.Context, script string) (string, error)) *Driver {
	return &Driver{psExec: psExecFn}
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
		GuestPassword: "pass",
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
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
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
			return "pass", nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			guestExecCalled = true
			if guestOS != "windows" {
				t.Errorf("guestOS = %q, want windows", guestOS)
			}
			if guestPassword != "pass" {
				t.Errorf("guestPassword = %q, want pass", guestPassword)
			}
			return &fakeGuestExec{stdout: "output", exitCode: 0}
		},
	}

	result, err := d.Update(context.Background(), fakeGUID, &ExecOp{Command: []string{"echo", "hello"}})
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
				return "10.0.0.5\n", nil
			}
			return "", fmt.Errorf("unexpected call %d", callNum)
		},
		resolveSecret: func(_ context.Context, ref providersdk.SecretRef) (string, error) {
			if ref != "env:BOX_PASSWORD" {
				t.Fatalf("unexpected secret ref %q", ref)
			}
			return "linux-pass", nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			if guestOS != "linux" {
				t.Errorf("guestOS = %q, want linux", guestOS)
			}
			if guestPassword != "linux-pass" {
				t.Errorf("guestPassword = %q, want linux-pass", guestPassword)
			}
			if sshHost != "10.0.0.5" {
				t.Errorf("sshHost = %q, want 10.0.0.5", sshHost)
			}
			return &fakeGuestExec{stdout: "linux output", exitCode: 0}
		},
	}

	result, err := d.Update(context.Background(), fakeGUID, &ExecOp{Command: []string{"uname", "-a"}})
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
			return "192.168.1.100\n", nil // vmIP
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	info, err := d.Allocate(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info["access"] != "ssh" {
		t.Errorf("access = %q, want ssh", info["access"])
	}
	if info["ssh_host"] != "192.168.1.100" {
		t.Errorf("ssh_host = %q, want 192.168.1.100", info["ssh_host"])
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
			return "192.168.1.100\n", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

	result, err := d.PersonalizeGuest(context.Background(), fakeGUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.AccessDetails.Properties["ssh_user"]; got != "ubuntu" {
		t.Errorf("ssh_user = %q, want ubuntu", got)
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
			return "10.0.0.1\n", nil
		}
		return "", fmt.Errorf("unexpected call %d", callNum)
	})

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

func (f *fakeGuestExec) Exec(_ context.Context, _ string, _ ...string) (*vmsdk.ExecResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &vmsdk.ExecResult{Stdout: f.stdout, ExitCode: f.exitCode}, nil
}

// --- providersdk.Driver interface compliance ---

var _ providersdk.Driver = (*Driver)(nil)
var _ providersdk.ResourceLister = (*Driver)(nil)
var _ providersdk.GuestPersonalizer = (*Driver)(nil)
