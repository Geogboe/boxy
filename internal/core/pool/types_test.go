package pool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/resource"
)

func TestPoolConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  PoolConfig
		wantErr error
	}{
		{
			name: "valid config",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 3,
				MaxTotal: 10,
			},
			wantErr: nil,
		},
		{
			name: "empty name",
			config: PoolConfig{
				Name:     "",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 3,
				MaxTotal: 10,
			},
			wantErr: ErrInvalidPoolName,
		},
		{
			name: "empty type",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     "",
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 3,
				MaxTotal: 10,
			},
			wantErr: ErrInvalidResourceType,
		},
		{
			name: "empty backend",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "",
				Image:    "ubuntu:22.04",
				MinReady: 3,
				MaxTotal: 10,
			},
			wantErr: ErrInvalidBackend,
		},
		{
			name: "empty image",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "",
				MinReady: 3,
				MaxTotal: 10,
			},
			wantErr: ErrInvalidImage,
		},
		{
			name: "negative min_ready",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: -1,
				MaxTotal: 10,
			},
			wantErr: ErrInvalidMinReady,
		},
		{
			name: "max_total less than min_ready",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 10,
				MaxTotal: 5,
			},
			wantErr: ErrInvalidMaxTotal,
		},
		{
			name: "zero min_ready is valid",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 0,
				MaxTotal: 10,
			},
			wantErr: nil,
		},
		{
			name: "min_ready equals max_total is valid",
			config: PoolConfig{
				Name:     "test-pool",
				Type:     provider.ResourceTypeContainer,
				Backend:  "docker",
				Image:    "ubuntu:22.04",
				MinReady: 5,
				MaxTotal: 5,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPoolConfig_ToResourceSpec(t *testing.T) {
	config := PoolConfig{
		Name:     "test-pool",
		Type:     provider.ResourceTypeContainer,
		Backend:  "docker",
		Image:    "ubuntu:22.04",
		MinReady: 3,
		MaxTotal: 10,
		CPUs:     2,
		MemoryMB: 512,
		DiskGB:   20,
		Labels: map[string]string{
			"env":  "test",
			"team": "backend",
		},
		Environment: map[string]string{
			"MY_VAR": "value",
		},
		ExtraConfig: map[string]interface{}{
			"custom": "data",
		},
	}

	spec := config.ToResourceSpec()

	assert.Equal(t, config.Type, spec.Type)
	assert.Equal(t, config.Backend, spec.ProviderType)
	assert.Equal(t, config.Image, spec.Image)
	assert.Equal(t, config.CPUs, spec.CPUs)
	assert.Equal(t, config.MemoryMB, spec.MemoryMB)
	assert.Equal(t, config.DiskGB, spec.DiskGB)
	assert.Equal(t, config.Labels, spec.Labels)
	assert.Equal(t, config.Environment, spec.Environment)
	assert.Equal(t, config.ExtraConfig, spec.ExtraConfig)
}

func TestPoolStats(t *testing.T) {
	t.Run("healthy pool", func(t *testing.T) {
		stats := &PoolStats{
			Name:              "test-pool",
			TotalReady:        5,
			TotalAllocated:    2,
			TotalProvisioning: 1,
			TotalError:        0,
			Total:             8,
			MinReady:          3,
			MaxTotal:          10,
			Healthy:           true,
			LastUpdate:        time.Now(),
		}

		assert.True(t, stats.Healthy)
		assert.GreaterOrEqual(t, stats.TotalReady, stats.MinReady)
		assert.Equal(t, 8, stats.Total)
		assert.Equal(t, 5, stats.TotalReady)
	})

	t.Run("unhealthy pool - below min_ready", func(t *testing.T) {
		stats := &PoolStats{
			Name:              "test-pool",
			TotalReady:        2,
			TotalAllocated:    5,
			TotalProvisioning: 2,
			TotalError:        1,
			Total:             10,
			MinReady:          5,
			MaxTotal:          10,
			Healthy:           false,
			LastUpdate:        time.Now(),
		}

		assert.False(t, stats.Healthy)
		assert.Less(t, stats.TotalReady, stats.MinReady)
	})

	t.Run("at capacity", func(t *testing.T) {
		stats := &PoolStats{
			Name:              "test-pool",
			TotalReady:        0,
			TotalAllocated:    10,
			TotalProvisioning: 0,
			TotalError:        0,
			Total:             10,
			MinReady:          3,
			MaxTotal:          10,
			Healthy:           false,
			LastUpdate:        time.Now(),
		}

		assert.Equal(t, stats.Total, stats.MaxTotal)
		assert.False(t, stats.Healthy)
	})
}

func TestPoolErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"invalid pool name", ErrInvalidPoolName},
		{"invalid resource type", ErrInvalidResourceType},
		{"invalid backend", ErrInvalidBackend},
		{"invalid image", ErrInvalidImage},
		{"invalid min ready", ErrInvalidMinReady},
		{"invalid max total", ErrInvalidMaxTotal},
		{"pool not found", ErrPoolNotFound},
		{"pool already exists", ErrPoolAlreadyExists},
		{"pool at capacity", ErrPoolAtCapacity},
		{"no resources available", ErrNoResourcesAvailable},
		{"pool not healthy", ErrPoolNotHealthy},
		{"resource not found", ErrResourceNotFound},
		{"resource not ready", ErrResourceNotReady},
		{"resource allocated", ErrResourceAllocated},
		{"provisioning failed", ErrProvisioningFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, tt.err)
			assert.NotEmpty(t, tt.err.Error())
		})
	}
}

// Benchmark tests
func BenchmarkPoolConfig_Validate(b *testing.B) {
	config := PoolConfig{
		Name:     "test-pool",
		Type:     provider.ResourceTypeContainer,
		Backend:  "docker",
		Image:    "ubuntu:22.04",
		MinReady: 3,
		MaxTotal: 10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.Validate()
	}
}

func BenchmarkPoolConfig_ToResourceSpec(b *testing.B) {
	config := PoolConfig{
		Name:     "test-pool",
		Type:     provider.ResourceTypeContainer,
		Backend:  "docker",
		Image:    "ubuntu:22.04",
		CPUs:     2,
		MemoryMB: 512,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.ToResourceSpec()
	}
}
