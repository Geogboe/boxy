package store

import (
	"context"
	"errors"

	"github.com/Geogboe/boxy/v2/pkg/model"
)

var ErrNotFound = errors.New("not found")

// Store is the minimal persistence surface the runtime managers need.
//
// This is intentionally small and may be split into narrower interfaces later.
type Store interface {
	// Pools
	GetPool(ctx context.Context, name model.PoolName) (model.Pool, error)
	PutPool(ctx context.Context, pool model.Pool) error

	// Resources
	GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error)
	PutResource(ctx context.Context, res model.Resource) error

	// Sandboxes
	GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error)
	CreateSandbox(ctx context.Context, sb model.Sandbox) error
	PutSandbox(ctx context.Context, sb model.Sandbox) error
	DeleteSandbox(ctx context.Context, id model.SandboxID) error

	// List operations return all entities of a given type.
	// An empty store returns a non-nil, zero-length slice.
	ListPools(ctx context.Context) ([]model.Pool, error)
	ListResources(ctx context.Context) ([]model.Resource, error)
	ListSandboxes(ctx context.Context) ([]model.Sandbox, error)
}
