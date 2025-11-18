package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/pkg/provider"
)

// PoolAllocator defines the interface for allocating resources from pools
type PoolAllocator interface {
	Allocate(ctx context.Context, sandboxID string) (*resource.Resource, error)
	Release(ctx context.Context, resourceID string) error
}

// SandboxRepository defines the interface for sandbox persistence
type SandboxRepository interface {
	CreateSandbox(ctx context.Context, sb *Sandbox) error
	UpdateSandbox(ctx context.Context, sb *Sandbox) error
	DeleteSandbox(ctx context.Context, id string) error
	GetSandboxByID(ctx context.Context, id string) (*Sandbox, error)
	ListSandboxes(ctx context.Context) ([]*Sandbox, error)
	ListActiveSandboxes(ctx context.Context) ([]*Sandbox, error)
	GetExpiredSandboxes(ctx context.Context) ([]*Sandbox, error)
}

// ResourceRepository defines the interface for resource access
type ResourceRepository interface {
	GetResourceByID(ctx context.Context, id string) (*resource.Resource, error)
	GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*resource.Resource, error)
	UpdateResource(ctx context.Context, res *resource.Resource) error
}

// Manager manages sandbox lifecycle
type Manager struct {
	pools          map[string]PoolAllocator
	sandboxRepo    SandboxRepository
	resourceRepo   ResourceRepository
	providers      *provider.Registry
	logger         *logrus.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	cleanupTicker  *time.Ticker
}

// NewManager creates a new sandbox manager
func NewManager(
	pools map[string]PoolAllocator,
	sandboxRepo SandboxRepository,
	resourceRepo ResourceRepository,
	providers *provider.Registry,
	logger *logrus.Logger,
) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		pools:        pools,
		sandboxRepo:  sandboxRepo,
		resourceRepo: resourceRepo,
		providers:    providers,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start begins background cleanup worker
func (m *Manager) Start() {
	m.logger.Info("Starting sandbox manager")

	m.cleanupTicker = time.NewTicker(30 * time.Second)

	m.wg.Add(1)
	go m.cleanupWorker()
}

// Stop gracefully stops the sandbox manager
func (m *Manager) Stop() {
	m.logger.Info("Stopping sandbox manager")

	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
	}

	m.cancel()
	m.wg.Wait()

	m.logger.Info("Sandbox manager stopped")
}

// Create creates a new sandbox
func (m *Manager) Create(ctx context.Context, req *CreateRequest) (*Sandbox, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	m.logger.WithFields(logrus.Fields{
		"name":      req.Name,
		"resources": len(req.Resources),
		"duration":  req.Duration,
	}).Info("Creating sandbox")

	// Create sandbox record
	sb := NewSandbox(req.Name, req.Duration)
	sb.Metadata = req.Metadata

	if err := m.sandboxRepo.CreateSandbox(ctx, sb); err != nil {
		return nil, fmt.Errorf("failed to create sandbox record: %w", err)
	}

	// Allocate resources from pools
	var allocatedIDs []string
	for _, resReq := range req.Resources {
		pool, ok := m.pools[resReq.PoolName]
		if !ok {
			// Cleanup already allocated resources
			m.cleanupPartialSandbox(ctx, sb.ID, allocatedIDs)
			return nil, fmt.Errorf("pool not found: %s", resReq.PoolName)
		}

		for i := 0; i < resReq.Count; i++ {
			res, err := pool.Allocate(ctx, sb.ID)
			if err != nil {
				m.logger.WithError(err).Errorf("Failed to allocate resource %d from pool %s", i+1, resReq.PoolName)
				// Cleanup
				m.cleanupPartialSandbox(ctx, sb.ID, allocatedIDs)
				return nil, fmt.Errorf("failed to allocate resources: %w", err)
			}
			allocatedIDs = append(allocatedIDs, res.ID)
		}
	}

	// Update sandbox with resource IDs
	sb.ResourceIDs = allocatedIDs
	sb.State = StateReady
	sb.UpdatedAt = time.Now()

	if err := m.sandboxRepo.UpdateSandbox(ctx, sb); err != nil {
		// Cleanup
		m.cleanupPartialSandbox(ctx, sb.ID, allocatedIDs)
		return nil, fmt.Errorf("failed to update sandbox: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"sandbox_id": sb.ID,
		"resources":  len(allocatedIDs),
	}).Info("Sandbox created successfully")

	return sb, nil
}

// Get retrieves a sandbox by ID
func (m *Manager) Get(ctx context.Context, id string) (*Sandbox, error) {
	return m.sandboxRepo.GetSandboxByID(ctx, id)
}

// List retrieves all active sandboxes
func (m *Manager) List(ctx context.Context) ([]*Sandbox, error) {
	return m.sandboxRepo.ListActiveSandboxes(ctx)
}

// Destroy destroys a sandbox and releases all resources
func (m *Manager) Destroy(ctx context.Context, id string) error {
	m.logger.WithField("sandbox_id", id).Info("Destroying sandbox")

	sb, err := m.sandboxRepo.GetSandboxByID(ctx, id)
	if err != nil {
		return fmt.Errorf("sandbox not found: %w", err)
	}

	if sb.State == StateDestroyed {
		return nil // Already destroyed
	}

	// Mark as expiring
	sb.State = StateExpiring
	sb.UpdatedAt = time.Now()
	_ = m.sandboxRepo.UpdateSandbox(ctx, sb)

	// Get all resources for this sandbox
	resources, err := m.resourceRepo.GetResourcesBySandboxID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get sandbox resources: %w", err)
	}

	// Release all resources
	var releaseErrors []error
	for _, res := range resources {
		pool, ok := m.pools[res.PoolID]
		if !ok {
			m.logger.WithField("pool_id", res.PoolID).Warn("Pool not found for resource release")
			continue
		}

		if err := pool.Release(ctx, res.ID); err != nil {
			m.logger.WithError(err).WithField("resource_id", res.ID).Error("Failed to release resource")
			releaseErrors = append(releaseErrors, err)
		}
	}

	// Mark sandbox as destroyed
	sb.State = StateDestroyed
	sb.UpdatedAt = time.Now()

	if err := m.sandboxRepo.UpdateSandbox(ctx, sb); err != nil {
		return fmt.Errorf("failed to update sandbox state: %w", err)
	}

	if len(releaseErrors) > 0 {
		m.logger.WithField("sandbox_id", id).Warn("Sandbox destroyed with some errors")
		return fmt.Errorf("sandbox destroyed with %d errors", len(releaseErrors))
	}

	m.logger.WithField("sandbox_id", id).Info("Sandbox destroyed successfully")
	return nil
}

// Extend extends the expiration time of a sandbox
func (m *Manager) Extend(ctx context.Context, id string, additionalDuration time.Duration) error {
	sb, err := m.sandboxRepo.GetSandboxByID(ctx, id)
	if err != nil {
		return fmt.Errorf("sandbox not found: %w", err)
	}

	if sb.State != StateReady {
		return fmt.Errorf("can only extend ready sandboxes")
	}

	if sb.ExpiresAt == nil {
		return fmt.Errorf("sandbox has no expiration time")
	}

	newExpiry := sb.ExpiresAt.Add(additionalDuration)
	sb.ExpiresAt = &newExpiry
	sb.UpdatedAt = time.Now()

	if err := m.sandboxRepo.UpdateSandbox(ctx, sb); err != nil {
		return fmt.Errorf("failed to extend sandbox: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"sandbox_id":  id,
		"new_expires": newExpiry,
	}).Info("Sandbox extended")

	return nil
}

// GetResourcesForSandbox retrieves all resources for a sandbox with connection info
func (m *Manager) GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*ResourceWithConnection, error) {
	resources, err := m.resourceRepo.GetResourcesBySandboxID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	var result []*ResourceWithConnection
	for _, res := range resources {
		// Get provider
		prov, ok := m.providers.Get(res.ProviderType)
		if !ok {
			m.logger.WithField("provider", res.ProviderType).Warn("Provider not found")
			continue
		}

		// Get connection info
		connInfo, err := prov.GetConnectionInfo(ctx, res)
		if err != nil {
			m.logger.WithError(err).WithField("resource_id", res.ID).Warn("Failed to get connection info")
			continue
		}

		result = append(result, &ResourceWithConnection{
			Resource:   res,
			Connection: connInfo,
		})
	}

	return result, nil
}

// cleanupWorker periodically checks for expired sandboxes
func (m *Manager) cleanupWorker() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(logrus.Fields{
				"panic": r,
			}).Error("Panic in sandbox cleanup worker, worker terminated")
		}
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.cleanupTicker.C:
			if err := m.cleanupExpired(m.ctx); err != nil {
				m.logger.WithError(err).Warn("Cleanup of expired sandboxes failed")
			}
		}
	}
}

// cleanupExpired destroys all expired sandboxes
func (m *Manager) cleanupExpired(ctx context.Context) error {
	expired, err := m.sandboxRepo.GetExpiredSandboxes(ctx)
	if err != nil {
		return err
	}

	if len(expired) == 0 {
		return nil
	}

	m.logger.WithField("count", len(expired)).Info("Cleaning up expired sandboxes")

	for _, sb := range expired {
		if err := m.Destroy(ctx, sb.ID); err != nil {
			m.logger.WithError(err).WithField("sandbox_id", sb.ID).Error("Failed to destroy expired sandbox")
		}
	}

	return nil
}

// cleanupPartialSandbox cleans up resources when sandbox creation fails
func (m *Manager) cleanupPartialSandbox(ctx context.Context, sandboxID string, resourceIDs []string) {
	m.logger.WithField("sandbox_id", sandboxID).Warn("Cleaning up partial sandbox")

	for _, resID := range resourceIDs {
		res, err := m.resourceRepo.GetResourceByID(ctx, resID)
		if err != nil {
			continue
		}

		pool, ok := m.pools[res.PoolID]
		if !ok {
			continue
		}

		_ = pool.Release(ctx, resID)
	}

	// Delete sandbox record
	_ = m.sandboxRepo.DeleteSandbox(ctx, sandboxID)
}

// ResourceWithConnection combines resource info with connection details
type ResourceWithConnection struct {
	Resource   *resource.Resource       `json:"resource"`
	Connection *resource.ConnectionInfo `json:"connection"`
}
