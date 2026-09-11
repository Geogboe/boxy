package hyperv

import (
	"context"
	"errors"
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
	for _, want := range []string{
		"New-VMSwitch", "SwitchType Internal", "New-NetNat", "10.250.0.0/29",
		"vEthernet (boxy-sb-sb-1)",
		// The prefix length is rendered from segmentPrefixLen, and the
		// gateway/alias pair around it must not have been transposed when
		// that %d was inserted into the positional argument list.
		"New-NetIPAddress -IPAddress '10.250.0.1' -PrefixLength 29 -InterfaceAlias 'vEthernet (boxy-sb-sb-1)'",
	} {
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

	// Observe the persisted ledger directly rather than calling allocate()
	// again: allocate is itself mutating (it can pop FreedIndexes and hand
	// out a fresh block), so using it as the observation mechanism produces
	// the same-looking result whether or not the entry actually survived.
	before, err := d.segments().store.Load()
	if err != nil {
		t.Fatalf("load ledger after failure: %v", err)
	}
	entry, ok := before.BySandboxID["sb-1"]
	if !ok {
		t.Fatal("CreateSegment released sb-1's ledger entry on failure; a retry would allocate a different CIDR than the partially-created switch is bound to")
	}
	if entry.CIDR != "10.250.0.0/29" {
		t.Fatalf("preserved entry CIDR = %q, want the first block %q", entry.CIDR, "10.250.0.0/29")
	}
	if len(before.FreedIndexes) != 0 {
		t.Fatalf("CreateSegment returned block indexes to the free list on failure: %v", before.FreedIndexes)
	}

	fail = false
	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("retry CreateSegment: %v", err)
	}
	if ref != providersdk.SegmentRef(entry.SwitchName) {
		t.Fatalf("retry got switch %q, want the same one from before the failure %q", ref, entry.SwitchName)
	}
	after, err := d.segments().store.Load()
	if err != nil {
		t.Fatalf("load ledger after retry: %v", err)
	}
	if after.BySandboxID["sb-1"].CIDR != entry.CIDR {
		t.Fatalf("retry changed the CIDR: got %q, want %q (the one allocated before the failure)",
			after.BySandboxID["sb-1"].CIDR, entry.CIDR)
	}
}

func TestBlockForIndex_RejectsIndexBeyondBaseRange(t *testing.T) {
	// 10.250.0.0/16 holds exactly 8192 /29 blocks, so 8192 is the first
	// index past the end.
	const blocks = 8192
	if _, _, err := blockForIndex(blocks - 1); err != nil {
		t.Fatalf("last in-range index must still allocate: %v", err)
	}
	for _, index := range []int{blocks, blocks + 1, 1 << 30, -1} {
		_, _, err := blockForIndex(index)
		if err == nil {
			t.Fatalf("blockForIndex(%d) returned an address outside %s instead of an error", index, segmentBaseCIDR)
		}
		if index >= 0 && !errors.Is(err, errSegmentRangeExhausted) {
			t.Fatalf("blockForIndex(%d) error = %v, want it to wrap errSegmentRangeExhausted", index, err)
		}
	}
}

func TestIndexForBlock_RejectsCIDROutsideBaseRange(t *testing.T) {
	// Below the base address: the unsigned subtraction would underflow into
	// an enormous bogus index if unchecked.
	if _, err := indexForBlock("10.249.255.248/29"); err == nil {
		t.Fatal("indexForBlock accepted a CIDR sorting below segmentBaseCIDR")
	}
	// Past the last block in the base range.
	if _, err := indexForBlock("10.251.0.0/29"); err == nil {
		t.Fatal("indexForBlock accepted a CIDR beyond segmentBaseCIDR's last block")
	}
	got, err := indexForBlock("10.250.0.8/29")
	if err != nil {
		t.Fatalf("indexForBlock on a valid block: %v", err)
	}
	if got != 1 {
		t.Fatalf("indexForBlock(10.250.0.8/29) = %d, want 1", got)
	}
}

// TestSegmentLedger_ReleaseSkipsCorruptCIDR covers the other half of the
// underflow guard: a hand-edited/corrupt CIDR must not push a bogus index
// onto the free list for the next allocate to hand out.
func TestSegmentLedger_ReleaseSkipsCorruptCIDR(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "network-segments.json")
	ledger := newSegmentLedger(ledgerPath)

	if _, err := ledger.allocate("sb-1"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if _, err := ledger.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		alloc := s.BySandboxID["sb-1"]
		alloc.CIDR = "10.249.255.248/29"
		s.BySandboxID["sb-1"] = alloc
		return s, nil
	}); err != nil {
		t.Fatalf("corrupt ledger entry: %v", err)
	}
	if err := ledger.release("sb-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	state, err := ledger.store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := state.BySandboxID["sb-1"]; ok {
		t.Fatal("release left the corrupt entry in place")
	}
	if len(state.FreedIndexes) != 0 {
		t.Fatalf("release pushed an index recovered from a corrupt CIDR onto the free list: %v", state.FreedIndexes)
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

// TestDriver_DestroySegment_ReleasesLedgerEntry covers the final-review fix
// that made DestroySegment self-contained: the ledger entry is keyed by
// sandbox ID and reachable only through unexported API, so a caller holding
// a providersdk.NetworkIsolator could never release it separately. Matching
// happens on the recorded SwitchName because switchNameForSandbox's
// space-stripping is lossy and cannot be inverted.
func TestDriver_DestroySegment_ReleasesLedgerEntry(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if err := d.DestroySegment(context.Background(), ref); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}

	state, err := d.segments().store.Load()
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if _, ok := state.BySandboxID["sb-1"]; ok {
		t.Fatal("DestroySegment left sb-1's ledger entry behind; its CIDR would leak permanently")
	}
	if len(state.FreedIndexes) != 1 || state.FreedIndexes[0] != 0 {
		t.Fatalf("FreedIndexes = %v, want the destroyed segment's block index [0] returned for reuse", state.FreedIndexes)
	}

	// The freed block is genuinely reusable by the next sandbox.
	next, err := d.CreateSegment(context.Background(), "sb-2")
	if err != nil {
		t.Fatalf("CreateSegment sb-2: %v", err)
	}
	state, err = d.segments().store.Load()
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	if got := state.BySandboxID["sb-2"].CIDR; got != "10.250.0.0/29" {
		t.Fatalf("sb-2 CIDR = %q, want the reused first block %q (ref %q)", got, "10.250.0.0/29", next)
	}
}

// TestDriver_DestroySegment_IdempotentWhenAlreadyGone mirrors the Docker
// driver's test of the same name. Hyper-V is idempotent by construction --
// the teardown script's Get- guards no-op when the NAT/switch are gone, and
// releaseBySwitchName no-ops when no entry carries the switch name.
func TestDriver_DestroySegment_IdempotentWhenAlreadyGone(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if err := d.DestroySegment(context.Background(), ref); err != nil {
		t.Fatalf("first DestroySegment: %v", err)
	}
	if err := d.DestroySegment(context.Background(), ref); err != nil {
		t.Fatalf("second DestroySegment on an already-gone segment must be a no-op, got: %v", err)
	}
	// Never created at all, so no ledger entry ever existed for it.
	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("boxy-sb-never-existed")); err != nil {
		t.Fatalf("DestroySegment on an unknown segment must be a no-op, got: %v", err)
	}

	state, err := d.segments().store.Load()
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if len(state.FreedIndexes) != 1 {
		t.Fatalf("FreedIndexes = %v, want exactly one entry (repeat destroys must not free the same block twice)", state.FreedIndexes)
	}
}

func TestDriver_CreateSegment_IsANetworkIsolator(t *testing.T) {
	var d providersdk.Driver = mockDriver(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := d.(providersdk.NetworkIsolator); !ok {
		t.Fatal("*hyperv.Driver must satisfy providersdk.NetworkIsolator")
	}
}
