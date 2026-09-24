package hyperv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

func TestSegmentLedger_Record_StoresSuppliedCIDRAndDerivesAddresses(t *testing.T) {
	ledger := newSegmentLedger(filepath.Join(t.TempDir(), "network-segments.json"))

	got, err := ledger.record("sb-1", "10.250.4.0/29")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.CIDR != "10.250.4.0/29" {
		t.Fatalf("CIDR = %q, want the caller-supplied range", got.CIDR)
	}
	// Gateway and guest are derived from the supplied block, using the same
	// offsets the ledger used to apply to blocks it carved itself.
	if got.Gateway != "10.250.4.1" {
		t.Fatalf("Gateway = %q, want 10.250.4.1", got.Gateway)
	}
	if got.GuestAddress != "10.250.4.2" {
		t.Fatalf("GuestAddress = %q, want 10.250.4.2", got.GuestAddress)
	}
}

// TestSegmentLedger_Record_IgnoresADifferentCIDROnRepeat pins the
// idempotency rule that matters once the server picks the range: a retried
// CreateSegment that proposes a different CIDR must not renumber a segment
// whose switch and NAT may already be bound to the original one.
func TestSegmentLedger_Record_IgnoresADifferentCIDROnRepeat(t *testing.T) {
	ledger := newSegmentLedger(filepath.Join(t.TempDir(), "network-segments.json"))

	first, err := ledger.record("sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	again, err := ledger.record("sb-1", "10.250.9.0/29")
	if err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if again.CIDR != first.CIDR {
		t.Fatalf("re-recording changed the CIDR: %q -> %q", first.CIDR, again.CIDR)
	}
}

func TestSegmentLedger_Record_RejectsUnusableCIDR(t *testing.T) {
	ledger := newSegmentLedger(filepath.Join(t.TempDir(), "network-segments.json"))
	for _, bad := range []string{"", "not-a-cidr", "2001:db8::/64"} {
		if _, err := ledger.record("sb-"+bad, bad); err == nil {
			t.Fatalf("record(%q) succeeded, want an error", bad)
		}
	}
}

func TestSegmentLedger_Release_RemovesTheEntry(t *testing.T) {
	ledger := newSegmentLedger(filepath.Join(t.TempDir(), "network-segments.json"))

	if _, err := ledger.record("sb-1", "10.250.0.0/29"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := ledger.release("sb-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok, err := ledger.lookupBySandboxID("sb-1"); err != nil {
		t.Fatalf("lookup: %v", err)
	} else if ok {
		t.Fatal("entry still present after release")
	}
}

func TestSegmentLedger_ConcurrentRecord_NoLostEntries(t *testing.T) {
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
			alloc, err := ledger.record(fmt.Sprintf("sb-%d", i), fmt.Sprintf("10.250.%d.0/29", i))
			errs[i] = err
			cidrs[i] = alloc.CIDR
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("record sb-%d: %v", i, err)
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
		alloc, ok, err := reloaded.lookupBySandboxID(sandboxID)
		if err != nil {
			t.Fatalf("reload lookup %s: %v", sandboxID, err)
		}
		if !ok {
			t.Fatalf("entry for %s missing after reload -- a concurrent write was lost", sandboxID)
		}
		if alloc.CIDR != cidrs[i] {
			t.Fatalf("entry for %s lost or changed: got CIDR %q after reload, want %q (the CIDR its own record() call returned)", sandboxID, alloc.CIDR, cidrs[i])
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

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "boxy-sb-sb-1" {
		t.Fatalf("ref = %q, want %q", ref, "boxy-sb-sb-1")
	}
	// Two calls: the local CIDR-conflict probe, then the setup script. The
	// probe is what lets this host refuse a server-proposed range that
	// collides with something only it can see (#370).
	if len(scripts) != 2 {
		t.Fatalf("expected a conflict probe then a setup call, got %d", len(scripts))
	}
	for _, want := range []string{"Get-NetNat", "Get-NetIPAddress"} {
		if !strings.Contains(scripts[0], want) {
			t.Fatalf("first call should probe for local CIDR conflicts, missing %q:\n%s", want, scripts[0])
		}
	}
	scripts = scripts[1:]
	for _, want := range []string{
		"New-VMSwitch", "SwitchType Internal",
		"vEthernet (boxy-sb-sb-1)",
		// One shared NAT over the whole base range, not one per segment.
		"New-NetNat -Name 'boxy-segments' -InternalIPInterfaceAddressPrefix '10.250.0.0/16'",
		// The prefix length is rendered from segmentPrefixLen, and the
		// gateway/alias pair around it must not have been transposed when
		// that %d was inserted into the positional argument list.
		"New-NetIPAddress -IPAddress '10.250.0.1' -PrefixLength 29 -InterfaceAlias 'vEthernet (boxy-sb-sb-1)'",
	} {
		if !strings.Contains(scripts[0], want) {
			t.Fatalf("script missing %q:\n%s", want, scripts[0])
		}
	}
	if strings.Contains(scripts[0], "New-NetNat -Name 'boxy-sb-") {
		t.Fatalf("script creates a per-sandbox NAT; Windows supports one NAT per host:\n%s", scripts[0])
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

	if _, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29"); err == nil {
		t.Fatal("expected CreateSegment to fail")
	}

	// Observe the persisted ledger directly rather than through another
	// CreateSegment call, which would itself write the entry and so look the
	// same whether or not the first one survived.
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

	fail = false
	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
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

// segmentDriver builds a Driver wired the way a real agent is for the
// attach path: a fake PowerShell host, a bootstrap resolver, and a guest-exec
// factory recording every session it hands out. guestNotes is returned for
// any Get-VM Notes read, so a caller can vary the guest OS/user.
func segmentDriver(t *testing.T, guestNotes string, sessions *[]*recordingGuestExec) *Driver {
	t.Helper()
	d := &Driver{
		psExec: func(_ context.Context, script string) (string, error) {
			switch {
			case strings.Contains(script, "(Get-VM -Id") && strings.Contains(script, ").Name"):
				return "boxy-vm-1\n", nil
			case strings.Contains(script, "(Get-VM -Id") && strings.Contains(script, ").Notes"):
				return guestNotes + "\n", nil
			case strings.Contains(script, "Get-VMNetworkAdapter"):
				// The pre-segment address the VM holds on the pool's own
				// switch; AttachToSegment is what replaces it.
				return "10.0.0.5\n", nil
			default:
				return "", nil
			}
		},
		resolveBootstrap: func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
			return providersdk.GuestBootstrapCredential{Username: "Administrator", Password: "${BOXY_TEST_PASSWORD}"}, nil
		},
		guestExecFactory: func(_, _, _, guestPassword, _ string) vmsdk.GuestExec {
			exec := &recordingGuestExec{password: guestPassword}
			if sessions != nil {
				*sessions = append(*sessions, exec)
			}
			return exec
		},
	}
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	return d
}

const windowsGuestNotes = "boxy_guest_os=windows;boxy_guest_user=Administrator"

// TestDriver_AttachToSegment_ConnectsThenAssignsSegmentAddress is the core
// regression test for C2 (#224, Plan 1c final review): before this fix,
// AttachToSegment moved the VM's adapter onto the segment's Internal switch
// and stopped there. That switch issues no DHCP, so the guest kept its
// stale, wrong-subnet address from the pool's original switch (or fell back
// to APIPA) and was unreachable on the very network it had just been
// isolated onto. The address must now come from the segment's own /29 block.
func TestDriver_AttachToSegment_ConnectsThenAssignsSegmentAddress(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("guest sessions = %d, want exactly one in-guest addressing session", len(sessions))
	}
	if len(sessions[0].calls) != 1 {
		t.Fatalf("guest exec calls = %+v, want one addressing script", sessions[0].calls)
	}
	script := strings.Join(sessions[0].calls[0], " ")
	// The first block is 10.250.0.0/29: .1 is the switch's gateway, so the
	// guest takes .2 (segmentGuestOffset).
	for _, want := range []string{
		"New-NetIPAddress",
		"-IPAddress '10.250.0.2'",
		"-PrefixLength 29",
		"-DefaultGateway '10.250.0.1'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("in-guest script missing %q:\n%s", want, script)
		}
	}
	// A segment has no external resolution to point at, so no DNS servers
	// are configured (see AttachToSegment).
	if strings.Contains(script, "Set-DnsClientServerAddress") {
		t.Fatalf("in-guest script should not configure DNS for an isolated segment:\n%s", script)
	}
}

// TestDriver_AttachToSegment_PrefersRotatedCredentialOverStaleBootstrap
// guards the ordering gap this fix had to solve: by attach time the guest
// has been rotated off the pool bootstrap and the control plane has already
// dropped its copy of the rotated value, so authenticating with whatever
// resolveBootstrapCredential returns would fail against a real guest. The
// driver must use the credential it rotated the guest onto itself.
func TestDriver_AttachToSegment_PrefersRotatedCredentialOverStaleBootstrap(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)

	if _, err := d.PersonalizeGuest(context.Background(), fakeGUID, providersdk.GuestPersonalizationOptions{ApplyNetwork: true}); err != nil {
		t.Fatalf("PersonalizeGuest: %v", err)
	}
	rotated := sessions[len(sessions)-1].password
	if rotated == "" || rotated == "${BOXY_TEST_PASSWORD}" {
		t.Fatalf("verification session password = %q, want a freshly rotated value", rotated)
	}

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	before := len(sessions)
	if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if len(sessions) != before+1 {
		t.Fatalf("guest sessions after attach = %d, want one more than %d", len(sessions), before)
	}
	if got := sessions[before].password; got != rotated {
		t.Fatalf("attach session password = %q, want the rotated credential %q (the stale pool bootstrap would not authenticate)", got, rotated)
	}
}

// TestDriver_AttachToSegment_FallsBackToBootstrapWithoutRotatedCredential
// covers the agent-restart window: the in-memory rotated credential is gone,
// so the attach falls back to resolveBootstrapCredential rather than failing
// outright. Best-effort by design -- see segmentGuestCredential.
func TestDriver_AttachToSegment_FallsBackToBootstrapWithoutRotatedCredential(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if len(sessions) != 1 || sessions[0].password != "${BOXY_TEST_PASSWORD}" {
		t.Fatalf("sessions = %+v, want a single session using the bootstrap credential", sessions)
	}
}

// TestDriver_PersonalizeGuest_RetainsRotatedCredentialOnlyAtAllocationTime
// scopes the retained credential to the phase that actually needs it.
// AttachToSegment is the only consumer, and it runs at allocation time; an
// admission-time (preheat) personalization has no sandbox, no segment, and
// therefore no attach to authenticate later. Retaining unconditionally left
// this agent process holding the plaintext password of every preheated,
// unclaimed VM in every Hyper-V pool for the pool's entire lifetime -- the
// same thing #358's "a preheated-but-unclaimed VM has no reason to be
// network-reachable" principle rejects, applied to credentials.
func TestDriver_PersonalizeGuest_RetainsRotatedCredentialOnlyAtAllocationTime(t *testing.T) {
	for name, applyNetwork := range map[string]bool{
		"admission-time (preheat) retains nothing": false,
		"allocation-time retains for the attach":   true,
	} {
		t.Run(name, func(t *testing.T) {
			var sessions []*recordingGuestExec
			d := segmentDriver(t, windowsGuestNotes, &sessions)

			if _, err := d.PersonalizeGuest(context.Background(), fakeGUID, providersdk.GuestPersonalizationOptions{ApplyNetwork: applyNetwork}); err != nil {
				t.Fatalf("PersonalizeGuest: %v", err)
			}

			d.rotatedCredsMu.Lock()
			_, held := d.rotatedCreds[fakeGUID]
			d.rotatedCredsMu.Unlock()
			if held != applyNetwork {
				t.Fatalf("rotated credential retained = %v, want %v for ApplyNetwork=%v", held, applyNetwork, applyNetwork)
			}
		})
	}
}

// TestDriver_AttachToSegment_ForgetsRotatedCredentialOnDelete pins the
// retained credential's lifetime to the resource's own: once Delete confirms
// the VM gone, the driver must not keep holding its guest password.
func TestDriver_AttachToSegment_ForgetsRotatedCredentialOnDelete(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)
	if _, err := d.PersonalizeGuest(context.Background(), fakeGUID, providersdk.GuestPersonalizationOptions{ApplyNetwork: true}); err != nil {
		t.Fatalf("PersonalizeGuest: %v", err)
	}
	d.rotatedCredsMu.Lock()
	_, held := d.rotatedCreds[fakeGUID]
	d.rotatedCredsMu.Unlock()
	if !held {
		t.Fatal("expected the rotated credential to be retained after a verified rotation")
	}

	// Get-VM finds nothing: Delete's already-gone path, which still runs the
	// deferred cleanup.
	d.psExec = func(context.Context, string) (string, error) { return "__BOXY_NOT_FOUND__\n", nil }
	if err := d.Delete(context.Background(), fakeGUID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	d.rotatedCredsMu.Lock()
	_, stillHeld := d.rotatedCreds[fakeGUID]
	d.rotatedCredsMu.Unlock()
	if stillHeld {
		t.Fatal("Delete left the resource's guest credential in memory")
	}
}

// TestDriver_AttachToSegment_DerivesGuestAddressForLegacyLedgerEntry covers
// an agent upgraded mid-sandbox: Plan 1a wrote network-segments.json without
// a guest_address field, and an attach against such an entry must derive the
// address from the block's CIDR rather than fail.
func TestDriver_AttachToSegment_DerivesGuestAddressForLegacyLedgerEntry(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	// Rewrite the entry the way an older build would have persisted it.
	if _, err := d.segments().store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		alloc := s.BySandboxID["sb-1"]
		alloc.GuestAddress = ""
		s.BySandboxID["sb-1"] = alloc
		return s, nil
	}); err != nil {
		t.Fatalf("downgrade ledger entry: %v", err)
	}

	if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("guest sessions = %d, want one", len(sessions))
	}
	if script := strings.Join(sessions[0].calls[0], " "); !strings.Contains(script, "-IPAddress '10.250.0.2'") {
		t.Fatalf("derived guest address missing from script:\n%s", script)
	}
}

// TestDriver_AttachToSegment_RepeatReassignsSameAddress covers
// providersdk.NetworkIsolator's idempotency contract for this method: the
// guest address is derived from a constant offset, not allocated, so a retry
// after a partial failure re-applies the identical address.
func TestDriver_AttachToSegment_RepeatReassignsSameAddress(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, windowsGuestNotes, &sessions)

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
			t.Fatalf("AttachToSegment attempt %d: %v", attempt, err)
		}
	}
	if len(sessions) != 2 {
		t.Fatalf("guest sessions = %d, want one per attach attempt", len(sessions))
	}
	first := strings.Join(sessions[0].calls[0], " ")
	second := strings.Join(sessions[1].calls[0], " ")
	if first != second {
		t.Fatalf("a retried attach configured a different address:\n%s\n---\n%s", first, second)
	}
}

// TestDriver_AttachToSegment_LinuxGuestIsAHardError: PowerShell Direct is
// Windows-only, and an unaddressed guest on an isolated switch is broken --
// so this fails loudly rather than skipping. Matches how every other
// boxy-managed in-guest addressing path rejects Linux.
func TestDriver_AttachToSegment_LinuxGuestIsAHardError(t *testing.T) {
	var sessions []*recordingGuestExec
	d := segmentDriver(t, "boxy_guest_os=linux;boxy_guest_user=ubuntu", &sessions)

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	err = d.AttachToSegment(context.Background(), fakeGUID, ref)
	if err == nil {
		t.Fatal("expected AttachToSegment to reject a Linux guest")
	}
	if !strings.Contains(err.Error(), "not supported for Linux guests") {
		t.Fatalf("error = %v, want the shared Linux-unsupported message", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("guest sessions = %d, want none for a rejected Linux guest", len(sessions))
	}
}

// TestDriver_AttachToSegment_UnknownSegmentIsAnError: without a ledger entry
// there is no way to know which address the guest should take, and silently
// leaving it unaddressed is exactly the C2 defect this fix exists to close.
func TestDriver_AttachToSegment_UnknownSegmentIsAnError(t *testing.T) {
	d := segmentDriver(t, windowsGuestNotes, nil)
	err := d.AttachToSegment(context.Background(), fakeGUID, providersdk.SegmentRef("boxy-sb-never-created"))
	if err == nil {
		t.Fatal("expected AttachToSegment to fail for a segment with no ledger entry")
	}
	if !strings.Contains(err.Error(), "no segment ledger entry") {
		t.Fatalf("error = %v, want it to name the missing ledger entry", err)
	}
}

// TestDriver_AttachToSegment_ConnectsAdapterBeforeAddressing pins the
// ordering: the guest can only be addressed for the segment's subnet once
// its adapter is actually on the segment's switch.
func TestDriver_AttachToSegment_ConnectsAdapterBeforeAddressing(t *testing.T) {
	var order []string
	d := segmentDriver(t, windowsGuestNotes, nil)
	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}

	base := d.psExec
	d.psExec = func(ctx context.Context, script string) (string, error) {
		if strings.Contains(script, "Connect-VMNetworkAdapter") {
			order = append(order, "connect")
		}
		return base(ctx, script)
	}
	d.guestExecFactory = func(_, _, _, _, _ string) vmsdk.GuestExec {
		order = append(order, "assign")
		return &recordingGuestExec{}
	}

	if err := d.AttachToSegment(context.Background(), fakeGUID, ref); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if len(order) != 2 || order[0] != "connect" || order[1] != "assign" {
		t.Fatalf("call order = %v, want connect then assign", order)
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

// TestDriver_DestroySegment_KeepsSharedNAT: other segments on the host
// route through the shared NAT, so tearing down one segment must never
// remove it.
func TestDriver_DestroySegment_KeepsSharedNAT(t *testing.T) {
	var script string
	d := mockDriver(func(_ context.Context, s string) (string, error) {
		script = s
		return "", nil
	})
	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("boxy-sb-sb-1")); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if strings.Contains(script, sharedNATName) {
		t.Fatalf("DestroySegment script references the shared NAT %q:\n%s", sharedNATName, script)
	}
}

// TestEnsureSharedNATScript_Rules pins the rules the shared-NAT script
// enforces. It can only check the script text; the behavior itself needs a
// real Hyper-V host.
func TestEnsureSharedNATScript_Rules(t *testing.T) {
	script := ensureSharedNATScript()
	for _, want := range []string{
		// Any NAT other than the shared or a legacy one is refused rather
		// than joined by a second one -- on every call, not only when the
		// shared NAT is missing.
		"Where-Object { $_.Name -ne 'boxy-segments' -and $_.Name -notlike 'boxy-sb-*' }",
		"one NAT network per host",
		// An existing shared NAT over the wrong prefix is an error.
		"$nat.InternalIPInterfaceAddressPrefix -ne '" + segmentBaseCIDR + "'",
		// Legacy per-sandbox NATs are removed, matched by name only.
		"Where-Object { $_.Name -like 'boxy-sb-*' }",
		"Remove-NetNat",
		// Losing a creation race counts as success once the NAT exists.
		"} catch {\n        if (-not (Get-NetNat -Name 'boxy-segments'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shared NAT script missing %q:\n%s", want, script)
		}
	}
	// Every refusal comes before any change, so a refused call leaves the
	// host untouched.
	lastThrow := strings.LastIndex(script, "throw (")
	if lastThrow > strings.Index(script, "Remove-NetNat") || lastThrow > strings.Index(script, "New-NetNat") {
		t.Fatalf("shared NAT script changes the host before its last validation check:\n%s", script)
	}
	// The foreign-NAT check must not sit inside the "shared NAT missing"
	// branch, or a NAT added after boxy-segments would be accepted.
	if strings.Index(script, "if (-not $nat)") < strings.Index(script, "$other.Count") {
		t.Fatalf("foreign-NAT check only runs when the shared NAT is missing:\n%s", script)
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

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
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

	// The freed block is genuinely reusable by the next sandbox.
	next, err := d.CreateSegment(context.Background(), "sb-2", "10.250.0.0/29")
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

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
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
	if len(state.BySandboxID) != 0 {
		t.Fatalf("ledger = %v, want empty after destroying the only segment", state.BySandboxID)
	}
}

func TestDriver_CreateSegment_IsANetworkIsolator(t *testing.T) {
	var d providersdk.Driver = mockDriver(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := d.(providersdk.NetworkIsolator); !ok {
		t.Fatal("*hyperv.Driver must satisfy providersdk.NetworkIsolator")
	}
}

// TestDriver_CreateSegment_SerializesHostScripts: Hyper-V fails concurrent
// Internal switch creation, so two CreateSegment calls on one Driver must
// never have their PowerShell running at the same time.
func TestDriver_CreateSegment_SerializesHostScripts(t *testing.T) {
	var running, maxRunning int32
	d := mockDriver(func(context.Context, string) (string, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			m := atomic.LoadInt32(&maxRunning)
			if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		return "", nil
	})
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")

	cidrs := map[string]string{
		"sb-1": "10.250.0.0/29",
		"sb-2": "10.250.0.8/29",
		"sb-3": "10.250.0.16/29",
		"sb-4": "10.250.0.24/29",
	}
	var wg sync.WaitGroup
	for _, id := range []string{"sb-1", "sb-2", "sb-3", "sb-4"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := d.CreateSegment(context.Background(), id, cidrs[id]); err != nil {
				t.Errorf("CreateSegment(%s): %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	if maxRunning != 1 {
		t.Fatalf("up to %d segment scripts ran at once, want 1", maxRunning)
	}
}
