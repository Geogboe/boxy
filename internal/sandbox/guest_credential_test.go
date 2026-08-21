package sandbox

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type credentialAllocator struct{}

func (credentialAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{
		Properties: map[string]any{"access": "winrm"},
		GuestCredential: &providersdk.GuestCredential{
			Kind: "password",
			Data: json.RawMessage(`{"username":"Administrator","password":"rotated"}`),
		},
	}, nil
}

func TestManagerKeepsGuestCredentialOutOfResourceProperties(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	resource := model.Resource{
		ID:      "vm-1",
		Type:    model.ResourceTypeVM,
		Profile: "windows",
		State:   model.ResourceStateReady,
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "vm-pool",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: "windows",
			Resources:       []model.Resource{resource},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	mgr := New(st, credentialAllocator{})
	sb, err := mgr.CreateFromPool(ctx, "vm-pool", 1, "guest", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}

	stored, err := st.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if stored.Properties["access"] != "winrm" {
		t.Fatalf("resource properties = %+v, want safe access property", stored.Properties)
	}
	if _, ok := stored.Properties["password"]; ok {
		t.Fatalf("resource properties = %+v, must not contain credential data", stored.Properties)
	}

	deliveries := mgr.TakeGuestCredentials(sb.ID)
	if len(deliveries) != 1 || deliveries[0].ResourceID != resource.ID {
		t.Fatalf("deliveries = %+v, want one delivery for %q", deliveries, resource.ID)
	}
	if string(deliveries[0].Credential.Data) != `{"username":"Administrator","password":"rotated"}` {
		t.Fatalf("credential data = %s, want opaque rotated credential", deliveries[0].Credential.Data)
	}
	if got := mgr.TakeGuestCredentials(sb.ID); got != nil {
		t.Fatalf("second delivery = %+v, want nil after one-time consume", got)
	}
}
