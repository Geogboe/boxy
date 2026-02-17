package docker

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

func TestDriver_ValidateConfig_RequiresHostOrContext(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name: "d1",
		Type: "docker",
		Config: map[string]any{
			"namespace": "boxy",
		},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestDriver_ValidateConfig_RejectsHostAndContext(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name: "d1",
		Type: "docker",
		Config: map[string]any{
			"host":    "unix:///var/run/docker.sock",
			"context": "default",
		},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestDriver_ValidateConfig_AcceptsUnixHost(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name: "d1",
		Type: "docker",
		Config: map[string]any{
			"host": "unix:///var/run/docker.sock",
		},
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestDriver_ValidateConfig_TLSRequiresTCP(t *testing.T) {
	t.Parallel()

	d := New()
	err := d.ValidateConfig(context.Background(), providersdk.Instance{
		Name: "d1",
		Type: "docker",
		Config: map[string]any{
			"host": "unix:///var/run/docker.sock",
			"tls": map[string]any{
				"enabled": true,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
