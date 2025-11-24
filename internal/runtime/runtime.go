package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	httpapi "github.com/Geogboe/boxy/internal/api/http"
	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider"
)

// Runtime holds running managers and resources to stop.
type Runtime struct {
	Pools        map[string]*pool.Manager
	Allocators   map[string]allocator.PoolAllocator
	SandboxMgr   *sandbox.Manager
	ResourceRepo pool.ResourceRepository
	Store        storage.Store
	APIStop      func()
	Registry     *provider.Registry
}

// Start initializes storage, encryption, providers, pool managers, sandbox manager, and API (if enabled).
func Start(ctx context.Context, cfg *config.Config, logger *logrus.Logger) (*Runtime, error) {
	// Storage
	store, err := storage.NewSQLiteStore(cfg.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	// Encryption
	encryptionKey, err := config.GetEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption key: %w", err)
	}
	encryptor, err := crypto.NewEncryptor(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Providers (local) and remote agent wiring
	providerRegistry := BuildRegistry(ctx, cfg.Pools, logger, encryptor)
	registerRemoteAgents(ctx, cfg, logger, providerRegistry)

	resourceRepo := storage.NewResourceRepositoryAdapter(store)

	// Pools
	poolManagers := make(map[string]*pool.Manager)
	poolAllocators := make(map[string]allocator.PoolAllocator)

	for _, poolCfg := range cfg.Pools {
		prov, ok := providerRegistry.Get(poolCfg.Backend)
		if !ok {
			logger.WithField("backend", poolCfg.Backend).Error("Provider not found, skipping pool")
			continue
		}
		manager, err := pool.NewManager(&poolCfg, prov, resourceRepo, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create pool manager for %s: %w", poolCfg.Name, err)
		}
		if err := manager.Start(); err != nil {
			return nil, fmt.Errorf("failed to start pool manager for %s: %w", poolCfg.Name, err)
		}
		poolManagers[poolCfg.Name] = manager
		poolAllocators[poolCfg.Name] = manager
		logger.WithField("pool", poolCfg.Name).Info("Pool manager started")
	}

	if len(poolManagers) == 0 {
		return nil, fmt.Errorf("no pools configured or all pools failed to start")
	}

	// Sandbox manager
	sandboxMgr := sandbox.NewManager(poolAllocators, store, store, providerRegistry, logger)
	sandboxMgr.Start()

	runtime := &Runtime{
		Pools:        poolManagers,
		Allocators:   poolAllocators,
		SandboxMgr:   sandboxMgr,
		ResourceRepo: resourceRepo,
		Store:        store,
		Registry:     providerRegistry,
	}

	// API
	if cfg.API.Enabled {
		apiTimeouts := httpapi.Timeouts{
			Read:  time.Duration(cfg.API.ReadTimeoutSecs) * time.Second,
			Write: time.Duration(cfg.API.WriteTimeoutSecs) * time.Second,
			Idle:  time.Duration(cfg.API.IdleTimeoutSecs) * time.Second,
		}
		apiServer := httpapi.NewServer(
			cfg.API.Listen,
			httpapi.NewSandboxManagerAdapter(sandboxMgr),
			httpapi.NewPoolStatsAdapter(poolManagers, resourceRepo, logger),
			logger,
			apiTimeouts,
		)
		apiCtx, apiCancel := context.WithCancel(context.Background())
		go func() {
			if err := apiServer.Run(apiCtx); err != nil {
				logger.WithError(err).Error("API server error")
			}
		}()
		runtime.APIStop = apiCancel
	} else {
		logger.Info("HTTP API disabled via config")
	}

	return runtime, nil
}

// Stop shuts down managers and storage.
func (r *Runtime) Stop(logger *logrus.Logger) {
	if r.APIStop != nil {
		r.APIStop()
	}
	if r.SandboxMgr != nil {
		r.SandboxMgr.Stop()
	}
	for name, mgr := range r.Pools {
		logger.WithField("pool", name).Info("Stopping pool manager")
		if err := mgr.Stop(); err != nil {
			logger.WithError(err).WithField("pool", name).Error("Error stopping pool manager")
		}
	}
	if r.Store != nil {
		if err := r.Store.Close(); err != nil {
			logger.WithError(err).Error("Error closing storage")
		}
	}
}
