package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TRUE UNIT TESTS - No external dependencies

func TestNewResource(t *testing.T) {
	t.Run("creates resource with UUID", func(t *testing.T) {
		res := NewResource("pool-123", ResourceTypeVM, "hyperv")

		assert.NotEmpty(t, res.ID)
		assert.Equal(t, "pool-123", res.PoolID)
		assert.Equal(t, ResourceTypeVM, res.Type)
		assert.Equal(t, "hyperv", res.ProviderType)
		assert.Equal(t, StateProvisioning, res.State)
		assert.Nil(t, res.SandboxID)
		assert.NotNil(t, res.Spec)
		assert.NotNil(t, res.Metadata)
		assert.False(t, res.CreatedAt.IsZero())
		assert.False(t, res.UpdatedAt.IsZero())
	})

	t.Run("each resource gets unique ID", func(t *testing.T) {
		res1 := NewResource("pool-1", ResourceTypeContainer, "docker")
		res2 := NewResource("pool-1", ResourceTypeContainer, "docker")

		assert.NotEqual(t, res1.ID, res2.ID)
	})
}

func TestResource_IsAvailable(t *testing.T) {
	t.Run("ready with no sandbox", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateReady
		res.SandboxID = nil

		assert.True(t, res.IsAvailable())
	})

	t.Run("not available when provisioning", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateProvisioning
		res.SandboxID = nil

		assert.False(t, res.IsAvailable())
	})

	t.Run("not available when allocated", func(t *testing.T) {
		sandboxID := "sandbox-123"
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateReady
		res.SandboxID = &sandboxID

		assert.False(t, res.IsAvailable())
	})

	t.Run("not available when in error state", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateError
		res.SandboxID = nil

		assert.False(t, res.IsAvailable())
	})
}

func TestResource_IsAllocated(t *testing.T) {
	sandboxID := "sandbox-123"

	t.Run("allocated when sandbox ID set and state allocated", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateAllocated
		res.SandboxID = &sandboxID

		assert.True(t, res.IsAllocated())
	})

	t.Run("not allocated with no sandbox ID", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateAllocated
		res.SandboxID = nil

		assert.False(t, res.IsAllocated())
	})

	t.Run("not allocated when ready", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.State = StateReady
		res.SandboxID = &sandboxID

		assert.False(t, res.IsAllocated())
	})
}

func TestResource_IsExpired(t *testing.T) {
	t.Run("not expired with no expiration", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.ExpiresAt = nil

		assert.False(t, res.IsExpired())
	})

	t.Run("expired when past expiration time", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.ExpiresAt = &past

		assert.True(t, res.IsExpired())
	})

	t.Run("not expired when before expiration time", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.ExpiresAt = &future

		assert.False(t, res.IsExpired())
	})

	t.Run("expired at exact expiration time", func(t *testing.T) {
		// This test might be flaky due to timing, but demonstrates the concept
		now := time.Now().Add(-1 * time.Millisecond)
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.ExpiresAt = &now

		// After() returns true if time is after the expiration
		assert.True(t, res.IsExpired())
	})
}

func TestResourceType_Constants(t *testing.T) {
	t.Run("resource types defined", func(t *testing.T) {
		assert.Equal(t, ResourceType("vm"), ResourceTypeVM)
		assert.Equal(t, ResourceType("container"), ResourceTypeContainer)
		assert.Equal(t, ResourceType("process"), ResourceTypeProcess)
	})
}

func TestResourceState_Constants(t *testing.T) {
	t.Run("resource states defined", func(t *testing.T) {
		assert.Equal(t, ResourceState("provisioning"), StateProvisioning)
		assert.Equal(t, ResourceState("ready"), StateReady)
		assert.Equal(t, ResourceState("allocated"), StateAllocated)
		assert.Equal(t, ResourceState("destroying"), StateDestroying)
		assert.Equal(t, ResourceState("destroyed"), StateDestroyed)
		assert.Equal(t, ResourceState("error"), StateError)
	})
}

func TestResourceSpec(t *testing.T) {
	t.Run("creates valid spec", func(t *testing.T) {
		spec := ResourceSpec{
			Type:         ResourceTypeVM,
			ProviderType: "hyperv",
			Image:        "windows-server-2022",
			CPUs:         4,
			MemoryMB:     8192,
			DiskGB:       100,
			Labels: map[string]string{
				"env": "test",
			},
			Environment: map[string]string{
				"KEY": "value",
			},
		}

		assert.Equal(t, ResourceTypeVM, spec.Type)
		assert.Equal(t, "hyperv", spec.ProviderType)
		assert.Equal(t, 4, spec.CPUs)
		assert.NotNil(t, spec.Labels)
		assert.NotNil(t, spec.Environment)
	})
}

func TestConnectionInfo(t *testing.T) {
	t.Run("creates valid connection info", func(t *testing.T) {
		conn := ConnectionInfo{
			Type:     "rdp",
			Host:     "192.168.1.100",
			Port:     3389,
			Username: "administrator",
			Password: "secret",
		}

		assert.Equal(t, "rdp", conn.Type)
		assert.Equal(t, "192.168.1.100", conn.Host)
		assert.Equal(t, 3389, conn.Port)
		assert.Equal(t, "administrator", conn.Username)
		assert.Equal(t, "secret", conn.Password)
	})

	t.Run("supports extra fields", func(t *testing.T) {
		conn := ConnectionInfo{
			Type: "docker-exec",
			ExtraFields: map[string]interface{}{
				"container_id": "abc123",
			},
		}

		assert.Equal(t, "abc123", conn.ExtraFields["container_id"])
	})
}

func TestResourceStatus(t *testing.T) {
	t.Run("creates valid status", func(t *testing.T) {
		status := ResourceStatus{
			State:     StateReady,
			Healthy:   true,
			Message:   "Resource is running",
			LastCheck: time.Now(),
			CPUUsage:  45.5,
		}

		assert.Equal(t, StateReady, status.State)
		assert.True(t, status.Healthy)
		assert.Equal(t, 45.5, status.CPUUsage)
	})
}

func TestResource_Metadata(t *testing.T) {
	t.Run("can store arbitrary metadata", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeVM, "hyperv")
		res.Metadata["ip_address"] = "192.168.1.100"
		res.Metadata["hostname"] = "test-vm"
		res.Metadata["custom_field"] = 12345

		assert.Equal(t, "192.168.1.100", res.Metadata["ip_address"])
		assert.Equal(t, "test-vm", res.Metadata["hostname"])
		assert.Equal(t, 12345, res.Metadata["custom_field"])
	})
}

func TestResource_Spec(t *testing.T) {
	t.Run("can store arbitrary spec data", func(t *testing.T) {
		res := NewResource("pool-1", ResourceTypeContainer, "docker")
		res.Spec["image"] = "ubuntu:22.04"
		res.Spec["cpus"] = 2
		res.Spec["memory_mb"] = 4096

		assert.Equal(t, "ubuntu:22.04", res.Spec["image"])
		assert.Equal(t, 2, res.Spec["cpus"])
		assert.Equal(t, 4096, res.Spec["memory_mb"])
	})
}
