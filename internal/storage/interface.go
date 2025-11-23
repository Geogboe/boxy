package storage

import (
	"context"

	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/pkg/provider"
)

// ResourceRepository defines the interface for resource storage operations
type ResourceRepository interface {
	CreateResource(ctx context.Context, res *provider.Resource) error
	UpdateResource(ctx context.Context, res *provider.Resource) error
	DeleteResource(ctx context.Context, id string) error
	GetResourceByID(ctx context.Context, id string) (*provider.Resource, error)
	GetResourcesByPoolID(ctx context.Context, poolID string) ([]*provider.Resource, error)
	GetResourcesByState(ctx context.Context, poolID string, state provider.ResourceState) ([]*provider.Resource, error)
	CountResourcesByPoolAndState(ctx context.Context, poolID string, state provider.ResourceState) (int, error)
	GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*provider.Resource, error)
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
