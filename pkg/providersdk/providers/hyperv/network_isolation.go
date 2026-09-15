package hyperv

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/diskjson"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// segmentAllocation is one sandbox's assigned switch/NAT identity, persisted
// so restarts don't lose track of which CIDRs are in use.
type segmentAllocation struct {
	SwitchName string `json:"switch_name"`
	CIDR       string `json:"cidr"`
	Gateway    string `json:"gateway"`

	// GuestAddress is the address AttachToSegment assigns inside the guest
	// (see segmentGuestOffset). It is persisted for observability -- you can
	// read a sandbox's in-guest address straight out of network-segments.json
	// -- but it is never the authoritative source: every read path derives it
	// from CIDR when empty (see lookupBySwitchName), because a ledger written
	// before this field existed carries no value for it and an attach must
	// not fail on that. Omitted from JSON when empty so an older ledger
	// round-trips unchanged rather than gaining a blank key.
	GuestAddress string `json:"guest_address,omitempty"`
}

type segmentLedgerState struct {
	// NextIndex is the next /29 block offset (0-based) to hand out from
	// segmentBaseCIDR. Only ever increases while entries exist; a released
	// CIDR is tracked in FreedIndexes and reused before NextIndex advances,
	// so long-running hosts don't walk the whole range needlessly.
	NextIndex    int                          `json:"next_index"`
	FreedIndexes []int                        `json:"freed_indexes,omitempty"`
	BySandboxID  map[string]segmentAllocation `json:"by_sandbox_id"`
}

// segmentBaseCIDR is the private range this driver carves per-sandbox blocks
// out of. Chosen from RFC 1918 space unlikely to collide with an operator's
// own LAN (10.250.0.0/16 is well outside common home/office 10.0.0.0/8
// allocations that start near 10.0.x.x or 10.1.x.x).
const segmentBaseCIDR = "10.250.0.0/16"

// segmentPrefixLen is the prefix length of each per-sandbox block carved out
// of segmentBaseCIDR. A /29 is 8 addresses: network, gateway, up to 5 usable
// hosts, broadcast -- enough for a small sandbox lab.
//
// This is the single source of truth for the block size: the persisted CIDR
// string, the host-side New-NetIPAddress -PrefixLength argument, and
// segmentBlockSize's address arithmetic are all derived from it rather than
// repeating the literal in three places.
const segmentPrefixLen = 29

// segmentBlockSize is how many IPv4 addresses one segmentPrefixLen block
// spans, derived from segmentPrefixLen rather than restated.
const segmentBlockSize = 1 << (32 - segmentPrefixLen)

// segmentGatewayOffset/segmentGuestOffset are the fixed positions, counted
// from a block's own network address, of the two addresses this driver hands
// out within a segment: the host-side vSwitch adapter's gateway address and
// the single guest address AttachToSegment configures inside the VM.
//
// One guest address is deliberately enough. A segment is single-tenant today
// -- one sandbox, and (per the sandbox wiring) one AttachToSegment call per
// claimed resource against that sandbox's segment -- so there is no
// multi-host allocation problem to solve inside a block yet. Deriving the
// address from a constant offset rather than tracking per-resource
// assignments in the ledger keeps AttachToSegment idempotent for free: a
// retry recomputes the identical address and assignGuestIP re-applies it to
// an already-configured guest (see its doc comment). Handing out more than
// one address per segment needs its own design, not a bigger constant.
const (
	segmentGatewayOffset = 1
	segmentGuestOffset   = 2
)

// segmentLedgerFilename is the JSON file name for the per-sandbox network
// segment ledger, written under Config.DataDir next to ledgerFilename.
const segmentLedgerFilename = "network-segments.json"

// errSegmentRangeExhausted reports that segmentBaseCIDR has no block left at
// the requested index -- either every block is in use, or a persisted index
// is out of range. Named (and wrapped, not replaced, by its callers) so a
// caller can tell genuine exhaustion apart from a malformed-config parse
// failure with errors.Is.
var errSegmentRangeExhausted = errors.New("network segment range exhausted")

type segmentLedger struct {
	store *diskjson.Store[segmentLedgerState]
}

func newSegmentLedger(path string) *segmentLedger {
	return &segmentLedger{
		store: diskjson.New(path, func() segmentLedgerState {
			return segmentLedgerState{BySandboxID: make(map[string]segmentAllocation)}
		}),
	}
}

// allocate returns the sandbox's existing CIDR/gateway if one is already
// recorded (idempotent -- a retried CreateSegment call must not hand out a
// second block), or carves and persists a new /29 otherwise.
func (l *segmentLedger) allocate(sandboxID string) (segmentAllocation, error) {
	state, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		if s.BySandboxID == nil {
			s.BySandboxID = make(map[string]segmentAllocation)
		}
		if _, ok := s.BySandboxID[sandboxID]; ok {
			return s, nil
		}
		index := s.NextIndex
		if n := len(s.FreedIndexes); n > 0 {
			index = s.FreedIndexes[n-1]
			s.FreedIndexes = s.FreedIndexes[:n-1]
		} else {
			s.NextIndex++
		}
		cidr, gateway, guest, err := blockForIndex(index)
		if err != nil {
			return s, err
		}
		s.BySandboxID[sandboxID] = segmentAllocation{
			SwitchName:   switchNameForSandbox(sandboxID),
			CIDR:         cidr,
			Gateway:      gateway,
			GuestAddress: guest,
		}
		return s, nil
	})
	if err != nil {
		return segmentAllocation{}, err
	}
	return state.BySandboxID[sandboxID], nil
}

func (l *segmentLedger) release(sandboxID string) error {
	_, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		return releaseAllocation(s, sandboxID), nil
	})
	return err
}

// releaseBySwitchName frees whichever sandbox's allocation carries
// switchName, if any. DestroySegment only ever receives the SegmentRef (the
// switch name), and switchNameForSandbox's space-stripping transformation is
// lossy, so it cannot be inverted to recover the sandbox ID -- matching on
// the SwitchName recorded verbatim in the allocation is exact where inverting
// the derivation would not be. A switch name with no matching entry is a
// no-op, not an error, matching DestroySegment's own idempotency contract.
//
// The scan and the release share one store.Update (and therefore one lock
// acquisition) rather than a Load followed by a separate release call, so no
// concurrent allocate can slip between finding the entry and deleting it.
func (l *segmentLedger) releaseBySwitchName(switchName string) error {
	_, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		for sandboxID, alloc := range s.BySandboxID {
			if alloc.SwitchName == switchName {
				return releaseAllocation(s, sandboxID), nil
			}
		}
		return s, nil
	})
	return err
}

// lookupBySwitchName returns the allocation recorded for switchName, if any.
// AttachToSegment receives only the opaque SegmentRef (the switch name) and
// never a sandbox ID, so this matches on the recorded SwitchName for exactly
// the reason releaseBySwitchName does: switchNameForSandbox's space-stripping
// derivation is lossy and cannot be inverted.
//
// A missing GuestAddress is derived from the entry's own CIDR rather than
// treated as an error: Plan 1a wrote this ledger without that field, and an
// agent upgraded mid-sandbox must still be able to address a guest on a
// segment allocated by the older build. The derivation is the same one
// blockForIndex performs, so the recovered value is identical to what a
// freshly-allocated entry would carry. ok is false with a nil error when no
// entry carries switchName -- a real condition (a segment created by a
// different agent, or a hand-removed ledger), not a failure of this lookup.
func (l *segmentLedger) lookupBySwitchName(switchName string) (segmentAllocation, bool, error) {
	state, err := l.store.Load()
	if err != nil {
		return segmentAllocation{}, false, err
	}
	for _, alloc := range state.BySandboxID {
		if alloc.SwitchName != switchName {
			continue
		}
		if alloc.GuestAddress == "" {
			guest, err := guestAddressForCIDR(alloc.CIDR)
			if err != nil {
				return segmentAllocation{}, false, fmt.Errorf("derive guest address for segment %q: %w", switchName, err)
			}
			alloc.GuestAddress = guest
		}
		return alloc, true, nil
	}
	return segmentAllocation{}, false, nil
}

// releaseAllocation removes sandboxID's entry from an in-flight ledger state
// and returns its block index to FreedIndexes for reuse. Factored out so
// release and releaseBySwitchName share one implementation rather than each
// reimplementing the free-list accounting; both call it from inside their own
// store.Update callback, so it must not lock anything itself.
//
// A CIDR whose index can't be recovered (corrupt or hand-edited state, see
// indexForBlock's range checks) is dropped from BySandboxID without being
// added to FreedIndexes: leaking one reusable block is strictly better than
// pushing a bogus index onto the free list for the next allocate to hand out.
func releaseAllocation(s segmentLedgerState, sandboxID string) segmentLedgerState {
	if s.BySandboxID == nil {
		return s
	}
	alloc, ok := s.BySandboxID[sandboxID]
	if !ok {
		return s
	}
	if idx, err := indexForBlock(alloc.CIDR); err == nil {
		s.FreedIndexes = append(s.FreedIndexes, idx)
	}
	delete(s.BySandboxID, sandboxID)
	return s
}

// segmentBaseBounds parses segmentBaseCIDR into its base address (as a
// uint32) and the number of segmentPrefixLen blocks it can hold -- 8192 for
// the current /16 base and /29 blocks, derived rather than hardcoded so a
// change to either constant stays consistent.
func segmentBaseBounds() (baseInt uint32, blocks int, err error) {
	_, base, parseErr := net.ParseCIDR(segmentBaseCIDR)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("parse segmentBaseCIDR: %w", parseErr)
	}
	ip4 := base.IP.To4()
	if ip4 == nil {
		return 0, 0, fmt.Errorf("segmentBaseCIDR %q is not IPv4", segmentBaseCIDR)
	}
	ones, bits := base.Mask.Size()
	if bits != 32 || ones > segmentPrefixLen {
		return 0, 0, fmt.Errorf("segmentBaseCIDR %q cannot hold a /%d block", segmentBaseCIDR, segmentPrefixLen)
	}
	baseInt = uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
	return baseInt, 1 << (segmentPrefixLen - ones), nil
}

// blockForIndex computes the index-th segmentPrefixLen block within
// segmentBaseCIDR, returning its network CIDR, the first usable address
// (used as the switch's gateway/host-side IP), and the guest-assignable
// address that follows it.
//
// The index is range-checked against segmentBaseCIDR's real capacity before
// any address arithmetic runs, so an out-of-range index returns
// errSegmentRangeExhausted instead of silently computing an address outside
// the base range (or, for a very large index, wrapping the uint32 offset
// around into unrelated address space).
func blockForIndex(index int) (cidr string, gateway string, guest string, err error) {
	baseInt, blocks, err := segmentBaseBounds()
	if err != nil {
		return "", "", "", err
	}
	if index < 0 || index >= blocks {
		return "", "", "", fmt.Errorf("%w: block index %d is outside the %d /%d blocks in %s",
			errSegmentRangeExhausted, index, blocks, segmentPrefixLen, segmentBaseCIDR)
	}
	//nolint:gosec // index is bounded to [0, blocks) immediately above, and
	// blocks is derived from segmentBaseCIDR's own prefix length, so the
	// conversion cannot overflow uint32 for any base range this constant can
	// express.
	blockStart := baseInt + uint32(index)*segmentBlockSize
	return fmt.Sprintf("%s/%d", ipv4FromUint32(blockStart), segmentPrefixLen),
		ipv4FromUint32(blockStart + segmentGatewayOffset),
		ipv4FromUint32(blockStart + segmentGuestOffset),
		nil
}

// guestAddressForCIDR derives a block's guest-assignable address from the
// block's own network CIDR, so a ledger entry persisted before
// segmentAllocation carried GuestAddress still yields the same address
// blockForIndex would have computed for it. Shares segmentGuestOffset with
// blockForIndex rather than restating the offset, so the two can't drift.
func guestAddressForCIDR(cidr string) (string, error) {
	_, block, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse segment CIDR %q: %w", cidr, err)
	}
	ip4 := block.IP.To4()
	if ip4 == nil {
		return "", fmt.Errorf("segment CIDR %q is not IPv4", cidr)
	}
	blockStart := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
	return ipv4FromUint32(blockStart + segmentGuestOffset), nil
}

// ipv4FromUint32 renders an IPv4 address held as a single uint32 (most
// significant octet first, matching segmentBaseBounds' packing) as
// dotted-quad text. The octets are taken through encoding/binary rather than
// a hand-rolled shift-and-truncate chain: the shifts are correct either way,
// but the
// explicit byte(x>>16) truncations read to gosec (G115) as unchecked integer
// conversions, and suppressing that on every octet would be noisier than
// simply not writing the conversion.
func ipv4FromUint32(addr uint32) string {
	var octets [4]byte
	binary.BigEndian.PutUint32(octets[:], addr)
	return net.IPv4(octets[0], octets[1], octets[2], octets[3]).String()
}

// indexForBlock recovers a persisted block's index within segmentBaseCIDR:
// the block size is fixed, so the offset from segmentBaseCIDR's base address
// divided by segmentBlockSize is the index. A CIDR that sorts below the base
// address, or beyond its last block, is reported as an error rather than
// allowed to underflow the unsigned subtraction (or overshoot) into a
// plausible-looking but wrong index -- reachable only from corrupt or
// hand-edited ledger state, but silent if unchecked.
func indexForBlock(cidr string) (int, error) {
	_, block, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	baseInt, blocks, err := segmentBaseBounds()
	if err != nil {
		return 0, err
	}
	blockIP4 := block.IP.To4()
	if blockIP4 == nil {
		return 0, fmt.Errorf("non-IPv4 CIDR %q", cidr)
	}
	blockInt := uint32(blockIP4[0])<<24 | uint32(blockIP4[1])<<16 | uint32(blockIP4[2])<<8 | uint32(blockIP4[3])
	if blockInt < baseInt {
		return 0, fmt.Errorf("CIDR %q sorts below segmentBaseCIDR %s", cidr, segmentBaseCIDR)
	}
	index := int((blockInt - baseInt) / segmentBlockSize)
	if index >= blocks {
		return 0, fmt.Errorf("CIDR %q is outside segmentBaseCIDR %s", cidr, segmentBaseCIDR)
	}
	return index, nil
}

func switchNameForSandbox(sandboxID string) string {
	return "boxy-sb-" + strings.ReplaceAll(sandboxID, " ", "")
}

// segments returns the Driver's single, shared *segmentLedger instance,
// constructing it exactly once. Building a fresh *segmentLedger (and
// therefore a fresh, unshared diskjson.Store/sync.Mutex) on every call would
// defeat the ledger's own concurrency guarantee: two concurrent
// CreateSegment/allocate calls would each lock their own independent mutex
// over the same underlying file, both could read the same stale snapshot,
// and the second Update's write would silently clobber the first's. Mirrors
// the ledgerStore/ledgerOnce pattern already used for the IP-range ledger.
//
// When New wasn't used to set segmentLedgerPath explicitly (e.g. a Driver
// built directly, as most tests in this package do), the ledger falls back to
// an ephemeral temp directory -- the same shape as ledger()'s own fallback,
// and for the same reason: a fixed relative filename resolved against the
// ambient process working directory would let unrelated Drivers converge on
// one shared file and race on each other's state.
func (d *Driver) segments() *segmentLedger {
	d.segmentLedgerOnce.Do(func() {
		d.segmentLedger = newSegmentLedger(d.resolveSegmentLedgerPath())
	})
	return d.segmentLedger
}

// resolveSegmentLedgerPath returns the configured segment ledger path, or an
// ephemeral per-Driver one when none was configured. See segments().
func (d *Driver) resolveSegmentLedgerPath() string {
	if d.segmentLedgerPath != "" {
		return d.segmentLedgerPath
	}
	dir, err := os.MkdirTemp("", "boxy-hyperv-segments-*")
	if err != nil {
		// os.MkdirTemp("", pattern) already resolves "" to os.TempDir()
		// internally, so this fallback dir is the same one that just failed
		// to yield a fresh subdirectory -- reusing the fixed
		// segmentLedgerFilename here would let every Driver whose MkdirTemp
		// call fails converge on one shared file, racing on state that has
		// nothing to do with each other. Make the fallback name unique too
		// (pid plus this Driver's own address can't collide within a single
		// machine) so a MkdirTemp failure degrades to "broken in isolation"
		// rather than "silently shared".
		return filepath.Join(os.TempDir(), fmt.Sprintf("boxy-hyperv-segments-%d-%p.json", os.Getpid(), d))
	}
	return filepath.Join(dir, segmentLedgerFilename)
}

// CreateSegment creates a dedicated Internal vSwitch + NAT for one sandbox.
// Internal, not Private: Private would also cut off the host-provided NAT
// (all internet access), which is the wrong default until egress policy
// exists to restrict it deliberately (see the design spec's Decision 1).
//
// The switch's host-side adapter is referenced directly by its deterministic
// name, "vEthernet (<switch name>)" — the name Hyper-V gives an Internal
// switch's host vNIC — rather than a fuzzy Get-NetAdapter | Where-Object
// -like lookup. The wildcard form
// was both a correctness bug (a substring match plus Select-Object -First 1
// can silently pick the wrong adapter, e.g. when one switch name is a
// substring of another's or of an unrelated host NIC) and a quoting defect
// (the pattern was interpolated into a double-quoted PowerShell string,
// which still expands $variables/backticks — psq() only protects
// single-quoted contexts, so it provided no real protection there
// regardless of input source). Task-2 code review findings 2 (and the
// review's separate Minor quoting-consistency note, resolved as a side
// effect) and the independent security scan finding on this same line.
//
// On failure, the sandbox's ledger entry is deliberately NOT released (see
// the comment on the error-handling branch below for why).
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	alloc, err := d.segments().allocate(sandboxID)
	if err != nil {
		return "", fmt.Errorf("allocate segment CIDR for sandbox %q: %w", sandboxID, err)
	}
	adapterAlias := fmt.Sprintf("vEthernet (%s)", alloc.SwitchName)
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue)) {
    New-VMSwitch -SwitchName '%s' -SwitchType Internal | Out-Null
}
if (-not (Get-NetIPAddress -InterfaceAlias '%s' -IPAddress '%s' -ErrorAction SilentlyContinue)) {
    New-NetIPAddress -IPAddress '%s' -PrefixLength %d -InterfaceAlias '%s' | Out-Null
}
if (-not (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue)) {
    New-NetNat -Name '%s' -InternalIPInterfaceAddressPrefix '%s' | Out-Null
}
`,
		psq(alloc.SwitchName), psq(alloc.SwitchName),
		psq(adapterAlias), psq(alloc.Gateway),
		psq(alloc.Gateway), segmentPrefixLen, psq(adapterAlias),
		psq(alloc.SwitchName), psq(alloc.SwitchName), psq(alloc.CIDR)))
	if err != nil {
		// Deliberately not releasing the ledger entry here (task-2 code
		// review finding 3): allocate()'s own idempotency contract exists
		// specifically so a retried CreateSegment for the same sandboxID
		// gets back the same CIDR/gateway/switch name, not a fresh one. If
		// the PowerShell step fails partway (e.g. New-VMSwitch succeeds but
		// New-NetNat fails) and this released the entry, a retry would
		// allocate a *different* CIDR while the partially-created
		// VMSwitch/IP from the first attempt is still bound to the *old*
		// one — the script's own "if not exists" idempotency checks would
		// then paper over a real mismatch instead of cleanly retrying
		// against the same identity. Retrying with the same ledger entry is
		// what makes this safe: the deterministic switch name and CIDR are
		// unchanged, so the "if not exists" checks above correctly resume
		// wherever the previous attempt left off.
		return "", fmt.Errorf("create segment for sandbox %q: %w", sandboxID, err)
	}
	return providersdk.SegmentRef(alloc.SwitchName), nil
}

// AttachToSegment moves an already-created, already-running VM's network
// adapter onto the sandbox's segment and then gives the guest a real address
// on that segment's subnet.
//
// The adapter move is the same live-reconnect PowerShell Driver.Create
// already uses to attach a new VM to its configured switch (driver.go's
// Create) -- no VM restart, single fast call.
//
// The in-guest address assignment that follows it is what makes the move
// useful (#224, Plan 1c): a segment's switch is Internal and issues no DHCP,
// so a VM reconnected onto it keeps whatever address it had on the pool's
// original switch -- wrong subnet, unreachable -- or falls back to APIPA.
// The address is sourced from the segment's own ledger entry rather than
// from any pool-declared network config, which is why hyperv's
// range/static_ip pool config was removed in the same change: isolation is
// automatic and universal for this driver, so the segment is always the
// guest's real, final network and a pool-declared address could only ever
// be stale. See ADR-0021's 2026-09-14 change-log entry.
//
// This makes the method heavier than ADR-0021's original "single lightweight
// reconnect" framing -- it now costs a Get-VM notes read, possibly a
// control-plane credential lookup, and a PSRP session (a real multi-second
// guest round trip, see #361). That is an accepted tradeoff, recorded in
// ADR-0021; there is no cheaper place to put it, because nothing else in the
// allocation sequence knows the segment's subnet.
//
// Idempotent, per providersdk.NetworkIsolator's contract: the reconnect is
// a no-op against the switch the VM is already on, the guest address is
// derived from a constant offset into the block rather than allocated, and
// assignGuestIP re-applies cleanly to an already-configured guest.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	vmName, err := d.vmNameFromID(ctx, providerResourceID)
	if err != nil {
		return fmt.Errorf("resolve VM name for %q: %w", providerResourceID, err)
	}
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Connect-VMNetworkAdapter -VMName '%s' -SwitchName '%s' | Out-Null`,
		psq(vmName), psq(string(ref))))
	if err != nil {
		return fmt.Errorf("attach %q to segment %q: %w", providerResourceID, ref, err)
	}
	if err := d.assignSegmentAddress(ctx, providerResourceID, ref); err != nil {
		return fmt.Errorf("address %q on segment %q: %w", providerResourceID, ref, err)
	}
	return nil
}

// assignSegmentAddress configures the guest's IPv4 identity for the segment
// it was just connected to, over PowerShell Direct (VMBus -- which needs no
// working guest network, and so still reaches a VM that the reconnect just
// stranded on a wrong-subnet address).
//
// A Linux guest is rejected the same way every other boxy-managed in-guest
// addressing path rejects one (guestIPUnsupportedOnLinux): PowerShell Direct
// is Windows-only and a Linux guest on an isolated segment needs its own
// mechanism (cloud-init or similar), out of scope here. This is a hard error
// rather than a skip -- an unaddressed guest on an isolated switch is simply
// broken, and providersdk.NetworkIsolator's skip semantics are about a
// driver not offering the capability at all, not about a resource the driver
// cannot finish isolating.
func (d *Driver) assignSegmentAddress(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	alloc, ok, err := d.segments().lookupBySwitchName(string(ref))
	if err != nil {
		return fmt.Errorf("read segment ledger: %w", err)
	}
	if !ok {
		return fmt.Errorf("no segment ledger entry records switch %q, so the guest's address on it is unknown", ref)
	}

	notes, err := d.readNotes(ctx, providerResourceID)
	if err != nil {
		return fmt.Errorf("read VM notes: %w", err)
	}
	guestOS := notes["boxy_guest_os"]
	if guestOS == "" {
		guestOS = "windows"
	}
	guestUser := notes["boxy_guest_user"]
	if guestUser == "" {
		if strings.EqualFold(guestOS, "linux") {
			guestUser = "admin"
		} else {
			guestUser = "Administrator"
		}
	}
	if strings.EqualFold(guestOS, "linux") {
		return guestIPUnsupportedOnLinux("segment IP assignment")
	}

	username, password, err := d.segmentGuestCredential(ctx, providerResourceID, notes, guestUser)
	if err != nil {
		return err
	}

	session, err := d.openGuestSession(ctx, providerResourceID, guestOS, username, password, "")
	if err != nil {
		return fmt.Errorf("open guest session: %w", err)
	}
	defer session.Close(ctx) //nolint:errcheck,gosec // best-effort close; assignGuestIP's own result is what's checked.

	// No DNS servers: a segment is an isolated network with no external
	// resolution to point at today. Revisit only if a concrete need arises.
	return d.assignGuestIP(ctx, session, guestOS, alloc.GuestAddress, strconv.Itoa(segmentPrefixLen), alloc.Gateway, "")
}

// DestroySegment removes the NAT and switch created by CreateSegment and
// frees the sandbox's ledger entry, returning its CIDR block for reuse.
// Idempotent: a segment already gone (both Get- calls find nothing, and no
// ledger entry carries this switch name) is not an error, matching
// Driver.Delete's contract.
//
// The ledger release happens here rather than in the caller: release is an
// unexported method on an unexported type reached through an unexported
// accessor, so code holding only a providersdk.NetworkIsolator interface
// value -- which is how every real consumer of this capability sees the
// driver -- has no way to invoke it. Doing it here also makes this method
// symmetric with the Docker driver's DestroySegment, which is likewise
// self-contained. The entry is matched by its recorded SwitchName rather
// than by inverting switchNameForSandbox to recover a sandbox ID; that
// derivation strips spaces and so is not invertible.
//
// The PowerShell teardown runs first and the release only on its success:
// the same preserve-on-failure reasoning CreateSegment documents applies in
// reverse, so a retry after a partial teardown still resolves the same
// switch/NAT identity rather than finding the entry already gone.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	switchName := string(ref)
	_, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-NetNat -Name '%s' -Confirm:$false | Out-Null
}
if (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-VMSwitch -Name '%s' -Force | Out-Null
}
`, psq(switchName), psq(switchName), psq(switchName), psq(switchName)))
	if err != nil {
		return fmt.Errorf("destroy segment %q: %w", ref, err)
	}
	if err := d.segments().releaseBySwitchName(switchName); err != nil {
		return fmt.Errorf("release segment ledger entry for %q: %w", ref, err)
	}
	return nil
}
