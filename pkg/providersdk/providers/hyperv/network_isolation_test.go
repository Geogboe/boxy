package hyperv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
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
