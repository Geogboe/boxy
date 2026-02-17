package docker

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

// Driver implements providersdk.Driver for docker providers.
type Driver struct{}

func New() *Driver { return &Driver{} }

func (*Driver) Type() providersdk.Type { return "docker" }

func (*Driver) ValidateConfig(ctx context.Context, inst providersdk.Instance) error {
	_ = ctx

	cfg, err := DecodeConfig(inst.Config)
	if err != nil {
		return fmt.Errorf("decode docker config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate docker config: %w", err)
	}
	return nil
}
