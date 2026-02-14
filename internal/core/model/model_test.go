package model

import (
	"encoding/json"
	"testing"
)

func TestModels_JSONRoundTrip(t *testing.T) {
	in := Sandbox{
		ID:   "sbx_123",
		Name: "demo",
		Resources: []ResourceID{
			"res_1",
			"res_2",
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Sandbox
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID {
		t.Fatalf("unexpected round-trip: %#v", out)
	}
	if len(out.Resources) != len(in.Resources) {
		t.Fatalf("unexpected resources: %#v", out.Resources)
	}
}

func TestPoolInventory_KindInvariant(t *testing.T) {
	p := Pool{
		Name: "dev",
		Policies: PoolPolicies{
			Preheat: PreheatPolicy{
				MinReady: 1,
				MaxTotal: 3,
			},
		},
		Inventory: ResourceCollection{
			ExpectedType: ResourceTypeContainer,
		},
	}

	if err := p.Inventory.Add(Resource{Type: ResourceTypeContainer}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := p.Inventory.Add(Resource{Type: ResourceTypeVM}); err == nil {
		t.Fatalf("expected kind mismatch error")
	}
	if err := p.Inventory.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
