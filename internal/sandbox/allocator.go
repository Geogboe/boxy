package sandbox

import (
	"context"

	"github.com/Geogboe/boxy/pkg/model"
)

// SandboxAllocator is called when resources are allocated to a sandbox.
// It runs provider-level allocation hooks and returns additional Properties
// to merge into the resource at allocation time.
//
// This is separate from pool.Provisioner, which handles supply-side lifecycle
// (creating and destroying resources in the pool).
type SandboxAllocator interface {
	Allocate(ctx context.Context, pool model.Pool, res model.Resource) (map[string]any, error)
}
