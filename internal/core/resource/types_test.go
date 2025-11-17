package resource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewResource(t *testing.T) {
	poolID := "test-pool"
	resourceType := ResourceTypeContainer
	providerType := "docker"

	res := NewResource(poolID, resourceType, providerType)

	assert.NotEmpty(t, res.ID, "ID should be generated")
	assert.Equal(t, poolID, res.PoolID)
	assert.Equal(t, resourceType, res.Type)
	assert.Equal(t, providerType, res.ProviderType)
	assert.Equal(t, StateProvisioning, res.State)
	assert.Nil(t, res.SandboxID)
	assert.NotNil(t, res.Spec)
	assert.NotNil(t, res.Metadata)
	assert.False(t, res.CreatedAt.IsZero())
	assert.False(t, res.UpdatedAt.IsZero())
}

func TestResource_IsAvailable(t *testing.T) {
	tests := []struct {
		name      string
		state     ResourceState
		sandboxID *string
		want      bool
	}{
		{
			name:      "ready and unallocated",
			state:     StateReady,
			sandboxID: nil,
			want:      true,
		},
		{
			name:      "ready but allocated",
			state:     StateReady,
			sandboxID: stringPtr("sb-123"),
			want:      false,
		},
		{
			name:      "provisioning",
			state:     StateProvisioning,
			sandboxID: nil,
			want:      false,
		},
		{
			name:      "allocated",
			state:     StateAllocated,
			sandboxID: stringPtr("sb-123"),
			want:      false,
		},
		{
			name:      "error state",
			state:     StateError,
			sandboxID: nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := NewResource("pool", ResourceTypeContainer, "docker")
			res.State = tt.state
			res.SandboxID = tt.sandboxID

			got := res.IsAvailable()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResource_IsAllocated(t *testing.T) {
	tests := []struct {
		name      string
		state     ResourceState
		sandboxID *string
		want      bool
	}{
		{
			name:      "allocated with sandbox ID",
			state:     StateAllocated,
			sandboxID: stringPtr("sb-123"),
			want:      true,
		},
		{
			name:      "ready with sandbox ID but not allocated state",
			state:     StateReady,
			sandboxID: stringPtr("sb-123"),
			want:      false,
		},
		{
			name:      "allocated state but no sandbox ID",
			state:     StateAllocated,
			sandboxID: nil,
			want:      false,
		},
		{
			name:      "not allocated",
			state:     StateReady,
			sandboxID: nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := NewResource("pool", ResourceTypeContainer, "docker")
			res.State = tt.state
			res.SandboxID = tt.sandboxID

			got := res.IsAllocated()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResource_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{
			name:      "no expiration",
			expiresAt: nil,
			want:      false,
		},
		{
			name:      "future expiration",
			expiresAt: timePtr(time.Now().Add(1 * time.Hour)),
			want:      false,
		},
		{
			name:      "past expiration",
			expiresAt: timePtr(time.Now().Add(-1 * time.Hour)),
			want:      true,
		},
		{
			name:      "just expired",
			expiresAt: timePtr(time.Now().Add(-1 * time.Second)),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := NewResource("pool", ResourceTypeContainer, "docker")
			res.ExpiresAt = tt.expiresAt

			got := res.IsExpired()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResourceSpec_Validation(t *testing.T) {
	t.Run("valid spec", func(t *testing.T) {
		spec := ResourceSpec{
			Type:         ResourceTypeContainer,
			ProviderType: "docker",
			Image:        "ubuntu:22.04",
			CPUs:         2,
			MemoryMB:     512,
			Labels: map[string]string{
				"env": "test",
			},
		}

		assert.Equal(t, ResourceTypeContainer, spec.Type)
		assert.Equal(t, "docker", spec.ProviderType)
		assert.Equal(t, "ubuntu:22.04", spec.Image)
		assert.Equal(t, 2, spec.CPUs)
		assert.Equal(t, 512, spec.MemoryMB)
		assert.NotNil(t, spec.Labels)
	})
}

func TestConnectionInfo(t *testing.T) {
	t.Run("create connection info", func(t *testing.T) {
		connInfo := &ConnectionInfo{
			Type:     "ssh",
			Host:     "192.168.1.100",
			Port:     22,
			Username: "ubuntu",
			Password: "secret",
			ExtraFields: map[string]interface{}{
				"key_path": "/path/to/key",
			},
		}

		assert.Equal(t, "ssh", connInfo.Type)
		assert.Equal(t, "192.168.1.100", connInfo.Host)
		assert.Equal(t, 22, connInfo.Port)
		assert.Equal(t, "ubuntu", connInfo.Username)
		assert.NotEmpty(t, connInfo.Password)
		assert.Contains(t, connInfo.ExtraFields, "key_path")
	})
}

func TestResourceStatus(t *testing.T) {
	t.Run("healthy status", func(t *testing.T) {
		status := &ResourceStatus{
			State:      StateReady,
			Healthy:    true,
			Message:    "running",
			LastCheck:  time.Now(),
			Uptime:     1 * time.Hour,
			CPUUsage:   25.5,
			MemoryUsed: 512 * 1024 * 1024,
		}

		assert.True(t, status.Healthy)
		assert.Equal(t, StateReady, status.State)
		assert.Equal(t, "running", status.Message)
		assert.Greater(t, status.CPUUsage, 0.0)
	})

	t.Run("unhealthy status", func(t *testing.T) {
		status := &ResourceStatus{
			State:     StateError,
			Healthy:   false,
			Message:   "container crashed",
			LastCheck: time.Now(),
		}

		assert.False(t, status.Healthy)
		assert.Equal(t, StateError, status.State)
		assert.Contains(t, status.Message, "crashed")
	})
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// Benchmark tests
func BenchmarkNewResource(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewResource("pool", ResourceTypeContainer, "docker")
	}
}

func BenchmarkResource_IsAvailable(b *testing.B) {
	res := NewResource("pool", ResourceTypeContainer, "docker")
	res.State = StateReady

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = res.IsAvailable()
	}
}
