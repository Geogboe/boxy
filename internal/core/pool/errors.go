package pool

import "errors"

var (
	// Configuration errors
	ErrInvalidPoolName     = errors.New("invalid pool name")
	ErrInvalidResourceType = errors.New("invalid resource type")
	ErrInvalidBackend      = errors.New("invalid backend provider")
	ErrInvalidImage        = errors.New("invalid image")
	ErrInvalidMinReady     = errors.New("min_ready must be >= 0")
	ErrInvalidMaxTotal     = errors.New("max_total must be >= min_ready")

	// Pool operation errors
	ErrPoolNotFound        = errors.New("pool not found")
	ErrPoolAlreadyExists   = errors.New("pool already exists")
	ErrPoolAtCapacity      = errors.New("pool at maximum capacity")
	ErrNoResourcesAvailable = errors.New("no resources available in pool")
	ErrPoolNotHealthy      = errors.New("pool is not healthy")

	// Resource operation errors
	ErrResourceNotFound    = errors.New("resource not found")
	ErrResourceNotReady    = errors.New("resource is not ready")
	ErrResourceAllocated   = errors.New("resource is already allocated")
	ErrProvisioningFailed  = errors.New("resource provisioning failed")
)
