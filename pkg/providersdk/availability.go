// pkg/providersdk/availability.go
package providersdk

import "context"

// ResourceAvailability is a driver's point-in-time view of how much of a
// resource it can currently hand out to a new Create request.
type ResourceAvailability struct {
	// MemoryMB is free memory available for new resources, in megabytes.
	MemoryMB int64
}

// AvailabilityReporter is an optional provider capability for reporting
// current resource headroom before a Create is attempted. Not every driver
// implements it — callers that care must type-assert, the same pattern as
// GuestPersonalizer.
type AvailabilityReporter interface {
	Availability(ctx context.Context) (*ResourceAvailability, error)
}
