package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/pkg/provider"
)

// ResourceRepository defines the interface for resource persistence
type ResourceRepository interface {
	Create(ctx context.Context, res *resource.Resource) error
	Update(ctx context.Context, res *resource.Resource) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*resource.Resource, error)
	GetByPoolID(ctx context.Context, poolID string) ([]*resource.Resource, error)
	GetByState(ctx context.Context, poolID string, state resource.ResourceState) ([]*resource.Resource, error)
	CountByPoolAndState(ctx context.Context, poolID string, state resource.ResourceState) (int, error)
}

// Manager manages resource pools and their lifecycle
type Manager struct {
	config       *PoolConfig
	provider     provider.Provider
	repository   ResourceRepository
	logger       *logrus.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.RWMutex
	running      bool
	stopChan     chan struct{}
}

// NewManager creates a new pool manager
func NewManager(
	config *PoolConfig,
	provider provider.Provider,
	repository ResourceRepository,
	logger *logrus.Logger,
) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pool configuration: %w", err)
	}

	// Set default health check interval if not specified
	if config.HealthCheckInterval == 0 {
		config.HealthCheckInterval = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		config:     config,
		provider:   provider,
		repository: repository,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		stopChan:   make(chan struct{}),
	}, nil
}

// Start begins the warm pool maintenance goroutines
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("pool manager already running")
	}

	m.logger.WithFields(logrus.Fields{
		"pool":      m.config.Name,
		"min_ready": m.config.MinReady,
		"max_total": m.config.MaxTotal,
	}).Info("Starting pool manager")

	m.running = true

	// Start replenishment worker
	m.wg.Add(1)
	go m.replenishmentWorker()

	// Start health check worker
	m.wg.Add(1)
	go m.healthCheckWorker()

	// Initial replenishment
	go func() {
		if err := m.ensureMinReady(m.ctx); err != nil {
			m.logger.WithError(err).Error("Initial replenishment failed")
		}
	}()

	return nil
}

// Stop gracefully stops the pool manager
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.logger.WithField("pool", m.config.Name).Info("Stopping pool manager")

	m.running = false
	m.cancel()
	close(m.stopChan)

	// Wait for workers to finish
	m.wg.Wait()

	m.logger.WithField("pool", m.config.Name).Info("Pool manager stopped")
	return nil
}

// Allocate allocates a ready resource from the pool
func (m *Manager) Allocate(ctx context.Context, sandboxID string) (*resource.Resource, error) {
	m.logger.WithFields(logrus.Fields{
		"pool":       m.config.Name,
		"sandbox_id": sandboxID,
	}).Debug("Allocating resource from pool")

	// Get ready resources
	ready, err := m.repository.GetByState(ctx, m.config.Name, resource.StateReady)
	if err != nil {
		return nil, fmt.Errorf("failed to get ready resources: %w", err)
	}

	// Filter for unallocated resources
	var available []*resource.Resource
	for _, res := range ready {
		if res.IsAvailable() {
			available = append(available, res)
		}
	}

	if len(available) == 0 {
		return nil, ErrNoResourcesAvailable
	}

	// Allocate the first available resource
	res := available[0]
	res.SandboxID = &sandboxID
	res.State = resource.StateAllocated
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		return nil, fmt.Errorf("failed to update resource: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"pool":        m.config.Name,
		"resource_id": res.ID,
		"sandbox_id":  sandboxID,
	}).Info("Resource allocated")

	// Trigger replenishment asynchronously
	go func() {
		if err := m.ensureMinReady(m.ctx); err != nil {
			m.logger.WithError(err).Error("Failed to replenish pool after allocation")
		}
	}()

	return res, nil
}

// Release releases a resource back to the pool
func (m *Manager) Release(ctx context.Context, resourceID string) error {
	res, err := m.repository.GetByID(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	if res.PoolID != m.config.Name {
		return fmt.Errorf("resource does not belong to this pool")
	}

	m.logger.WithFields(logrus.Fields{
		"pool":        m.config.Name,
		"resource_id": resourceID,
	}).Info("Releasing resource")

	// Destroy the resource (we don't reuse resources for security)
	if err := m.provider.Destroy(ctx, res); err != nil {
		m.logger.WithError(err).Error("Failed to destroy resource during release")
		// Mark as error but continue
		res.State = resource.StateError
	} else {
		res.State = resource.StateDestroyed
	}

	res.SandboxID = nil
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	// Trigger replenishment
	go func() {
		if err := m.ensureMinReady(m.ctx); err != nil {
			m.logger.WithError(err).Error("Failed to replenish pool after release")
		}
	}()

	return nil
}

// GetStats returns current pool statistics
func (m *Manager) GetStats(ctx context.Context) (*PoolStats, error) {
	stats := &PoolStats{
		Name:       m.config.Name,
		MinReady:   m.config.MinReady,
		MaxTotal:   m.config.MaxTotal,
		LastUpdate: time.Now(),
	}

	// Count resources by state
	ready, err := m.repository.CountByPoolAndState(ctx, m.config.Name, resource.StateReady)
	if err != nil {
		return nil, err
	}
	stats.TotalReady = ready

	allocated, err := m.repository.CountByPoolAndState(ctx, m.config.Name, resource.StateAllocated)
	if err != nil {
		return nil, err
	}
	stats.TotalAllocated = allocated

	provisioning, err := m.repository.CountByPoolAndState(ctx, m.config.Name, resource.StateProvisioning)
	if err != nil {
		return nil, err
	}
	stats.TotalProvisioning = provisioning

	errorCount, err := m.repository.CountByPoolAndState(ctx, m.config.Name, resource.StateError)
	if err != nil {
		return nil, err
	}
	stats.TotalError = errorCount

	stats.Total = stats.TotalReady + stats.TotalAllocated + stats.TotalProvisioning + stats.TotalError
	stats.Healthy = stats.TotalReady >= m.config.MinReady

	return stats, nil
}

// replenishmentWorker continuously ensures min_ready count is maintained
func (m *Manager) replenishmentWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			if err := m.ensureMinReady(m.ctx); err != nil {
				m.logger.WithError(err).Warn("Replenishment check failed")
			}
		}
	}
}

// healthCheckWorker periodically checks resource health
func (m *Manager) healthCheckWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			if err := m.performHealthChecks(m.ctx); err != nil {
				m.logger.WithError(err).Warn("Health check failed")
			}
		}
	}
}

// ensureMinReady provisions resources until min_ready count is reached
func (m *Manager) ensureMinReady(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Count ready resources
	ready, err := m.repository.GetByState(ctx, m.config.Name, resource.StateReady)
	if err != nil {
		return fmt.Errorf("failed to get ready resources: %w", err)
	}

	// Filter for actually available resources
	availableCount := 0
	for _, res := range ready {
		if res.IsAvailable() {
			availableCount++
		}
	}

	needed := m.config.MinReady - availableCount
	if needed <= 0 {
		return nil // Already have enough
	}

	// Check total capacity
	all, err := m.repository.GetByPoolID(ctx, m.config.Name)
	if err != nil {
		return fmt.Errorf("failed to get all pool resources: %w", err)
	}

	// Count non-destroyed resources
	activeCount := 0
	for _, res := range all {
		if res.State != resource.StateDestroyed {
			activeCount++
		}
	}

	if activeCount+needed > m.config.MaxTotal {
		needed = m.config.MaxTotal - activeCount
		if needed <= 0 {
			m.logger.WithField("pool", m.config.Name).Warn("Pool at max capacity, cannot replenish")
			return ErrPoolAtCapacity
		}
	}

	m.logger.WithFields(logrus.Fields{
		"pool":      m.config.Name,
		"needed":    needed,
		"available": availableCount,
		"min_ready": m.config.MinReady,
	}).Info("Replenishing pool")

	// Provision needed resources
	for i := 0; i < needed; i++ {
		if err := m.provisionOne(ctx); err != nil {
			m.logger.WithError(err).Error("Failed to provision resource")
			// Continue trying to provision others
		}
	}

	return nil
}

// provisionOne provisions a single resource
func (m *Manager) provisionOne(ctx context.Context) error {
	spec := m.config.ToResourceSpec()

	m.logger.WithFields(logrus.Fields{
		"pool":  m.config.Name,
		"image": spec.Image,
	}).Debug("Provisioning resource")

	// Create resource in provisioning state
	res := resource.NewResource(m.config.Name, spec.Type, spec.ProviderType)
	res.State = resource.StateProvisioning

	if err := m.repository.Create(ctx, res); err != nil {
		return fmt.Errorf("failed to create resource record: %w", err)
	}

	// Provision via provider
	provisioned, err := m.provider.Provision(ctx, spec)
	if err != nil {
		// Mark as error
		res.State = resource.StateError
		res.Metadata = map[string]interface{}{
			"error": err.Error(),
		}
		_ = m.repository.Update(ctx, res)
		return fmt.Errorf("provider provisioning failed: %w", err)
	}

	// Update resource with provisioned data
	res.ProviderID = provisioned.ProviderID
	res.State = resource.StateReady
	res.Metadata = provisioned.Metadata
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		// Try to clean up provisioned resource
		_ = m.provider.Destroy(ctx, provisioned)
		return fmt.Errorf("failed to update resource after provisioning: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"pool":        m.config.Name,
		"resource_id": res.ID,
	}).Info("Resource provisioned and ready")

	return nil
}

// performHealthChecks checks health of all ready resources
func (m *Manager) performHealthChecks(ctx context.Context) error {
	ready, err := m.repository.GetByState(ctx, m.config.Name, resource.StateReady)
	if err != nil {
		return err
	}

	for _, res := range ready {
		status, err := m.provider.GetStatus(ctx, res)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"resource_id": res.ID,
				"error":       err,
			}).Warn("Health check failed")
			continue
		}

		// If unhealthy, mark for destruction
		if !status.Healthy {
			m.logger.WithField("resource_id", res.ID).Warn("Resource unhealthy, marking for destruction")
			res.State = resource.StateError
			res.Metadata["health_check_failed"] = true
			_ = m.repository.Update(ctx, res)

			// Destroy unhealthy resource
			go func(r *resource.Resource) {
				if err := m.provider.Destroy(ctx, r); err != nil {
					m.logger.WithError(err).Error("Failed to destroy unhealthy resource")
				}
			}(res)
		}
	}

	return nil
}
