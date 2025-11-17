package storage

import (
	"context"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/core/sandbox"
)

// ResourceRepository defines the interface for resource storage operations
type ResourceRepository interface {
	CreateResource(ctx context.Context, res *resource.Resource) error
	UpdateResource(ctx context.Context, res *resource.Resource) error
	DeleteResource(ctx context.Context, id string) error
	GetResourceByID(ctx context.Context, id string) (*resource.Resource, error)
	GetResourcesByPoolID(ctx context.Context, poolID string) ([]*resource.Resource, error)
	GetResourcesByState(ctx context.Context, poolID string, state resource.ResourceState) ([]*resource.Resource, error)
	CountResourcesByPoolAndState(ctx context.Context, poolID string, state resource.ResourceState) (int, error)
	GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*resource.Resource, error)
}

// SandboxRepository defines the interface for sandbox storage operations
type SandboxRepository interface {
	CreateSandbox(ctx context.Context, sb *sandbox.Sandbox) error
	UpdateSandbox(ctx context.Context, sb *sandbox.Sandbox) error
	DeleteSandbox(ctx context.Context, id string) error
	GetSandboxByID(ctx context.Context, id string) (*sandbox.Sandbox, error)
	ListSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error)
	ListActiveSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error)
	GetExpiredSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error)
}

// Store combines all storage interfaces
type Store interface {
	ResourceRepository
	SandboxRepository
	Close() error
}
