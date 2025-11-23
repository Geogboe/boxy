package agent

import (
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/provider"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

// Helper functions for converting between internal types and proto types

func protoToResourceSpec(pbSpec *pb.ResourceSpec) provider.ResourceSpec {
	return provider.ResourceSpec{
		Type:         provider.ResourceType(pbSpec.Type),
		ProviderType: pbSpec.ProviderType, // string field
		Image:        pbSpec.Image,
		CPUs:         int(pbSpec.Cpus),
		MemoryMB:     int(pbSpec.MemoryMb),
		DiskGB:       int(pbSpec.DiskGb),
		Labels:       pbSpec.Labels,
		Environment:  pbSpec.Environment,
		ExtraConfig:  stringMapToMap(pbSpec.ExtraConfig),
	}
}

func resourceToProto(res *provider.Resource) *pb.Resource {
	return &pb.Resource{
		Id:           res.ID,
		PoolId:       res.PoolID,
		SandboxId:    stringVal(res.SandboxID),
		Type:         string(res.Type),
		State:        string(res.State),
		ProviderType: res.ProviderType, // string field
		ProviderId:   res.ProviderID,
		Spec:         mapToStringMap(res.Spec),
		Metadata:     mapToStringMap(res.Metadata),
		CreatedAt:    res.CreatedAt.Unix(),
		UpdatedAt:    res.UpdatedAt.Unix(),
		ExpiresAt:    timeVal(res.ExpiresAt).Unix(),
	}
}

func protoToResource(pb *pb.Resource) *provider.Resource {
	return &provider.Resource{
		ID:           pb.Id,
		PoolID:       pb.PoolId,
		SandboxID:    stringPtr(pb.SandboxId),
		Type:         provider.ResourceType(pb.Type),
		State:        provider.ResourceState(pb.State),
		ProviderType: pb.ProviderType, // string field
		ProviderID:   pb.ProviderId,
		Spec:         stringMapToMap(pb.Spec),
		Metadata:     stringMapToMap(pb.Metadata),
		CreatedAt:    time.Unix(pb.CreatedAt, 0),
		UpdatedAt:    time.Unix(pb.UpdatedAt, 0),
		ExpiresAt:    timePtr(time.Unix(pb.ExpiresAt, 0)),
	}
}

// Convert proto action/params to ResourceUpdate
func protoToResourceUpdate(action string, params map[string]string) provider_pkg.ResourceUpdate {
	updates := provider_pkg.ResourceUpdate{}

	// Parse power state actions using switch
	switch action {
	case "power-running":
		ps := provider_pkg.PowerStateRunning
		updates.PowerState = &ps
	case "power-stopped":
		ps := provider_pkg.PowerStateStopped
		updates.PowerState = &ps
	case "power-paused":
		ps := provider_pkg.PowerStatePaused
		updates.PowerState = &ps
	case "power-reset":
		ps := provider_pkg.PowerStateReset
		updates.PowerState = &ps
	case "snapshot-create", "snapshot-restore", "snapshot-delete":
		// Parse snapshot actions
		updates.Snapshot = &provider_pkg.SnapshotOp{
			Operation: params["operation"],
			Name:      params["name"],
		}
	case "update-resources":
		// Parse resource updates
		updates.Resources = &provider_pkg.ResourceLimits{}
		if cpuStr, ok := params["cpus"]; ok {
			var cpus int
			_, _ = fmt.Sscanf(cpuStr, "%d", &cpus)
			updates.Resources.CPUs = &cpus
		}
		if memStr, ok := params["memory_mb"]; ok {
			var mem int
			_, _ = fmt.Sscanf(memStr, "%d", &mem)
			updates.Resources.MemoryMB = &mem
		}
	}

	return updates
}

// Pointer helpers
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() || t.Unix() == 0 {
		return nil
	}
	return &t
}

func timeVal(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// Map conversion helpers
func mapToStringMap(m map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

func stringMapToMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}
