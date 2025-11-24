package sandbox

import "errors"

var (
	// Validation errors
	ErrNoResourcesRequested = errors.New("no resources requested")
	ErrInvalidDuration      = errors.New("duration must be positive")
	ErrInvalidPoolName      = errors.New("pool name cannot be empty")
	ErrInvalidResourceCount = errors.New("resource count must be positive")

	// Operational errors
	ErrSandboxNotFound       = errors.New("sandbox not found")
	ErrSandboxAlreadyExists  = errors.New("sandbox already exists")
	ErrSandboxExpired        = errors.New("sandbox has expired")
	ErrInsufficientResources = errors.New("insufficient resources available")
	ErrSandboxCreationFailed = errors.New("failed to create sandbox")
)
