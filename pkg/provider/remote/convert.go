package remote

import (
	"fmt"
	"time"

	"github.com/Geogboe/boxy/internal/core/resource"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

// Helper functions for converting between internal types and proto types

func resourceSpecToProto(spec *resource.ResourceSpec) *pb.ResourceSpec {
	return &pb.ResourceSpec{
		Type:         string(spec.Type),
		ProviderType: spec.ProviderType,
		Image:        spec.Image,
		Cpus:         int32(spec.CPUs),
		MemoryMb:     int32(spec.MemoryMB),
		DiskGb:       int32(spec.DiskGB),
		Labels:       spec.Labels,
		Environment:  spec.Environment,
		ExtraConfig:  mapToStringMap(spec.ExtraConfig),
	}
}

func resourceToProto(res *resource.Resource) *pb.Resource {
	return &pb.Resource{
		Id:           res.ID,
		PoolId:       res.PoolID,
		SandboxId:    stringVal(res.SandboxID),
		Type:         string(res.Type),
		State:        string(res.State),
		ProviderType: res.ProviderType,
		ProviderId:   res.ProviderID,
		Spec:         mapToStringMap(res.Spec),
		Metadata:     mapToStringMap(res.Metadata),
		CreatedAt:    res.CreatedAt.Unix(),
		UpdatedAt:    res.UpdatedAt.Unix(),
		ExpiresAt:    timeVal(res.ExpiresAt).Unix(),
	}
}

func protoToResource(pb *pb.Resource) *resource.Resource {
	return &resource.Resource{
		ID:           pb.Id,
		PoolID:       pb.PoolId,
		SandboxID:    stringPtr(pb.SandboxId),
		Type:         resource.ResourceType(pb.Type),
		State:        resource.ResourceState(pb.State),
		ProviderType: pb.ProviderType,
		ProviderID:   pb.ProviderId,
		Spec:         stringMapToMap(pb.Spec),
		Metadata:     stringMapToMap(pb.Metadata),
		CreatedAt:    time.Unix(pb.CreatedAt, 0),
		UpdatedAt:    time.Unix(pb.UpdatedAt, 0),
		ExpiresAt:    timePtr(time.Unix(pb.ExpiresAt, 0)),
	}
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

// Convert ResourceUpdate to proto action/params format
func resourceUpdateToProto(updates provider_pkg.ResourceUpdate) (string, map[string]string) {
	params := make(map[string]string)

	// Handle PowerState updates
	if updates.PowerState != nil {
		return fmt.Sprintf("power-%s", string(*updates.PowerState)), params
	}

	// Handle Snapshot operations
	if updates.Snapshot != nil {
		action := fmt.Sprintf("snapshot-%s", updates.Snapshot.Operation)
		params["name"] = updates.Snapshot.Name
		return action, params
	}

	// Handle Resource limit updates
	if updates.Resources != nil {
		action := "update-resources"
		if updates.Resources.CPUs != nil {
			params["cpus"] = fmt.Sprintf("%d", *updates.Resources.CPUs)
		}
		if updates.Resources.MemoryMB != nil {
			params["memory_mb"] = fmt.Sprintf("%d", *updates.Resources.MemoryMB)
		}
		return action, params
	}

	// Default
	return "unknown", params
}
