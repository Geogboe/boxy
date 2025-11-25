package pool

import (
	"context"
	cryptoRand "crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/lifecycle/hooks"
	"github.com/Geogboe/boxy/pkg/provider"
)

// ResourceRepository defines the interface for resource persistence
type ResourceRepository interface {
	Create(ctx context.Context, res *provider.Resource) error
	Update(ctx context.Context, res *provider.Resource) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*provider.Resource, error)
	GetByPoolID(ctx context.Context, poolID string) ([]*provider.Resource, error)
	GetByState(ctx context.Context, poolID string, state provider.ResourceState) ([]*provider.Resource, error)
	CountByPoolAndState(ctx context.Context, poolID string, state provider.ResourceState) (int, error)
}

// Manager manages resource pools and their lifecycle
type Manager struct {
	config       *PoolConfig
	provider     provider.Provider
	repository   ResourceRepository
	hookExecutor *hooks.Executor
	logger       *logrus.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	asyncWg      sync.WaitGroup // Tracks async background operations
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
	// Apply defaults before validation
	config.ApplyDefaults()

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pool configuration: %w", err)
	}

	// Set default health check interval if not specified
	if config.HealthCheckInterval == 0 {
		config.HealthCheckInterval = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		config:       config,
		provider:     provider,
		repository:   repository,
		hookExecutor: hooks.NewExecutor(logger),
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		stopChan:     make(chan struct{}),
	}, nil
}

// Config returns a copy of the pool configuration.
func (m *Manager) Config() PoolConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
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

	// Initial replenishment (tracked in WaitGroup)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				m.logger.WithFields(logrus.Fields{
					"pool":  m.config.Name,
					"panic": r,
				}).Error("Panic in initial replenishment")
			}
		}()
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

	// Wait for all async background operations to complete
	m.logger.WithField("pool", m.config.Name).Debug("Waiting for async operations to complete")
	m.asyncWg.Wait()

	m.logger.WithField("pool", m.config.Name).Info("Pool manager stopped")
	return nil
}

// Allocate allocates a ready resource from the pool
func (m *Manager) Allocate(ctx context.Context, sandboxID string, expiresAt *time.Time) (*provider.Resource, error) {
	m.logger.WithFields(logrus.Fields{
		"pool":       m.config.Name,
		"sandbox_id": sandboxID,
	}).Debug("Allocating resource from pool")

	// Lock to prevent concurrent allocations from racing
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get ready resources
	ready, err := m.repository.GetByState(ctx, m.config.Name, provider.StateReady)
	if err != nil {
		return nil, fmt.Errorf("failed to get ready resources: %w", err)
	}

	// Filter for unallocated resources
	var available []*provider.Resource
	for _, res := range ready {
		if res.IsAvailable() {
			available = append(available, res)
		}
	}

	if len(available) == 0 {
		return nil, ErrNoResourcesAvailable
	}

	// Get the first available resource
	res := available[0]

	// Run on_allocate hooks (personalization) if any
	// ADR-008: Use OnAllocate instead of BeforeAllocate
	if len(m.config.Hooks.OnAllocate) > 0 {
		m.logger.WithFields(logrus.Fields{
			"pool":        m.config.Name,
			"resource_id": res.ID,
			"sandbox_id":  sandboxID,
		}).Info("Running personalization hooks")

		// TODO(mvp2): Support configurable credentials from pool config
		// For now, auto-generate credentials
		username := "boxy-user"
		password := generateRandomPassword(16)

		hookCtx := hooks.HookContext{
			ResourceID:   res.ID,
			ResourceIP:   getResourceIP(res.Metadata),
			ResourceType: string(res.Type),
			ProviderID:   res.ProviderID,
			PoolName:     m.config.Name,
			Username:     username,
			Password:     password,
			Metadata:     make(map[string]string),
		}

		results, err := m.hookExecutor.ExecuteHooks(
			ctx,
			m.config.Hooks.OnAllocate,
			hooks.HookPointOnAllocate,
			m.provider,
			res,
			hookCtx,
			m.config.Timeouts.Personalization,
		)

		// Store hook results and credentials in metadata
		if res.Metadata == nil {
			res.Metadata = make(map[string]interface{})
		}
		res.Metadata["personalization_hooks"] = results
		res.Metadata["allocated_username"] = username
		// Note: password is stored encrypted in provider metadata already

		if err != nil {
			m.logger.WithError(err).Error("Personalization hooks failed")
			// Don't mark resource as error, just return the error
			// Resource stays ready for next allocation attempt
			return nil, fmt.Errorf("personalization hooks failed: %w", err)
		}
	}

	// Mark as allocated
	res.SandboxID = &sandboxID
	res.State = provider.StateAllocated
	res.UpdatedAt = time.Now()

	//  Call provider-specific allocation artifacts (e.g., scratch provider's connect scripts)
	// This is optional - only scratch provider currently implements this
	type artifactAllocator interface {
		AllocateArtifacts(res *provider.Resource, sandboxID string, expiresAt *time.Time) error
	}
	if aa, ok := m.provider.(artifactAllocator); ok {
		if err := aa.AllocateArtifacts(res, sandboxID, expiresAt); err != nil {
			m.logger.WithError(err).Warn("Failed to allocate provider artifacts")
			// Don't fail allocation, just log warning
		}
	}

	if err := m.repository.Update(ctx, res); err != nil {
		return nil, fmt.Errorf("failed to update resource: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"pool":        m.config.Name,
		"resource_id": res.ID,
		"sandbox_id":  sandboxID,
	}).Info("Resource allocated")

	// Trigger replenishment asynchronously (tracked for graceful shutdown)
	m.asyncWg.Add(1)
	go func() {
		defer m.asyncWg.Done()
		defer func() {
			if r := recover(); r != nil {
				m.logger.WithFields(logrus.Fields{
					"pool":  m.config.Name,
					"panic": r,
				}).Error("Panic in async replenishment after allocation")
			}
		}()
		if err := m.ensureMinReady(m.ctx); err != nil {
			// Only log if not cancelled
			if ctx.Err() == nil {
				m.logger.WithError(err).Error("Failed to replenish pool after allocation")
			}
		}
	}()

	return res, nil
}

// generateRandomPassword generates a cryptographically secure random password
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	password := make([]byte, length)

	for i := range password {
		// Use crypto/rand for cryptographically secure random numbers
		idx, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// This should never happen unless system entropy is exhausted
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		password[i] = charset[idx.Int64()]
	}

	return string(password)
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
		res.State = provider.StateError
	} else {
		res.State = provider.StateDestroyed
	}

	res.SandboxID = nil
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	// Trigger replenishment (tracked for graceful shutdown)
	m.asyncWg.Add(1)
	go func() {
		defer m.asyncWg.Done()
		defer func() {
			if r := recover(); r != nil {
				m.logger.WithFields(logrus.Fields{
					"pool":  m.config.Name,
					"panic": r,
				}).Error("Panic in async replenishment after release")
			}
		}()
		if err := m.ensureMinReady(m.ctx); err != nil {
			// Only log if not cancelled
			if m.ctx.Err() == nil {
				m.logger.WithError(err).Error("Failed to replenish pool after release")
			}
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
	ready, err := m.repository.CountByPoolAndState(ctx, m.config.Name, provider.StateReady)
	if err != nil {
		return nil, err
	}
	stats.TotalReady = ready

	allocated, err := m.repository.CountByPoolAndState(ctx, m.config.Name, provider.StateAllocated)
	if err != nil {
		return nil, err
	}
	stats.TotalAllocated = allocated

	provisioning, err := m.repository.CountByPoolAndState(ctx, m.config.Name, provider.StateProvisioning)
	if err != nil {
		return nil, err
	}
	stats.TotalProvisioning = provisioning

	errorCount, err := m.repository.CountByPoolAndState(ctx, m.config.Name, provider.StateError)
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
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(logrus.Fields{
				"pool":  m.config.Name,
				"panic": r,
			}).Error("Panic in replenishment worker, worker terminated")
		}
	}()

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
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(logrus.Fields{
				"pool":  m.config.Name,
				"panic": r,
			}).Error("Panic in health check worker, worker terminated")
		}
	}()

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
	ready, err := m.repository.GetByState(ctx, m.config.Name, provider.StateReady)
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
		if res.State != provider.StateDestroyed {
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
	res := provider.NewResource(m.config.Name, spec.Type, spec.ProviderType)
	res.State = provider.StateProvisioning

	if err := m.repository.Create(ctx, res); err != nil {
		return fmt.Errorf("failed to create resource record: %w", err)
	}

	// Provision via provider with timeout
	provCtx, provCancel := context.WithTimeout(ctx, m.config.Timeouts.Provision)
	defer provCancel()

	provisioned, err := m.provider.Provision(provCtx, spec)
	if err != nil {
		// Mark as error
		res.State = provider.StateError
		res.Metadata = map[string]interface{}{
			"error": err.Error(),
		}
		_ = m.repository.Update(ctx, res)
		return fmt.Errorf("provider provisioning failed: %w", err)
	}

	// Update resource with provisioned data (still in provisioning state until hooks complete)
	res.ProviderID = provisioned.ProviderID
	res.Metadata = provisioned.Metadata
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		// Try to clean up provisioned resource
		_ = m.provider.Destroy(ctx, provisioned)
		return fmt.Errorf("failed to update resource after provisioning: %w", err)
	}

	// Run on_provision hooks (finalization)
	// ADR-008: Use OnProvision instead of AfterProvision
	if len(m.config.Hooks.OnProvision) > 0 {
		m.logger.WithFields(logrus.Fields{
			"pool":        m.config.Name,
			"resource_id": res.ID,
		}).Info("Running finalization hooks")

		hookCtx := hooks.HookContext{
			ResourceID:   res.ID,
			ResourceIP:   getResourceIP(provisioned.Metadata),
			ResourceType: string(res.Type),
			ProviderID:   res.ProviderID,
			PoolName:     m.config.Name,
			Metadata:     make(map[string]string),
		}

		results, err := m.hookExecutor.ExecuteHooks(
			ctx,
			m.config.Hooks.OnProvision,
			hooks.HookPointOnProvision,
			m.provider,
			res,
			hookCtx,
			m.config.Timeouts.Finalization,
		)

		// Store hook results in metadata
		if res.Metadata == nil {
			res.Metadata = make(map[string]interface{})
		}
		res.Metadata["finalization_hooks"] = results

		if err != nil {
			m.logger.WithError(err).Error("Finalization hooks failed")
			res.State = provider.StateError
			res.Metadata["error"] = fmt.Sprintf("finalization hooks failed: %v", err)
			_ = m.repository.Update(ctx, res)

			// Clean up resource since hooks failed
			_ = m.provider.Destroy(ctx, res)
			return fmt.Errorf("finalization hooks failed: %w", err)
		}
	}

	// Mark as ready
	res.State = provider.StateReady
	res.UpdatedAt = time.Now()

	if err := m.repository.Update(ctx, res); err != nil {
		// Try to clean up provisioned resource
		_ = m.provider.Destroy(ctx, res)
		return fmt.Errorf("failed to update resource after finalization: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"pool":        m.config.Name,
		"resource_id": res.ID,
	}).Info("Resource provisioned and ready")

	return nil
}

// getResourceIP extracts IP address from metadata
func getResourceIP(metadata map[string]interface{}) string {
	if ip, ok := metadata["ip_address"].(string); ok {
		return ip
	}
	return ""
}

// performHealthChecks checks health of all ready resources
func (m *Manager) performHealthChecks(ctx context.Context) error {
	ready, err := m.repository.GetByState(ctx, m.config.Name, provider.StateReady)
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
			res.State = provider.StateError
			res.Metadata["health_check_failed"] = true
			_ = m.repository.Update(ctx, res)

			// Destroy unhealthy resource (tracked for graceful shutdown)
			m.asyncWg.Add(1)
			go func(r *provider.Resource) {
				defer m.asyncWg.Done()
				defer func() {
					if rec := recover(); rec != nil {
						m.logger.WithFields(logrus.Fields{
							"pool":        m.config.Name,
							"resource_id": r.ID,
							"panic":       rec,
						}).Error("Panic in async destroy of unhealthy resource")
					}
				}()
				if err := m.provider.Destroy(ctx, r); err != nil {
					// Only log if not cancelled
					if ctx.Err() == nil {
						m.logger.WithError(err).Error("Failed to destroy unhealthy resource")
					}
				}
			}(res)
		}
	}

	return nil
}
