package devfactory

import "github.com/Geogboe/boxy/pkg/providersdk"

// ExecOp is the devfactory spelling of the shared command operation.
type ExecOp = providersdk.ExecOperation

// SetStateOp changes the simulated state of a resource.
// Useful for testing state transitions (e.g. simulating a crash).
type SetStateOp struct {
	State string
}
