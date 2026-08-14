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
