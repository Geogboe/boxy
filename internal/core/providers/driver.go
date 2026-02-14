package providers

import (
	"context"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// Driver is the minimal interface a provider-type implementation exposes to Boxy.
//
// Keep this intentionally small. Add optional capability interfaces as-needed.
type Driver interface {
	Type() model.ProviderType

	// ValidateConfig validates a Provider's config blob for this Driver's kind.
	// Implementations may also perform decoding into a typed config struct.
	ValidateConfig(ctx context.Context, p model.Provider) error
}
