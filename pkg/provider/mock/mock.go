package mock

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
)

// Provider is a mock provider for testing without Docker
type Provider struct {
	mu               sync.Mutex
	resources        map[string]*provider.Resource
	provisionDelay   time.Duration
	destroyDelay     time.Duration
	failureRate      float64 // 0.0 to 1.0
	provisionCount   int
	destroyCount     int
	healthCheckCount int
	shouldFailHealth bool
	logger           *logrus.Logger
}

// Config for mock provider
type Config struct {
	ProvisionDelay   time.Duration
	DestroyDelay     time.Duration
	FailureRate      float64
	ShouldFailHealth bool
}

// NewProvider creates a new mock provider
func NewProvider(logger *logrus.Logger, cfg *Config) *Provider {
	if cfg == nil {
		cfg = &Config{
			ProvisionDelay: 100 * time.Millisecond,
			DestroyDelay:   50 * time.Millisecond,
			FailureRate:    0.0,
		}
	}

	return &Provider{
		resources:        make(map[string]*provider.Resource),
		provisionDelay:   cfg.ProvisionDelay,
		destroyDelay:     cfg.DestroyDelay,
		failureRate:      cfg.FailureRate,
		shouldFailHealth: cfg.ShouldFailHealth,
		logger:           logger,
	}
}

// Provision creates a mock resource
func (p *Provider) Provision(ctx context.Context, spec provider.ResourceSpec) (*provider.Resource, error) {
	p.mu.Lock()
	p.provisionCount++
	count := p.provisionCount
	p.mu.Unlock()

	// Simulate provisioning delay
	select {
	case <-time.After(p.provisionDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Simulate random failures
	// #nosec G404 - Using math/rand for mock provider is acceptable
	if p.failureRate > 0 && rand.Float64() < p.failureRate {
		return nil, fmt.Errorf("mock provision failure (simulated)")
	}

	res := &provider.Resource{
		ID:           uuid.New().String(),
		Type:         spec.Type,
		State:        provider.StateReady,
		ProviderType: "mock",
		ProviderID:   fmt.Sprintf("mock-%d", count),
		Spec: map[string]interface{}{
			"image":     spec.Image,
			"cpus":      spec.CPUs,
			"memory_mb": spec.MemoryMB,
		},
		Metadata: map[string]interface{}{
			"mock_id":    count,
			"mock_delay": p.provisionDelay.String(),
			"password":   "mock-password-123",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	p.mu.Lock()
	p.resources[res.ProviderID] = res
	p.mu.Unlock()

	p.logger.WithField("resource_id", res.ID).Debug("Mock resource provisioned")

	return res, nil
}

// Destroy removes a mock resource
func (p *Provider) Destroy(ctx context.Context, res *provider.Resource) error {
	p.mu.Lock()
	p.destroyCount++
	p.mu.Unlock()

	// Simulate destroy delay
	select {
	case <-time.After(p.destroyDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	p.mu.Lock()
	delete(p.resources, res.ProviderID)
	p.mu.Unlock()

	p.logger.WithField("resource_id", res.ID).Debug("Mock resource destroyed")

	return nil
}

// GetStatus returns mock status
func (p *Provider) GetStatus(ctx context.Context, res *provider.Resource) (*provider.ResourceStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Look up by ProviderID (not ID, since pool manager owns the ID)
	_, exists := p.resources[res.ProviderID]
	if !exists {
		return &provider.ResourceStatus{
			State:     provider.StateDestroyed,
			Healthy:   false,
			Message:   "resource not found",
			LastCheck: time.Now(),
		}, nil
	}

	healthy := !p.shouldFailHealth
	state := provider.StateReady
	if !healthy {
		state = provider.StateError
	}

	return &provider.ResourceStatus{
		State:     state,
		Healthy:   healthy,
		Message:   "mock resource",
		LastCheck: time.Now(),
		Uptime:    time.Since(res.CreatedAt),
	}, nil
}

// GetConnectionInfo returns mock connection info
func (p *Provider) GetConnectionInfo(ctx context.Context, res *provider.Resource) (*provider.ConnectionInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Look up by ProviderID (not ID, since pool manager owns the ID)
	_, exists := p.resources[res.ProviderID]
	if !exists {
		return nil, fmt.Errorf("resource not found")
	}

	return &provider.ConnectionInfo{
		Type:     "mock",
		Host:     "mock-host",
		Port:     9999,
		Username: "mock-user",
		Password: "mock-password-123",
		ExtraFields: map[string]interface{}{
			"mock_id": res.Metadata["mock_id"],
		},
	}, nil
}

// HealthCheck returns health status
func (p *Provider) HealthCheck(ctx context.Context) error {
	p.mu.Lock()
	p.healthCheckCount++
	p.mu.Unlock()

	if p.shouldFailHealth {
		return fmt.Errorf("mock provider unhealthy (simulated)")
	}
	return nil
}

// Name returns provider name
func (p *Provider) Name() string {
	return "mock"
}

// Type returns resource type
func (p *Provider) Type() provider.ResourceType {
	return provider.ResourceTypeContainer
}

// Stats returns mock provider statistics
func (p *Provider) Stats() ProviderStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return ProviderStats{
		ProvisionCount:   p.provisionCount,
		DestroyCount:     p.destroyCount,
		HealthCheckCount: p.healthCheckCount,
		ActiveResources:  len(p.resources),
	}
}

// SetFailureRate changes the failure rate dynamically
func (p *Provider) SetFailureRate(rate float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failureRate = rate
}

// SetShouldFailHealth changes health check behavior
func (p *Provider) SetShouldFailHealth(shouldFail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shouldFailHealth = shouldFail
}

// Reset clears all mock state
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.resources = make(map[string]*provider.Resource)
	p.provisionCount = 0
	p.destroyCount = 0
	p.healthCheckCount = 0
}

// Update modifies a resource (mock implementation)
func (p *Provider) Update(ctx context.Context, res *provider.Resource, updates provider.ResourceUpdate) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Look up by ProviderID (not ID, since pool manager owns the ID)
	_, exists := p.resources[res.ProviderID]
	if !exists {
		return fmt.Errorf("resource not found: %s", res.ProviderID)
	}

	// Mock: just return success
	return nil
}

// Execute runs a command in the resource (mock implementation)
func (p *Provider) Exec(ctx context.Context, res *provider.Resource, cmd []string) (*provider.ExecResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Look up by ProviderID (not ID, since pool manager owns the ID)
	_, exists := p.resources[res.ProviderID]
	if !exists {
		return nil, fmt.Errorf("resource not found: %s", res.ProviderID)
	}

	// Mock: simulate successful command execution
	return &provider.ExecResult{
		ExitCode: 0,
		Stdout:   fmt.Sprintf("Mock output for: %v", cmd),
		Stderr:   "",
	}, nil
}

// ProviderStats contains statistics about the mock provider
type ProviderStats struct {
	ProvisionCount   int
	DestroyCount     int
	HealthCheckCount int
	ActiveResources  int
}

// Ensure Provider implements provider.Provider interface
var _ provider.Provider = (*Provider)(nil)
