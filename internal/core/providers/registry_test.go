package providers

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

type stubDriver struct {
	t model.ProviderType
}

func (d stubDriver) Type() model.ProviderType { return d.t }

func (d stubDriver) ValidateConfig(ctx context.Context, p model.Provider) error {
	_ = ctx
	_ = p
	return nil
}

func TestRegistry_ValidateProviders_UnknownKind(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubDriver{t: "docker"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := r.ValidateProviders(context.Background(), []model.Provider{
		{Name: "p1", Type: "unknown"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}
