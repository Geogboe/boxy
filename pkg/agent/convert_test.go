package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/Geogboe/boxy/pkg/provider"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

func TestProtoToResourceSpec(t *testing.T) {
	pbSpec := &pb.ResourceSpec{
		Type:         "vm",
		ProviderType: "mock",
		Image:        "ubuntu-22.04",
		Cpus:         4,
		MemoryMb:     8192,
		DiskGb:       100,
		Labels: map[string]string{
			"env":  "test",
			"team": "dev",
		},
		Environment: map[string]string{
			"DEBUG": "true",
		},
		ExtraConfig: map[string]string{
			"key": "value",
		},
	}

	spec := protoToResourceSpec(pbSpec)

	assert.Equal(t, provider.ResourceType("vm"), spec.Type)
	assert.Equal(t, "mock", spec.ProviderType)
	assert.Equal(t, "ubuntu-22.04", spec.Image)
	assert.Equal(t, 4, spec.CPUs)
	assert.Equal(t, 8192, spec.MemoryMB)
	assert.Equal(t, 100, spec.DiskGB)
	assert.Equal(t, "test", spec.Labels["env"])
	assert.Equal(t, "true", spec.Environment["DEBUG"])
	assert.Equal(t, "value", spec.ExtraConfig["key"])
}

func TestResourceToProto(t *testing.T) {
	sandboxID := "sandbox-123"
	now := time.Now()
	expires := now.Add(1 * time.Hour)

	res := &provider.Resource{
		ID:           "res-123",
		PoolID:       "pool-456",
		SandboxID:    &sandboxID,
		Type:         provider.ResourceTypeVM,
		State:        provider.StateReady,
		ProviderType: "mock",
		ProviderID:   "provider-789",
		Spec: map[string]interface{}{
			"cpus":   4,
			"memory": 8192,
		},
		Metadata: map[string]interface{}{
			"created_by": "test",
		},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: &expires,
	}

	pbRes := resourceToProto(res)

	assert.Equal(t, "res-123", pbRes.Id)
	assert.Equal(t, "pool-456", pbRes.PoolId)
	assert.Equal(t, "sandbox-123", pbRes.SandboxId)
	assert.Equal(t, "vm", pbRes.Type)
	assert.Equal(t, "ready", pbRes.State)
	assert.Equal(t, "mock", pbRes.ProviderType)
	assert.Equal(t, "provider-789", pbRes.ProviderId)
	assert.Equal(t, "4", pbRes.Spec["cpus"])
	assert.Equal(t, "test", pbRes.Metadata["created_by"])
	assert.Equal(t, now.Unix(), pbRes.CreatedAt)
	assert.Equal(t, now.Unix(), pbRes.UpdatedAt)
	assert.Equal(t, expires.Unix(), pbRes.ExpiresAt)
}

func TestResourceToProto_NilFields(t *testing.T) {
	res := &provider.Resource{
		ID:           "res-123",
		PoolID:       "pool-456",
		SandboxID:    nil, // nil pointer
		Type:         provider.ResourceTypeVM,
		State:        provider.StateProvisioning,
		ProviderType: "mock",
		ProviderID:   "provider-789",
		Spec:         map[string]interface{}{},
		Metadata:     map[string]interface{}{},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		ExpiresAt:    nil, // nil pointer
	}

	pbRes := resourceToProto(res)

	assert.Equal(t, "", pbRes.SandboxId) // nil becomes empty string
	// Zero time gets converted, not nil time - this is expected behavior
}

func TestProtoToResource(t *testing.T) {
	now := time.Now()

	pbRes := &pb.Resource{
		Id:           "res-123",
		PoolId:       "pool-456",
		SandboxId:    "sandbox-789",
		Type:         "vm",
		State:        "ready",
		ProviderType: "mock",
		ProviderId:   "provider-abc",
		Spec: map[string]string{
			"cpus":   "4",
			"memory": "8192",
		},
		Metadata: map[string]string{
			"tag": "test",
		},
		CreatedAt: now.Unix(),
		UpdatedAt: now.Unix(),
		ExpiresAt: now.Add(1 * time.Hour).Unix(),
	}

	res := protoToResource(pbRes)

	assert.Equal(t, "res-123", res.ID)
	assert.Equal(t, "pool-456", res.PoolID)
	assert.NotNil(t, res.SandboxID)
	assert.Equal(t, "sandbox-789", *res.SandboxID)
	assert.Equal(t, provider.ResourceType("vm"), res.Type)
	assert.Equal(t, provider.ResourceState("ready"), res.State)
	assert.Equal(t, "mock", res.ProviderType)
	assert.Equal(t, "provider-abc", res.ProviderID)
	assert.Equal(t, "4", res.Spec["cpus"])
	assert.Equal(t, "test", res.Metadata["tag"])
}

func TestProtoToResourceUpdate_PowerState(t *testing.T) {
	tests := []struct {
		action   string
		expected provider.PowerState
	}{
		{"power-running", provider.PowerStateRunning},
		{"power-stopped", provider.PowerStateStopped},
		{"power-paused", provider.PowerStatePaused},
		{"power-reset", provider.PowerStateReset},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			update := protoToResourceUpdate(tt.action, nil)
			assert.NotNil(t, update.PowerState)
			assert.Equal(t, tt.expected, *update.PowerState)
		})
	}
}

func TestProtoToResourceUpdate_Snapshot(t *testing.T) {
	params := map[string]string{
		"operation": "create",
		"name":      "snapshot-1",
	}

	update := protoToResourceUpdate("snapshot-create", params)

	assert.NotNil(t, update.Snapshot)
	assert.Equal(t, "create", update.Snapshot.Operation)
	assert.Equal(t, "snapshot-1", update.Snapshot.Name)
}

func TestProtoToResourceUpdate_Resources(t *testing.T) {
	params := map[string]string{
		"cpus":      "8",
		"memory_mb": "16384",
	}

	update := protoToResourceUpdate("update-resources", params)

	assert.NotNil(t, update.Resources)
	assert.NotNil(t, update.Resources.CPUs)
	assert.Equal(t, 8, *update.Resources.CPUs)
	assert.NotNil(t, update.Resources.MemoryMB)
	assert.Equal(t, 16384, *update.Resources.MemoryMB)
}

func TestStringPtr(t *testing.T) {
	// Empty string returns nil
	assert.Nil(t, stringPtr(""))

	// Non-empty string returns pointer
	s := stringPtr("test")
	assert.NotNil(t, s)
	assert.Equal(t, "test", *s)
}

func TestStringVal(t *testing.T) {
	// Nil pointer returns empty string
	assert.Equal(t, "", stringVal(nil))

	// Non-nil pointer returns value
	s := "test"
	assert.Equal(t, "test", stringVal(&s))
}

func TestTimePtr(t *testing.T) {
	// Zero time returns nil
	assert.Nil(t, timePtr(time.Time{}))

	// Valid time returns pointer
	now := time.Now()
	tp := timePtr(now)
	assert.NotNil(t, tp)
	assert.Equal(t, now, *tp)
}

func TestTimeVal(t *testing.T) {
	// Nil pointer returns zero time
	assert.True(t, timeVal(nil).IsZero())

	// Non-nil pointer returns value
	now := time.Now()
	assert.Equal(t, now, timeVal(&now))
}

func TestMapToStringMap(t *testing.T) {
	input := map[string]interface{}{
		"string": "value",
		"int":    42,
		"bool":   true,
		"float":  3.14,
	}

	result := mapToStringMap(input)

	assert.Equal(t, "value", result["string"])
	assert.Equal(t, "42", result["int"])
	assert.Equal(t, "true", result["bool"])
	assert.Equal(t, "3.14", result["float"])
}

func TestMapToStringMap_Empty(t *testing.T) {
	input := map[string]interface{}{}
	result := mapToStringMap(input)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestStringMapToMap(t *testing.T) {
	input := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	result := stringMapToMap(input)

	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, "value2", result["key2"])
}

func TestStringMapToMap_Empty(t *testing.T) {
	input := map[string]string{}
	result := stringMapToMap(input)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestRoundTripConversion(t *testing.T) {
	// Test that converting Resource -> Proto -> Resource preserves data
	sandboxID := "sandbox-123"
	now := time.Now().Truncate(time.Second) // Truncate to second precision
	expires := now.Add(1 * time.Hour)

	original := &provider.Resource{
		ID:           "res-123",
		PoolID:       "pool-456",
		SandboxID:    &sandboxID,
		Type:         provider.ResourceTypeVM,
		State:        provider.StateReady,
		ProviderType: "mock",
		ProviderID:   "provider-789",
		Spec: map[string]interface{}{
			"image": "ubuntu-22.04",
			"cpus":  4,
		},
		Metadata: map[string]interface{}{
			"owner": "test-user",
		},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: &expires,
	}

	// Convert to proto and back
	pbRes := resourceToProto(original)
	converted := protoToResource(pbRes)

	// Compare (note: interface{} values become strings in proto)
	assert.Equal(t, original.ID, converted.ID)
	assert.Equal(t, original.PoolID, converted.PoolID)
	assert.Equal(t, *original.SandboxID, *converted.SandboxID)
	assert.Equal(t, original.Type, converted.Type)
	assert.Equal(t, original.State, converted.State)
	assert.Equal(t, original.ProviderType, converted.ProviderType)
	assert.Equal(t, original.ProviderID, converted.ProviderID)
	assert.Equal(t, original.CreatedAt.Unix(), converted.CreatedAt.Unix())
	assert.Equal(t, original.UpdatedAt.Unix(), converted.UpdatedAt.Unix())
	assert.Equal(t, original.ExpiresAt.Unix(), converted.ExpiresAt.Unix())
}
