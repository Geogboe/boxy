// Package segmentcidr allocates non-overlapping address ranges for
// per-sandbox network segments.
//
// It exists because segment ranges have to be unique across every host a
// sandbox touches, not just within one host. Each host choosing its own
// range independently is what broke cross-host mesh peering (#370): two
// hosts allocating from the same base in the same order can hand out the
// same range, and a host then routes traffic addressed to the peer's
// segment locally instead of into the tunnel, since its own routing table
// has an equally valid, higher-priority local match. This is route
// ambiguity, not the separate "disallowed source address" WireGuard drop
// (#379) -- that one is caused by Docker's NAT masquerading traffic before
// encryption, and happens even when both ranges are unique.
//
// The allocator is deliberately stateless. Callers pass in the ranges
// already in use and get back one that isn't, so the "what is allocated"
// question stays answerable from whatever the caller already persists
// (for Boxy, the segments recorded on each sandbox) rather than from a
// second ledger that can drift from it.
package segmentcidr

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

// DefaultBase is the range per-sandbox blocks are carved from. Chosen in
// ADR-0021 for Hyper-V and reused here for every provider: it sits clear of
// Docker's own defaults (172.17.0.0/16 upward) and of the 10.0.x/10.1.x end
// of RFC 1918 where operator LANs usually live, so a block handed to a host
// is unlikely to collide with something already on it.
const DefaultBase = "10.250.0.0/16"

// DefaultBlockLen is the prefix length of one per-sandbox block. A /29 is 8
// addresses -- network, gateway, up to 5 usable hosts, broadcast -- which
// is enough for a small lab, and a /16 base yields 8192 of them.
const DefaultBlockLen = 29

// Allocator hands out non-overlapping blocks from a base range.
type Allocator struct {
	base     netip.Prefix
	blockLen int
}

// New returns an Allocator carving blockLen-sized blocks out of base.
func New(base string, blockLen int) (*Allocator, error) {
	p, err := netip.ParsePrefix(base)
	if err != nil {
		return nil, fmt.Errorf("parse base %q: %w", base, err)
	}
	if !p.Addr().Is4() {
		return nil, fmt.Errorf("base %q is not IPv4", base)
	}
	if blockLen < p.Bits() || blockLen > 32 {
		return nil, fmt.Errorf("block length /%d does not fit inside base %s", blockLen, base)
	}
	return &Allocator{base: p.Masked(), blockLen: blockLen}, nil
}

// NewDefault returns an Allocator over DefaultBase and DefaultBlockLen.
func NewDefault() *Allocator {
	a, err := New(DefaultBase, DefaultBlockLen)
	if err != nil {
		// Unreachable: both constants are validated by TestNewDefault.
		panic(fmt.Sprintf("segmentcidr: invalid defaults: %v", err))
	}
	return a
}

// Count is how many blocks the base range yields in total.
func (a *Allocator) Count() int {
	return 1 << (a.blockLen - a.base.Bits())
}

// Allocate returns the lowest-indexed block that does not overlap any of
// inUse, skipping additionally the blocks in avoid.
//
// avoid exists for the retry path: when a host refuses a proposed range
// because it collides with something only that host can see (a VPN route,
// an unrelated bridge), the caller re-allocates with the refused range in
// avoid. Refusals are per-attempt rather than persisted -- a permanent
// local conflict costs a few wasted proposals each time and self-corrects,
// where remembering it would be more state to go stale.
//
// Entries in inUse and avoid that don't parse are ignored rather than
// failing the allocation: a malformed record in persisted state should not
// make it impossible to create any new segment, and treating it as "not
// occupying anything" is the conservative direction -- the worst case is a
// proposal the host then refuses.
func (a *Allocator) Allocate(inUse []string, avoid []string) (string, error) {
	taken := make([]netip.Prefix, 0, len(inUse)+len(avoid))
	for _, s := range append(append([]string{}, inUse...), avoid...) {
		if p, err := netip.ParsePrefix(s); err == nil {
			taken = append(taken, p.Masked())
		}
	}

	for i := 0; i < a.Count(); i++ {
		block, err := a.blockAt(i)
		if err != nil {
			return "", err
		}
		if !overlapsAny(block, taken) {
			return block.String(), nil
		}
	}
	return "", fmt.Errorf("no free /%d block left in %s (%d total)", a.blockLen, a.base, a.Count())
}

// blockAt returns the index'th block counting up from the base address.
//
// index is bounded against Count() before any arithmetic rather than after,
// so an out-of-range value can't first wrap the uint32 offset around into
// unrelated address space and only then be caught by the containment check.
func (a *Allocator) blockAt(index int) (netip.Prefix, error) {
	if index < 0 || index >= a.Count() {
		return netip.Prefix{}, fmt.Errorf("block index %d is outside the %d blocks in %s", index, a.Count(), a.base)
	}

	addr := a.base.Addr().As4()
	size := uint32(1) << (32 - a.blockLen)
	// #nosec G115 -- index is bounded to [0, Count()) immediately above, and
	// Count() derives from the base's own prefix length, so the product
	// cannot exceed the address space the base spans.
	start := binary.BigEndian.Uint32(addr[:]) + uint32(index)*size

	var out [4]byte
	binary.BigEndian.PutUint32(out[:], start)
	block := netip.AddrFrom4(out)
	if !a.base.Contains(block) {
		return netip.Prefix{}, fmt.Errorf("block index %d falls outside base %s", index, a.base)
	}
	return netip.PrefixFrom(block, a.blockLen), nil
}

// OverlapsAny reports whether cidr shares any address with one of others.
// Entries that don't parse are ignored, matching Allocate's handling.
//
// Exported for NetworkIsolator implementations, which have to answer the
// same question against whatever their host already has (existing Docker
// networks, Get-NetNat prefixes, interface addresses) before accepting a
// proposed segment range.
func OverlapsAny(cidr string, others []string) bool {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false
	}
	parsed := make([]netip.Prefix, 0, len(others))
	for _, s := range others {
		if o, err := netip.ParsePrefix(s); err == nil {
			parsed = append(parsed, o.Masked())
		}
	}
	return overlapsAny(p.Masked(), parsed)
}

// overlapsAny reports whether p shares any address with one of others.
// Two prefixes overlap exactly when either contains the other's first
// address -- comparing containment both ways covers the case where one is
// larger than the other, which a single-direction check would miss (a /16
// in inUse against a /29 proposal, for instance).
func overlapsAny(p netip.Prefix, others []netip.Prefix) bool {
	for _, o := range others {
		if o.Contains(p.Addr()) || p.Contains(o.Addr()) {
			return true
		}
	}
	return false
}
