package providersdk

import "fmt"

// CapacityError indicates the host does not currently have enough available
// capacity (e.g. memory) to satisfy a Create request. Provider-neutral: any
// driver can return one, not just Hyper-V, which aliases this type — see
// pkg/providersdk/providers/hyperv/driver.go.
type CapacityError struct {
	RequestedMemoryMB int64
	AvailableMemoryMB int64
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf(
		"insufficient host capacity: requested %d MB, %d MB available",
		e.RequestedMemoryMB, e.AvailableMemoryMB,
	)
}

// ErrorType implements ErrorTyper.
func (e *CapacityError) ErrorType() string { return "capacity" }

// OrphanedResourceError indicates Create failed and best-effort cleanup of
// the partially-created resource also failed, leaving it on the underlying
// host outside Boxy's inventory. ID is the provider-native identifier — the
// same convention every successfully created Resource uses — so a caller
// can record a quarantined resource and retry destroying it later.
// CauseMessage is a plain string, not a wrapped error, so this type
// round-trips through json.Marshal/json.Unmarshal across the
// RemoteAgent/gRPC boundary (see #185) — an error interface's concrete type
// usually can't survive that.
type OrphanedResourceError struct {
	ID           string
	CauseMessage string
}

func (e *OrphanedResourceError) Error() string {
	return fmt.Sprintf("resource %q orphaned after create failure and cleanup failure: %s", e.ID, e.CauseMessage)
}

// ErrorType implements ErrorTyper.
func (e *OrphanedResourceError) ErrorType() string { return "orphaned_resource" }

// CIDRConflictError indicates a NetworkIsolator refused a caller-proposed
// segment CIDR because that range is already in use on this host by
// something the caller cannot see -- another Docker network, an existing
// NAT prefix, a host interface address, a VPN route. The caller is expected
// to propose a different range rather than treat this as fatal.
//
// It exists because segment CIDR allocation is split across two parties
// that each hold half the information (#370): the daemon knows which ranges
// are in use across *all* hosts, which is what cross-host mesh peering
// requires, but only the agent knows what else already occupies that range
// on its own host. Both collision types are real and neither side can rule
// out the other's alone.
//
// ConflictingWith is a human-readable description of what the range
// collided with, for diagnostics -- it is deliberately not structured,
// since every provider reports a different kind of object. Both fields are
// plain strings so the type round-trips through json.Marshal across the
// RemoteAgent/gRPC boundary, same as OrphanedResourceError.
type CIDRConflictError struct {
	RequestedCIDR   string
	ConflictingWith string
}

func (e *CIDRConflictError) Error() string {
	return fmt.Sprintf("proposed segment CIDR %s conflicts with %s on this host", e.RequestedCIDR, e.ConflictingWith)
}

// ErrorType implements ErrorTyper.
func (e *CIDRConflictError) ErrorType() string { return "cidr_conflict" }
