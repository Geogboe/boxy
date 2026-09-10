package hyperv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

// TestSegmentLedger_ConcurrentAllocate_NoLostEntries exercises the exact race
// task-2's code review flagged (finding 1): before the fix, *Driver.segments*
// constructed a brand-new *segmentLedger (and therefore a brand-new,
// unshared diskjson.Store/sync.Mutex) on every call, so two concurrent
// allocate() calls against the "same" ledger each locked their own
// independent mutex over the same underlying file, both could read the same
// stale snapshot, and the second Update's write could silently clobber the
// first's, dropping an entry. This test drives many goroutines each calling
// allocate() for a distinct sandbox ID against one shared *segmentLedger
// instance and asserts every entry survives.
func TestSegmentLedger_ConcurrentAllocate_NoLostEntries(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	cidrs := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			alloc, err := ledger.allocate(fmt.Sprintf("sb-%d", i))
			errs[i] = err
			cidrs[i] = alloc.CIDR
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("allocate sb-%d: %v", i, err)
		}
	}

	// Reload the persisted state fresh from disk (a new *segmentLedger
	// instance, not the one the goroutines shared) to confirm every entry
	// actually made it to the final on-disk write, not just that each
	// goroutine's own in-memory return value looked fine.
	reloaded := newSegmentLedger(ledgerPath)
	seenCIDRs := make(map[string]bool)
	for i := 0; i < n; i++ {
		sandboxID := fmt.Sprintf("sb-%d", i)
		alloc, err := reloaded.allocate(sandboxID)
		if err != nil {
			t.Fatalf("reload allocate %s: %v", sandboxID, err)
		}
		if alloc.CIDR != cidrs[i] {
			t.Fatalf("entry for %s lost or changed: got CIDR %q after reload, want %q (the CIDR its own allocate() call returned)", sandboxID, alloc.CIDR, cidrs[i])
		}
		if seenCIDRs[alloc.CIDR] {
			t.Fatalf("duplicate CIDR %q handed to more than one sandbox", alloc.CIDR)
		}
		seenCIDRs[alloc.CIDR] = true
	}
	if len(seenCIDRs) != n {
		t.Fatalf("expected %d distinct CIDRs persisted, got %d", n, len(seenCIDRs))
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
	for _, want := range []string{"New-VMSwitch", "SwitchType Internal", "New-NetNat", "10.250.0.0/29", "vEthernet (boxy-sb-sb-1)"} {
		if !strings.Contains(scripts[0], want) {
			t.Fatalf("script missing %q:\n%s", want, scripts[0])
		}
	}
	if strings.Contains(scripts[0], "Get-NetAdapter") {
		t.Fatalf("script should resolve the adapter by its deterministic vEthernet alias, not a fuzzy Get-NetAdapter lookup:\n%s", scripts[0])
	}
}

// TestDriver_CreateSegment_FailurePreservesLedgerEntry exercises task-2 code
// review finding 3: a PowerShell failure must not release the sandbox's
// ledger entry, so a retried CreateSegment for the same sandboxID gets back
// the same CIDR/gateway/switch name rather than a different one.
func TestDriver_CreateSegment_FailurePreservesLedgerEntry(t *testing.T) {
	fail := true
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		if fail {
			return "", fmt.Errorf("boom")
		}
		return "", nil
	})
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")

	if _, err := d.CreateSegment(context.Background(), "sb-1"); err == nil {
		t.Fatal("expected CreateSegment to fail")
	}
	before, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate after failure: %v", err)
	}

	fail = false
	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("retry CreateSegment: %v", err)
	}
	if ref != providersdk.SegmentRef(before.SwitchName) {
		t.Fatalf("retry got switch %q, want the same one from before the failure %q", ref, before.SwitchName)
	}
	after, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate after retry: %v", err)
	}
	if after.CIDR != before.CIDR {
		t.Fatalf("retry changed the CIDR: got %q, want %q (the one allocated before the failure)", after.CIDR, before.CIDR)
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
