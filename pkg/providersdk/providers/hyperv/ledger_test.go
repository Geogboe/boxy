package hyperv

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// --- nextFreeAddress / ipv4UsableBounds ---

func TestNextFreeAddress_ReturnsFirstUsableAddress(t *testing.T) {
	addr, err := nextFreeAddress("192.0.2.0/24", nil)
	if err != nil {
		t.Fatalf("nextFreeAddress: %v", err)
	}
	if addr != "192.0.2.1" {
		t.Errorf("addr = %q, want 192.0.2.1 (network address .0 must be skipped)", addr)
	}
}

func TestNextFreeAddress_SkipsTaken(t *testing.T) {
	taken := map[string]bool{"192.0.2.1": true, "192.0.2.2": true}
	addr, err := nextFreeAddress("192.0.2.0/24", taken)
	if err != nil {
		t.Fatalf("nextFreeAddress: %v", err)
	}
	if addr != "192.0.2.3" {
		t.Errorf("addr = %q, want 192.0.2.3", addr)
	}
}

func TestNextFreeAddress_SkipsBroadcast(t *testing.T) {
	// A /30 has exactly two usable host addresses: .1 and .2 (.0 network,
	// .3 broadcast).
	taken := map[string]bool{"192.0.2.1": true}
	addr, err := nextFreeAddress("192.0.2.0/30", taken)
	if err != nil {
		t.Fatalf("nextFreeAddress: %v", err)
	}
	if addr != "192.0.2.2" {
		t.Errorf("addr = %q, want 192.0.2.2", addr)
	}
	taken["192.0.2.2"] = true
	if _, err := nextFreeAddress("192.0.2.0/30", taken); err == nil {
		t.Fatal("expected exhaustion error once both usable addresses are taken")
	}
}

func TestNextFreeAddress_Exhausted(t *testing.T) {
	taken := map[string]bool{}
	for i := 1; i <= 254; i++ {
		taken[fmt.Sprintf("192.0.2.%d", i)] = true
	}
	if _, err := nextFreeAddress("192.0.2.0/24", taken); err == nil {
		t.Fatal("expected error when range is fully allocated")
	}
}

func TestNextFreeAddress_InvalidRange(t *testing.T) {
	if _, err := nextFreeAddress("not-a-cidr", nil); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestIPv4UsableBounds_TooSmallRangeRejected(t *testing.T) {
	// /31 and /32 have no room for network+broadcast+at least one host.
	for _, cidr := range []string{"192.0.2.0/31", "192.0.2.0/32"} {
		if _, err := nextFreeAddress(cidr, nil); err == nil {
			t.Errorf("nextFreeAddress(%q) = nil error, want error (too small)", cidr)
		}
	}
}

// --- Driver ledger operations ---

// newTestLedgerDriver builds a Driver backed by a real, temp-directory
// ledger store via New — exercising the same construction path production
// code uses, unlike mockDriver's bare &Driver{} (which relies on ledger()'s
// lazy ephemeral fallback).
func newTestLedgerDriver(t *testing.T) *Driver {
	t.Helper()
	d, err := New(&Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func TestDriver_ReserveAddress_DistinctForConcurrentEntriesSameRange(t *testing.T) {
	d := newTestLedgerDriver(t)
	netCfg := &NetworkConfig{Range: "192.0.2.0/24"}

	if err := d.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-a): %v", err)
	}
	if err := d.reserveRangeEntry("vm-b", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-b): %v", err)
	}

	var wg sync.WaitGroup
	addrs := make(chan string, 2)
	errs := make(chan error, 2)
	for _, id := range []string{"vm-a", "vm-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			addr, err := d.reserveAddress(id)
			addrs <- addr
			errs <- err
		}(id)
	}
	wg.Wait()
	close(addrs)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("reserveAddress: %v", err)
		}
	}
	seen := map[string]bool{}
	for addr := range addrs {
		if seen[addr] {
			t.Fatalf("two concurrent reservations in the same range both got %q", addr)
		}
		seen[addr] = true
	}
	if len(seen) != 2 {
		t.Fatalf("got %d distinct addresses, want 2", len(seen))
	}
}

func TestDriver_ReserveAddress_IdempotentForSameID(t *testing.T) {
	d := newTestLedgerDriver(t)
	netCfg := &NetworkConfig{Range: "192.0.2.0/24"}
	if err := d.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry: %v", err)
	}

	first, err := d.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress (1st): %v", err)
	}
	second, err := d.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress (2nd, simulated crash retry): %v", err)
	}
	if first != second {
		t.Errorf("addresses differ across retries: %q vs %q, want same", first, second)
	}
}

func TestDriver_ReserveAddress_ExcludesGateway(t *testing.T) {
	d := newTestLedgerDriver(t)
	netCfg := &NetworkConfig{Range: "192.0.2.0/30", DefaultGateway: "192.0.2.1"}
	if err := d.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry: %v", err)
	}
	addr, err := d.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress: %v", err)
	}
	if addr == "192.0.2.1" {
		t.Errorf("reserved the gateway address %q", addr)
	}
	if addr != "192.0.2.2" {
		t.Errorf("addr = %q, want 192.0.2.2 (only other usable address in a /30)", addr)
	}
}

func TestDriver_ReserveAddress_NoEntryErrors(t *testing.T) {
	d := newTestLedgerDriver(t)
	if _, err := d.reserveAddress("no-such-id"); err == nil {
		t.Fatal("expected error reserving an address for an id with no ledger entry")
	}
}

func TestDriver_ReleaseAddress_FreesForReuse(t *testing.T) {
	d := newTestLedgerDriver(t)
	netCfg := &NetworkConfig{Range: "192.0.2.0/30"} // exactly two usable addresses: .1 and .2
	if err := d.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-a): %v", err)
	}
	addrA, err := d.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress(vm-a): %v", err)
	}

	if err := d.reserveRangeEntry("vm-b", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-b): %v", err)
	}
	addrB, err := d.reserveAddress("vm-b")
	if err != nil {
		t.Fatalf("reserveAddress(vm-b): %v", err)
	}
	if addrA == addrB {
		t.Fatalf("vm-a and vm-b both got %q before any release", addrA)
	}

	if err := d.releaseAddress("vm-a"); err != nil {
		t.Fatalf("releaseAddress(vm-a): %v", err)
	}
	if _, ok, err := d.ledgerLookup("vm-a"); err != nil || ok {
		t.Fatalf("ledgerLookup(vm-a) after release: ok=%v err=%v, want ok=false", ok, err)
	}

	if err := d.reserveRangeEntry("vm-c", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-c): %v", err)
	}
	addrC, err := d.reserveAddress("vm-c")
	if err != nil {
		t.Fatalf("reserveAddress(vm-c): %v", err)
	}
	if addrC != addrA {
		t.Errorf("addrC = %q, want the released address %q to be reused", addrC, addrA)
	}
}

func TestDriver_ReleaseAddress_NoEntryIsNoop(t *testing.T) {
	d := newTestLedgerDriver(t)
	if err := d.releaseAddress("never-reserved"); err != nil {
		t.Fatalf("releaseAddress on an id with no entry: %v", err)
	}
}

func TestDriver_ReserveAddress_ExhaustedRangeReturnsClearError(t *testing.T) {
	d := newTestLedgerDriver(t)
	// A /30 has two usable addresses (.1, .2); pinning the gateway to .2
	// leaves exactly one allocatable address for this test to exhaust.
	netCfg := &NetworkConfig{Range: "192.0.2.0/30", DefaultGateway: "192.0.2.2"}
	if err := d.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-a): %v", err)
	}
	if _, err := d.reserveAddress("vm-a"); err != nil {
		t.Fatalf("reserveAddress(vm-a): %v", err)
	}
	if err := d.reserveRangeEntry("vm-b", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry(vm-b): %v", err)
	}
	if _, err := d.reserveAddress("vm-b"); err == nil {
		t.Fatal("expected an exhaustion error for the second VM in a fully-allocated /30")
	}
}

func TestDriver_Ledger_PersistsAcrossFreshDriverInstances(t *testing.T) {
	dir := t.TempDir()
	d1, err := New(&Config{DataDir: dir})
	if err != nil {
		t.Fatalf("New (1st): %v", err)
	}
	netCfg := &NetworkConfig{Range: "192.0.2.0/24"}
	if err := d1.reserveRangeEntry("vm-a", netCfg); err != nil {
		t.Fatalf("reserveRangeEntry: %v", err)
	}
	addr, err := d1.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress: %v", err)
	}

	// A fresh Driver over the same DataDir (simulating a daemon/agent
	// restart) must see the same assignment — it's what makes the ledger
	// restart-safe.
	d2, err := New(&Config{DataDir: dir})
	if err != nil {
		t.Fatalf("New (2nd): %v", err)
	}
	entry, ok, err := d2.ledgerLookup("vm-a")
	if err != nil {
		t.Fatalf("ledgerLookup: %v", err)
	}
	if !ok {
		t.Fatal("2nd Driver instance has no ledger entry for vm-a")
	}
	if entry.AssignedAddress != addr {
		t.Errorf("assigned address after restart = %q, want %q", entry.AssignedAddress, addr)
	}

	// Retrying the reservation on the 2nd instance must return the same
	// address rather than picking a new one.
	again, err := d2.reserveAddress("vm-a")
	if err != nil {
		t.Fatalf("reserveAddress (2nd instance): %v", err)
	}
	if again != addr {
		t.Errorf("reserveAddress after restart = %q, want %q", again, addr)
	}
}

func TestNew_DataDirDefaultsAndResolves(t *testing.T) {
	base := t.TempDir()
	d, err := New(&Config{DataDir: filepath.Join(base, "custom")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := d.ledger().Path(); filepath.Dir(got) != filepath.Join(base, "custom") {
		t.Errorf("ledger path = %q, want dir %q", got, filepath.Join(base, "custom"))
	}
}
