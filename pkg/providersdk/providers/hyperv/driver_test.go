package hyperv

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		callCount++
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
		capturedScript = script
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
}

func TestDriver_Create_CleanupOnFailure(t *testing.T) {
	callCount := 0
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		callCount++
		if callCount == 1 {
			// Main create script fails.
			return "", fmt.Errorf("New-VHD failed")
		}
		// Cleanup script succeeds.
		return "", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\t.vhdx`,
	})
	if err == nil {
		t.Fatal("expected error when create script fails")
	}
	if callCount < 2 {
		t.Errorf("expected cleanup call, callCount = %d", callCount)
	}
}

func TestDriver_Create_LinuxDefaults(t *testing.T) {
	var capturedScript string
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		capturedScript = script
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
		return "boxy_guest_os=windows;boxy_guest_user=admin;boxy_guest_password=pass\n", nil
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
			return "boxy_guest_os=windows;boxy_guest_user=Administrator;boxy_guest_password=pass\n", nil
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			guestExecCalled = true
			if guestOS != "windows" {
				t.Errorf("guestOS = %q, want windows", guestOS)
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
				return "boxy_guest_os=linux;boxy_guest_user=admin;boxy_guest_password=\n", nil
			case 2:
				// vmNameFromID
				return "boxy-abc123\n", nil
			case 3:
				// vmIP
				return "10.0.0.5\n", nil
			}
			return "", fmt.Errorf("unexpected call %d", callNum)
		},
		guestExecFactory: func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec {
			if guestOS != "linux" {
				t.Errorf("guestOS = %q, want linux", guestOS)
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
			// Info query: name|vhd
			return "boxy-abc123|C:\\VMs\\boxy-abc123.vhdx\n", nil
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
