package server

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/provider"
)

// SandboxRuntime holds lightweight state for sandbox CLI operations.
type SandboxRuntime struct {
	Allocators   map[string]allocator.PoolAllocator
	SandboxMgr   *sandbox.Manager
	ResourceRepo storage.ResourceRepository
	Store        storage.Store
	Pools        map[string]*pool.Manager
}

// StartSandboxRuntime builds pool managers and sandbox manager for ad-hoc sandbox operations.
func StartSandboxRuntime(ctx context.Context, cfg *config.Config, registry *provider.Registry, store storage.Store, logger *logrus.Logger) (*SandboxRuntime, error) {
	poolManagers := make(map[string]*pool.Manager)
	poolAllocators := make(map[string]allocator.PoolAllocator)

	for _, poolCfg := range cfg.Pools {
		prov, ok := registry.Get(poolCfg.Backend)
		if !ok {
			logger.WithField("backend", poolCfg.Backend).Warn("Provider not found, skipping pool")
			continue
		}
		manager, err := pool.NewManager(&poolCfg, prov, storage.NewResourceRepositoryAdapter(store), logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create pool manager for %s: %w", poolCfg.Name, err)
		}
		poolManagers[poolCfg.Name] = manager
		poolAllocators[poolCfg.Name] = manager
	}

	if len(poolManagers) == 0 {
		return nil, fmt.Errorf("no pools configured or all pools failed to start")
	}

	sbMgr := sandbox.NewManager(poolAllocators, store, store, registry, logger)
	sbMgr.Start()

	return &SandboxRuntime{
		Allocators:   poolAllocators,
		SandboxMgr:   sbMgr,
		ResourceRepo: store,
		Store:        store,
		Pools:        poolManagers,
	}, nil
}

// Stop shuts down managers; storage is closed by caller.
func (r *SandboxRuntime) Stop(logger *logrus.Logger) {
	if r.SandboxMgr != nil {
		r.SandboxMgr.Stop()
	}
	for name, mgr := range r.Pools {
		logger.WithField("pool", name).Info("Stopping pool manager")
		if err := mgr.Stop(); err != nil {
			logger.WithError(err).WithField("pool", name).Error("Error stopping pool manager")
		}
	}
}

type ResourceRequest = sandbox.ResourceRequest

type CreateSandboxRequest struct {
	Name      string
	Resources []sandbox.ResourceRequest
	Duration  time.Duration
}

// SandboxView is a thin view for CLI output.
type SandboxView struct {
	ID          string
	Name        string
	State       sandbox.SandboxState
	ResourceIDs []string
	ExpiresAt   *time.Time
}

func (s SandboxView) TimeRemaining() time.Duration {
	if s.ExpiresAt == nil {
		return 0
	}
	rem := time.Until(*s.ExpiresAt)
	if rem < 0 {
		return 0
	}
	return rem
}

// ResourceWithConn bundles resource and connection info.
type ResourceWithConn struct {
	Resource   *provider.Resource
	Connection *provider.ConnectionInfo
}

func CreateSandbox(ctx context.Context, rt *SandboxRuntime, req CreateSandboxRequest) (SandboxView, error) {
	sbReq := &sandbox.CreateRequest{
		Name:      req.Name,
		Resources: req.Resources,
		Duration:  req.Duration,
	}
	sb, err := rt.SandboxMgr.Create(ctx, sbReq)
	if err != nil {
		return SandboxView{}, err
	}
	return toView(sb), nil
}

func WaitSandboxReady(ctx context.Context, rt *SandboxRuntime, id string, timeout time.Duration) (SandboxView, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sb, err := rt.SandboxMgr.WaitForReady(ctx, id, timeout)
	if err != nil {
		return SandboxView{}, err
	}
	return toView(sb), nil
}

func GetSandboxResources(ctx context.Context, rt *SandboxRuntime, sandboxID string) ([]ResourceWithConn, error) {
	resources, err := rt.SandboxMgr.GetResourcesForSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	out := make([]ResourceWithConn, 0, len(resources))
	for _, rwc := range resources {
		out = append(out, ResourceWithConn{
			Resource:   rwc.Resource,
			Connection: rwc.Connection,
		})
	}
	return out, nil
}

func DestroySandbox(ctx context.Context, store storage.Store, sandboxID string) error {
	// New manager per call to re-use storage; minimal wiring.
	sbRepo := storage.NewSandboxRepositoryAdapter(store)
	sbMgr := sandbox.NewManager(nil, sbRepo, store, nil, logrus.New())
	sbMgr.Start()
	defer sbMgr.Stop()

	return sbMgr.Destroy(ctx, sandboxID)
}

func ListSandboxes(ctx context.Context, store storage.Store) ([]SandboxView, error) {
	sbRepo := storage.NewSandboxRepositoryAdapter(store)
	list, err := sbRepo.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SandboxView, 0, len(list))
	for _, sb := range list {
		out = append(out, toView(sb))
	}
	return out, nil
}

func toView(sb *sandbox.Sandbox) SandboxView {
	return SandboxView{
		ID:          sb.ID,
		Name:        sb.Name,
		State:       sb.State,
		ResourceIDs: sb.ResourceIDs,
		ExpiresAt:   sb.ExpiresAt,
	}
}
