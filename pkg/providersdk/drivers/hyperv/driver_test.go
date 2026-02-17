package hyperv

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

func TestDriver_ValidateConfig_RequiresEndpoint(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name:   "h1",
		Type:   "hyperv",
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestDriver_ValidateConfig_AcceptsEndpoint(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name: "h1",
		Type: "hyperv",
		Config: map[string]any{
			"endpoint": "win-hyperv-01",
		},
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
