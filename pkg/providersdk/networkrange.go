// pkg/providersdk/networkrange.go
package providersdk

import "context"

// NetworkRange describes one IPv4 subnet a provider discovered bound to a
// named virtual switch/network on its host, as opposed to an
// operator-declared range in pool config. Fields are best-effort: a driver
// may be unable to determine Gateway or confirm NATBacked even when CIDR
// itself is known, so callers should not assume every field is populated.
type NetworkRange struct {
	// CIDR is the discovered IPv4 subnet in canonical network-address form
	// (e.g. "203.0.113.0/24").
	CIDR string `json:"cidr"`

	// Gateway is the host-side address on the switch's own interface within
	// CIDR, when known (e.g. "203.0.113.1"). Empty if undetermined.
	Gateway string `json:"gateway,omitempty"`

	// NATBacked reports whether the driver could positively confirm CIDR is
	// backed by a NAT rule (e.g. a Get-NetNat entry) rather than a plain
	// internal/private switch with no NAT. False means "not confirmed", not
	// "confirmed absent" — a driver that can't correlate NAT rules to a
	// switch at all should leave this false rather than guess.
	NATBacked bool `json:"nat_backed,omitempty"`
}

// NetworkRangeReporter is an optional provider capability for discovering
// the real IPv4 range(s) a named switch/network has bound on the host,
// distinct from whatever range an operator declared in pool config. Not
// every driver implements it — callers that care must type-assert, the same
// pattern as AvailabilityReporter, ResourceLister, and GuestPersonalizer.
//
// The intended use is a driver validating its own pool config against live
// host state before provisioning — not a general "hardware inventory"
// subsystem. A switch name that doesn't exist, or exists but has no
// discoverable IPv4 range (e.g. a Private switch with no host vNIC), is not
// itself an error: implementations should return (nil, nil) for "nothing
// found" and reserve a non-nil error for a query that could not be
// completed at all.
type NetworkRangeReporter interface {
	NetworkRanges(ctx context.Context, switchName string) ([]NetworkRange, error)
}
