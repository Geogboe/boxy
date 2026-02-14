package docker

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// Driver implements the Boxy Driver interface for docker providers.
type Driver struct{}

func New() *Driver { return &Driver{} }

func (*Driver) Type() model.ProviderType { return "docker" }

func (*Driver) ValidateConfig(ctx context.Context, p model.Provider) error {
	_ = ctx

	cfg, err := DecodeConfig(p.Config)
	if err != nil {
		return fmt.Errorf("decode docker config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate docker config: %w", err)
	}
	return nil
}
