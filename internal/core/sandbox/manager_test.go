package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/v2/internal/core/model"
	"github.com/Geogboe/boxy/v2/internal/core/store"
)

func TestManager_CreateFromPool_ConsumesReadyResource(t *testing.T) {
	st := store.NewMemoryStore()

	r1 := model.Resource{
		ID:        "res_1",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{ID: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	r2 := model.Resource{
		ID:        "res_2",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{ID: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(2, 0).UTC(),
	}
	if err := st.PutResource(context.Background(), r1); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutResource(context.Background(), r2); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	pool := model.Pool{
		Name:      "docker-containers",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{r1, r2}},
	}
	if err := st.PutPool(context.Background(), pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	mgr := New(st)
	sb, err := mgr.CreateFromPool(context.Background(), "docker-containers", 1, "demo", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.ID == "" {
		t.Fatalf("expected sandbox id")
	}
	if len(sb.Resources) != 1 {
		t.Fatalf("expected 1 resource id in sandbox, got %d", len(sb.Resources))
	}

	updatedPool, err := st.GetPool(context.Background(), "docker-containers")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updatedPool.Inventory.Resources) != 1 {
		t.Fatalf("expected pool inventory size 1, got %d", len(updatedPool.Inventory.Resources))
	}

	updatedRes, err := st.GetResource(context.Background(), sb.Resources[0])
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if updatedRes.State != model.ResourceStateAllocated {
		t.Fatalf("expected resource allocated, got %q", updatedRes.State)
	}
}
