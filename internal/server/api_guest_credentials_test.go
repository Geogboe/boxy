package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type serverGuestCredentialAllocator struct{}

func (serverGuestCredentialAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{GuestCredential: &providersdk.GuestCredential{
		Kind: "password",
		Data: json.RawMessage(`{"username":"Administrator","password":"rotated"}`),
	}}, nil
}

func TestAPI_GuestCredentialIsOneTimeAndNotResourceState(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	resource := model.Resource{ID: "vm-1", Type: model.ResourceTypeVM, Profile: "windows", State: model.ResourceStateReady}
	if err := st.PutPool(ctx, model.Pool{
		Name: "vm-pool",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: "windows",
			Resources:       []model.Resource{resource},
		},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	mgr := sandbox.New(st, serverGuestCredentialAllocator{})
	sb, err := mgr.CreateFromPool(ctx, "vm-pool", 1, "guest", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}

	stored, err := st.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if stored.Properties != nil {
		t.Fatalf("stored resource properties = %+v, want no ephemeral credential", stored.Properties)
	}

	mux := server.NewTestMux(st, mgr, false)
	path := "/api/v1/sandboxes/" + string(sb.ID) + "/guest-credential"
	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body=%s", first.Code, first.Body.String())
	}
	var response struct {
		Credentials []sandbox.GuestCredentialDelivery `json:"credentials"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if len(response.Credentials) != 1 || string(response.Credentials[0].Credential.Data) != `{"username":"Administrator","password":"rotated"}` {
		t.Fatalf("first response = %+v, want one rotated credential", response)
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
	if second.Code != http.StatusGone {
		t.Fatalf("second status = %d, want Gone", second.Code)
	}
}
