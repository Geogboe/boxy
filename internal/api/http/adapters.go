package httpapi

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/pkg/provider"
)

// SandboxManagerAdapter wraps the real sandbox manager to satisfy SandboxService.
type SandboxManagerAdapter struct {
	manager *sandbox.Manager
}

func NewSandboxManagerAdapter(mgr *sandbox.Manager) *SandboxManagerAdapter {
	return &SandboxManagerAdapter{manager: mgr}
}

func (a *SandboxManagerAdapter) Create(ctx context.Context, req *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
	return a.manager.Create(ctx, req)
}
func (a *SandboxManagerAdapter) List(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return a.manager.List(ctx)
}
func (a *SandboxManagerAdapter) Get(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	return a.manager.Get(ctx, id)
}
func (a *SandboxManagerAdapter) Destroy(ctx context.Context, id string) error {
	return a.manager.Destroy(ctx, id)
}
func (a *SandboxManagerAdapter) Extend(ctx context.Context, id string, d time.Duration) error {
	return a.manager.Extend(ctx, id, d)
}
func (a *SandboxManagerAdapter) GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*sandbox.ResourceWithConnection, error) {
	return a.manager.GetResourcesForSandbox(ctx, sandboxID)
}

// PoolStatsAdapter collects pool status using managers and the shared resource repository.
type PoolStatsAdapter struct {
	managers map[string]*pool.Manager
	repo     pool.ResourceRepository
	logger   *logrus.Logger
}

func NewPoolStatsAdapter(managers map[string]*pool.Manager, repo pool.ResourceRepository, logger *logrus.Logger) *PoolStatsAdapter {
	return &PoolStatsAdapter{
		managers: managers,
		repo:     repo,
		logger:   logger,
	}
}

func (a *PoolStatsAdapter) List() ([]PoolStatus, error) {
	ctx := context.Background()
	var out []PoolStatus

	for name, mgr := range a.managers {
		cfg := mgr.Config()

		ready, err := a.repo.CountByPoolAndState(ctx, name, provider.StateReady)
		if err != nil {
			a.logger.WithError(err).WithField("pool", name).Warn("failed to count ready resources")
		}
		allocated, err := a.repo.CountByPoolAndState(ctx, name, provider.StateAllocated)
		if err != nil {
			a.logger.WithError(err).WithField("pool", name).Warn("failed to count allocated resources")
		}
		provisioning, err := a.repo.CountByPoolAndState(ctx, name, provider.StateProvisioning)
		if err != nil {
			a.logger.WithError(err).WithField("pool", name).Warn("failed to count provisioning resources")
		}
		destroying, err := a.repo.CountByPoolAndState(ctx, name, provider.StateDestroying)
		if err != nil {
			a.logger.WithError(err).WithField("pool", name).Warn("failed to count destroying resources")
		}
		errCount, err := a.repo.CountByPoolAndState(ctx, name, provider.StateError)
		if err != nil {
			a.logger.WithError(err).WithField("pool", name).Warn("failed to count error resources")
		}

		out = append(out, PoolStatus{
			Name:         cfg.Name,
			Backend:      cfg.Backend,
			MinReady:     cfg.MinReady,
			MaxTotal:     cfg.MaxTotal,
			Ready:        ready,
			Allocated:    allocated,
			Provisioning: provisioning,
			Destroying:   destroying,
			Error:        errCount,
		})
	}

	return out, nil
}
