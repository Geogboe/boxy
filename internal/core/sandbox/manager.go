package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/pkg/provider"
)

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
	GetResourceByID(ctx context.Context, id string) (*provider.Resource, error)
	GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*provider.Resource, error)
	UpdateResource(ctx context.Context, res *provider.Resource) error
}

// Manager manages sandbox lifecycle
type Manager struct {
	allocator     *allocator.Allocator
	sandboxRepo   SandboxRepository
	resourceRepo  ResourceRepository
	providers     *provider.Registry
	logger        *logrus.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	cleanupTicker *time.Ticker
}

// NewManager creates a new sandbox manager
func NewManager(
	pools map[string]allocator.PoolAllocator,
	sandboxRepo SandboxRepository,
	resourceRepo ResourceRepository,
	providers *provider.Registry,
	logger *logrus.Logger,
) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	alloc := allocator.New(pools, resourceRepo, logger)

	return &Manager{
		allocator:    alloc,
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
// The sandbox is created in StateCreating and resources are allocated asynchronously.
// Use Get() or WaitForReady() to check when allocation completes.
func (m *Manager) Create(ctx context.Context, req *CreateRequest) (*Sandbox, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	m.logger.WithFields(logrus.Fields{
		"name":      req.Name,
		"resources": len(req.Resources),
		"duration":  req.Duration,
	}).Info("Creating sandbox")

	// Validate pools exist before creating sandbox
	for _, resReq := range req.Resources {
		if !m.allocator.HasPool(resReq.PoolName) {
			return nil, fmt.Errorf("pool not found: %s", resReq.PoolName)
		}
	}

	// Create sandbox record in StateCreating
	sb := NewSandbox(req.Name, req.Duration)
	sb.Metadata = req.Metadata

	if err := m.sandboxRepo.CreateSandbox(ctx, sb); err != nil {
		return nil, fmt.Errorf("failed to create sandbox record: %w", err)
	}

	// Allocate resources asynchronously
	go m.allocateResourcesAsync(sb.ID, sb.ExpiresAt, req.Resources)

	m.logger.WithField("sandbox_id", sb.ID).Info("Sandbox creation started")

	return sb, nil
}

// CreateSync creates a new sandbox and waits for all resources to be allocated
// This is a synchronous version for cases where waiting is desired
func (m *Manager) CreateSync(ctx context.Context, req *CreateRequest) (*Sandbox, error) {
	sb, err := m.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return m.WaitForReady(ctx, sb.ID, 5*time.Minute)
}

// WaitForReady waits for a sandbox to transition to StateReady or StateError
func (m *Manager) WaitForReady(ctx context.Context, sandboxID string, timeout time.Duration) (*Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for sandbox to be ready")
		case <-ticker.C:
			sb, err := m.sandboxRepo.GetSandboxByID(ctx, sandboxID)
			if err != nil {
				return nil, err
			}

			switch sb.State {
			case StateReady:
				return sb, nil
			case StateError:
				errMsg := "unknown error"
				if msg, ok := sb.Metadata["error"]; ok {
					errMsg = msg
				}
				return nil, fmt.Errorf("sandbox creation failed: %s", errMsg)
			case StateCreating:
				// Still creating, continue waiting
				continue
			default:
				return nil, fmt.Errorf("unexpected sandbox state: %s", sb.State)
			}
		}
	}
}

// allocateResourcesAsync allocates resources for a sandbox in background
func (m *Manager) allocateResourcesAsync(sandboxID string, expiresAt *time.Time, resourceReqs []ResourceRequest) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(logrus.Fields{
				"sandbox_id": sandboxID,
				"panic":      r,
			}).Error("Panic in async resource allocation")

			// Mark sandbox as error
			ctx := context.Background()
			if sb, err := m.sandboxRepo.GetSandboxByID(ctx, sandboxID); err == nil {
				sb.State = StateError
				if sb.Metadata == nil {
					sb.Metadata = make(map[string]string)
				}
				sb.Metadata["error"] = fmt.Sprintf("panic during allocation: %v", r)
				sb.UpdatedAt = time.Now()
				_ = m.sandboxRepo.UpdateSandbox(ctx, sb)
			}
		}
	}()

	ctx := context.Background()
	var allocatedIDs []string

	// Allocate resources from pools
	for _, resReq := range resourceReqs {
		if !m.allocator.HasPool(resReq.PoolName) {
			m.markSandboxError(sandboxID, fmt.Sprintf("pool not found: %s", resReq.PoolName), allocatedIDs)
			return
		}

		for i := 0; i < resReq.Count; i++ {
			res, err := m.allocator.Allocate(ctx, resReq.PoolName, sandboxID, expiresAt)
			if err != nil {
				m.logger.WithError(err).Errorf("Failed to allocate resource %d from pool %s", i+1, resReq.PoolName)
				m.markSandboxError(sandboxID, fmt.Sprintf("failed to allocate from pool %s: %v", resReq.PoolName, err), allocatedIDs)
				return
			}
			allocatedIDs = append(allocatedIDs, res.ID)
		}
	}

	// Update sandbox with resource IDs and mark ready
	sb, err := m.sandboxRepo.GetSandboxByID(ctx, sandboxID)
	if err != nil {
		m.logger.WithError(err).Error("Failed to get sandbox after allocation")
		m.cleanupPartialSandbox(ctx, sandboxID, allocatedIDs)
		return
	}

	sb.ResourceIDs = allocatedIDs
	sb.State = StateReady
	sb.UpdatedAt = time.Now()

	if err := m.sandboxRepo.UpdateSandbox(ctx, sb); err != nil {
		m.logger.WithError(err).Error("Failed to update sandbox after allocation")
		m.markSandboxError(sandboxID, fmt.Sprintf("failed to update sandbox: %v", err), allocatedIDs)
		return
	}

	m.logger.WithFields(logrus.Fields{
		"sandbox_id": sandboxID,
		"resources":  len(allocatedIDs),
	}).Info("Sandbox ready")
}

// markSandboxError marks a sandbox as error and cleans up partial allocations
func (m *Manager) markSandboxError(sandboxID string, errorMsg string, allocatedIDs []string) {
	ctx := context.Background()

	// Cleanup allocated resources
	m.cleanupPartialSandbox(ctx, sandboxID, allocatedIDs)

	// Mark sandbox as error
	if sb, err := m.sandboxRepo.GetSandboxByID(ctx, sandboxID); err == nil {
		sb.State = StateError
		if sb.Metadata == nil {
			sb.Metadata = make(map[string]string)
		}
		sb.Metadata["error"] = errorMsg
		sb.UpdatedAt = time.Now()
		_ = m.sandboxRepo.UpdateSandbox(ctx, sb)
	}
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

	// Release all resources for the sandbox
	if err := m.allocator.ReleaseSandbox(ctx, id); err != nil {
		m.logger.WithError(err).WithField("sandbox_id", id).Warn("Sandbox destroyed with some release errors")
		return err
	}

	// Mark sandbox as destroyed
	sb.State = StateDestroyed
	sb.UpdatedAt = time.Now()

	if err := m.sandboxRepo.UpdateSandbox(ctx, sb); err != nil {
		return fmt.Errorf("failed to update sandbox state: %w", err)
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
		_ = m.allocator.ReleaseResource(ctx, resID)
	}

	// Delete sandbox record
	_ = m.sandboxRepo.DeleteSandbox(ctx, sandboxID)
}

// ResourceWithConnection combines resource info with connection details
type ResourceWithConnection struct {
	Resource   *provider.Resource       `json:"resource"`
	Connection *provider.ConnectionInfo `json:"connection"`
}
